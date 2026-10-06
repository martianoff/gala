package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// A Go call's results as one GALA value
//
// GALA has no multi-value expressions: an expression is one value. A Go
// function that returns several results is therefore presented as ONE value
// wherever its call is used as a value:
//
//	(T, error)        → Try[T]               std.GoTry(call)
//	(A, B, error)     → Try[Tuple[A, B]]     std.GoTry2(call)    … GoTry10
//	(A, B)            → Tuple[A, B]          std.GoTuple(call)
//	(A, B, C)         → Tuple3[A, B, C]      std.GoTuple3(call)  … GoTuple10
//
// Go spreads a multi-value call over the parameters of the function it is
// passed to (`f(g())`), and each helper takes exactly the call's results, so
// the generated Go is the call wrapped once and the helper's type arguments
// are left to Go's inference.
//
// The conversion is made where the call is built (applyPostfixSuffix), so
// every single-value position sees the GALA value: a binding, a match
// subject, an argument, a lambda or function result, a branch, a receiver.
// The few positions that take the call's results one by one read the raw call
// back out of the wrapper (rawGoCall):
//
//   - a multi-name binding, `val a, err = goCall()` (also var, := and `=`),
//   - `Try(goCall())` / `Try(() => goCall())`, which already mean "run this and
//     turn an error into a Failure", so they keep producing Try[T] rather
//     than Try[Try[T]] (see tryThunkValue),
//   - the sole argument of a Go function whose parameters take the results
//     one for one (`template.Must(tmpl.Parse(s))`), which Go spreads
//     (spreadGoResultArg),
//   - a statement, whose value is discarded (dropDiscardedGoResults).
//
// A call whose only result is `error` is one value already, and stays an
// `error`: a signature cannot tell a call that fails (`os.Remove`) from one
// that builds an error (`errors.New`). Only Try(...) reads it as a failure.

// goResult records one Go call converted to a GALA value.
type goResult struct {
	transpiler.GoResultValue
	raw    *ast.CallExpr                 // the Go call itself, yielding every result
	suffix *grammar.PostfixSuffixContext // the call's argument list, for quoting the callee in a diagnostic
	via    string                        // the name the value was read through (`data` of `val data = os.ReadFile(p)`); "" for the call itself
}

// count is the number of the call's results, a trailing error included.
func (r *goResult) count() int {
	if r.Fails {
		return r.Values + 1
	}
	return r.Values
}

// call renders the call for a diagnostic: `os.ReadFile(...)`.
func (r *goResult) call() string {
	if calleeText(r.suffix) == "" {
		return "this call"
	}
	return "`" + r.source() + "`"
}

// source renders the call as a pasteable placeholder: `os.ReadFile(...)`, or
// `...` when the callee is not worth quoting.
func (r *goResult) source() string {
	callee := calleeText(r.suffix)
	if callee == "" {
		return "..."
	}
	return callee + "(...)"
}

// liftGoResults converts expr, a call just built from its suffix, to one GALA
// value when it calls a Go function with two or more results. Anything else is
// returned unchanged.
func (t *galaASTTransformer) liftGoResults(expr ast.Expr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return expr, nil
	}
	// The signature first: it answers at once for almost every call (no Go
	// type info, or a single result), before the guards that infer types.
	sig := t.resolveGoCallSignature(call)
	if sig != nil && (len(sig.Returns) < 2 || t.isImmutableUnwrapCall(call) || t.isGalaCallee(call)) {
		sig = nil
	}
	if sig == nil {
		// A GALA function declaring a Go result list, or a value of a
		// function type with several results, is called as Go's are.
		sig = t.declaredGoResultsSignature(call)
	}
	if sig == nil {
		return expr, nil
	}
	returns := t.instantiateGoSignatureReturns(sig, call.Args, t.callSiteTypeArgs(call), call.Ellipsis != token.NoPos)
	value, _ := transpiler.GoResultValueOf(returns)
	res := &goResult{GoResultValue: value, raw: call, suffix: suffix}
	if value.Values > transpiler.MaxGoResultValues {
		line, col := t.lastLine, t.lastCol
		if suffix != nil {
			line, col = suffix.GetStart().GetLine(), suffix.GetStart().GetColumn()
		}
		return nil, galaerr.NewCodedSemanticError(galaerr.CodeGoCallResultAsValue, line, col,
			fmt.Sprintf("%s returns %d values, more than the %d a Tuple holds, so it has no GALA value", res.call(), value.Values, transpiler.MaxGoResultValues),
			fmt.Sprintf("bind the results by name: `val %s = %s`", transpiler.PlaceholderNames(res.count(), res.Fails), res.source()))
	}
	wrapper := &ast.CallExpr{Fun: t.stdHelperIdent(goResultHelper(value.Values, res.Fails)), Args: []ast.Expr{call}}
	if t.goResults == nil {
		t.goResults = make(map[*ast.CallExpr]*goResult)
	}
	t.goResults[wrapper] = res
	if !res.Type.IsNil() {
		t.exprTypeCache[wrapper] = res.Type
	}
	return wrapper, nil
}

// goResultHelper names the std function that converts n values (plus an error
// when fails) to one GALA value.
func goResultHelper(n int, fails bool) string {
	switch {
	case fails && n == 1:
		return "GoTry"
	case fails:
		return fmt.Sprintf("GoTry%d", n)
	case n == 2:
		return "GoTuple"
	default:
		return fmt.Sprintf("GoTuple%d", n)
	}
}

// stdIsInScope reports whether std's names are reachable without a qualifier:
// in std itself, or where it is dot-imported.
func (t *galaASTTransformer) stdIsInScope() bool {
	return t.packageName == registry.StdPackageName || t.importManager.IsDotImported(registry.StdPackageName)
}

// stdHelperIdent references a std function the way stdIdent does, without
// marking the std import as needed: a converted call that ends up discarded is
// unwrapped again, and an import kept only for it would be unused. The import
// is claimed for the wrappers that survive (dropDiscardedGoResults).
func (t *galaASTTransformer) stdHelperIdent(name string) ast.Expr {
	if t.stdIsInScope() {
		return ast.NewIdent(name)
	}
	return &ast.SelectorExpr{X: ast.NewIdent(registry.StdPackageName), Sel: ast.NewIdent(name)}
}

// calleeText is the source text of the callee of the call suffix — everything
// in its postfix expression before the argument list — or "" when that is not
// a short single-line expression worth quoting.
func calleeText(suffix *grammar.PostfixSuffixContext) string {
	if suffix == nil {
		return ""
	}
	postfix, ok := suffix.GetParent().(*grammar.PostfixExprContext)
	if !ok || postfix.GetStart() == nil || suffix.GetStart() == nil {
		return ""
	}
	start, end := postfix.GetStart().GetStart(), suffix.GetStart().GetStart()-1
	if end < start || end-start >= 60 {
		return ""
	}
	text := postfix.GetStart().GetInputStream().GetText(start, end)
	if text == "" || strings.ContainsAny(text, "\n\r") {
		return ""
	}
	return text
}

// isGalaCallee reports whether call names a GALA function or method, which
// never returns several values even when a Go function of the same name does.
func (t *galaASTTransformer) isGalaCallee(call *ast.CallExpr) bool {
	fun, _ := splitCallFunTypeArgs(call.Fun)
	switch f := fun.(type) {
	case *ast.Ident:
		return t.getFunction(f.Name) != nil
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok && t.importManager.IsPackage(id.Name) {
			return t.getFunction(id.Name+"."+f.Sel.Name) != nil
		}
		_, key := t.resolveReceiverTypeAndLookupKey(f.X, f.Sel.Name)
		if meta := t.getTypeMeta(key); meta != nil {
			// A Go type's metadata, synthesized from Go, holds Go methods.
			m, ok := meta.Methods[f.Sel.Name]
			return ok && !m.GoDeclared
		}
	}
	return false
}

// declaredGoResultsSignature returns the signature of a call whose callee GALA
// declares with several Go results: a GALA function or method with a Go
// result list (`func (c *Counter) Write(p []byte) (int, error)`; a generic
// one is lowered to a free function, see recordGenericGoResultCall), or a local
// value of a function type with several results (`val get =
// sync.OnceValues(...)`). nil for any other call.
func (t *galaASTTransformer) declaredGoResultsSignature(call *ast.CallExpr) *transpiler.GoFuncSignature {
	if sig := t.genericGoResultCalls[call]; sig != nil {
		return sig
	}
	fun, _ := splitCallFunTypeArgs(call.Fun)
	// A val is called through its Immutable wrapper: `get.Get()()`.
	if sel := immutableGetReceiver(fun); sel != nil {
		if id, ok := sel.X.(*ast.Ident); ok && t.isVal(id.Name) {
			fun = id
		}
	}
	switch f := fun.(type) {
	case *ast.Ident:
		if t.shadowingScope(f.Name) != nil {
			if ft := t.resolveTranspilerTypeAsFuncType(t.getType(f.Name)); ft != nil && len(ft.Results) >= 2 {
				return &transpiler.GoFuncSignature{Params: goParams(ft.Params, nil), Returns: ft.Results}
			}
			return nil
		}
		if fm := t.getFunction(f.Name); fm != nil && fm.GoResults != nil {
			return &transpiler.GoFuncSignature{Params: goParams(fm.ParamTypes, fm.ParamNames), Returns: fm.GoResults, TypeParams: fm.TypeParams}
		}
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok && t.importManager.IsPackage(id.Name) {
			if fm := t.getFunction(id.Name + "." + f.Sel.Name); fm != nil && fm.GoResults != nil {
				return &transpiler.GoFuncSignature{Params: goParams(fm.ParamTypes, fm.ParamNames), Returns: fm.GoResults, TypeParams: fm.TypeParams}
			}
			return nil
		}
		_, key := t.resolveReceiverTypeAndLookupKey(f.X, f.Sel.Name)
		if meta := t.getTypeMeta(key); meta != nil {
			if m := meta.Methods[f.Sel.Name]; m != nil && m.GoResults != nil {
				return &transpiler.GoFuncSignature{Params: goParams(m.ParamTypes, m.ParamNames), Returns: m.GoResults, TypeParams: m.TypeParams}
			}
		}
	}
	return nil
}

// goParams pairs parameter types with their names (names may be nil).
func goParams(types []transpiler.Type, names []string) []transpiler.GoParam {
	params := make([]transpiler.GoParam, len(types))
	for i, typ := range types {
		params[i].Type = typ
		if i < len(names) {
			params[i].Name = names[i]
		}
	}
	return params
}

// isImmutableUnwrapCall reports whether expr is the `.Get()` that
// unwrapImmutable inserts to read a val through its std.Immutable wrapper.
func (t *galaASTTransformer) isImmutableUnwrapCall(expr ast.Expr) bool {
	sel := immutableGetReceiver(expr)
	if sel == nil {
		return false
	}
	// A val's scope entry records the unwrapped type, so the wrapper is
	// recognised by the name being a val. A user-written `v.Get()` on a val
	// arrives as `v.Get().Get()`, whose receiver is a call, not the name.
	if id, ok := sel.X.(*ast.Ident); ok && t.isVal(id.Name) {
		return true
	}
	return t.isImmutableType(t.getExprTypeName(sel.X))
}

// immutableGetReceiver returns the selector of a zero-argument `.Get()` call,
// the shape of an Immutable unwrap, and nil for anything else.
func immutableGetReceiver(expr ast.Expr) *ast.SelectorExpr {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return nil
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != transpiler.MethodGet {
		return nil
	}
	return sel
}

// rawGoCall returns the Go call inside a converted value, yielding every
// result, and expr itself otherwise. Positions that take a call's results one
// by one read it back through here.
func (t *galaASTTransformer) rawGoCall(expr ast.Expr) ast.Expr {
	if call, ok := expr.(*ast.CallExpr); ok {
		if res := t.goResults[call]; res != nil {
			return res.raw
		}
	}
	return expr
}

// goResultOf returns the conversion expr carries: expr is a converted Go call,
// or reads a val bound to one. nil otherwise, and nil when the converted
// value's type is unknown, since no misuse can then be judged.
func (t *galaASTTransformer) goResultOf(expr ast.Expr) *goResult {
	// Nothing is converted until a Go call is, so most files stop here.
	if len(t.goResults) == 0 {
		return nil
	}
	expr = ast.Unparen(expr)
	if call, ok := expr.(*ast.CallExpr); ok {
		if res := t.goResults[call]; res != nil {
			return typedGoResult(res)
		}
		// A val read through its Immutable wrapper: `data.Get()`.
		if sel := immutableGetReceiver(call); sel != nil {
			expr = sel.X
		}
	}
	if id, ok := expr.(*ast.Ident); ok && (t.isVal(id.Name) || t.isVar(id.Name)) {
		if res := t.boundGoResult(id.Name); res != nil {
			named := *res
			named.via = id.Name
			return typedGoResult(&named)
		}
	}
	return nil
}

func typedGoResult(res *goResult) *goResult {
	if res.Type.IsNil() {
		return nil
	}
	return res
}

// bindGoResult remembers that name is bound to a converted Go call, so a later
// misuse of the name can say where its Try or Tuple came from.
func (t *galaASTTransformer) bindGoResult(name string, value ast.Expr) {
	if t.currentScope == nil {
		return
	}
	res := t.goResultOf(value)
	if res == nil {
		if t.currentScope.goResults != nil {
			delete(t.currentScope.goResults, name)
		}
		return
	}
	if t.currentScope.goResults == nil {
		t.currentScope.goResults = make(map[string]*goResult)
	}
	t.currentScope.goResults[name] = res
}

// boundGoResult finds the conversion a name was bound to in the innermost scope
// that binds the name.
func (t *galaASTTransformer) boundGoResult(name string) *goResult {
	for s := t.currentScope; s != nil; s = s.parent {
		if _, bound := s.vals[name]; bound {
			return s.goResults[name]
		}
	}
	return nil
}

// dropDiscardedGoResults unwraps converted calls whose value is discarded: an
// expression statement, and the call of a `go` or `defer` statement, which Go
// would otherwise evaluate at once as an argument of the helper. A conversion
// that remains claims the std import.
//
// One walk does both: ast.Inspect visits a node's children after the node, so
// a wrapper replaced by its raw call is never visited, and every conversion the
// walk meets is one that stays. The file is walked rather than the map because
// a conversion built while lowering an alternative that was then thrown away
// is in the map but not in the file.
func (t *galaASTTransformer) dropDiscardedGoResults(file *ast.File) {
	if len(t.goResults) == 0 {
		return
	}
	raw := func(call *ast.CallExpr) *ast.CallExpr {
		if res := t.goResults[call]; res != nil {
			return res.raw
		}
		return call
	}
	needsStd := !t.stdIsInScope()
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.ExprStmt:
			s.X = t.rawGoCall(s.X)
		case *ast.GoStmt:
			s.Call = raw(s.Call)
		case *ast.DeferStmt:
			s.Call = raw(s.Call)
		case *ast.CallExpr:
			if needsStd && t.goResults[s] != nil {
				t.needsStdImport = true
			}
		}
		return true
	})
}

// spreadGoResultArg returns the raw Go call when arg is the sole argument
// (argCount 1) of a call to a Go function whose parameters take that call's
// results one for one — `template.Must(tmpl.Parse(text))`, which Go spreads —
// and arg itself otherwise. sig may be nil.
func (t *galaASTTransformer) spreadGoResultArg(sig *transpiler.GoFuncSignature, argCount int, arg ast.Expr) ast.Expr {
	if sig == nil || sig.IsVariadic || argCount != 1 || len(sig.Params) < 2 {
		return arg
	}
	if res := t.goResults[asCall(arg)]; res != nil && res.count() == len(sig.Params) {
		return res.raw
	}
	return arg
}

// tryThunkValue is the value a Try thunk computes from expr: Try(...) already
// turns an error into a Failure, so a Go call's error becomes the panic Try
// catches instead of a Try inside the Try. For a call that returns (T, error),
// or (A, B, error) and so on, that is a func body running the call and
// panicking on the error; for a Go call returning only `error`, one that
// yields Void. ok is false when expr is neither.
func (t *galaASTTransformer) tryThunkValue(expr ast.Expr) (body *ast.BlockStmt, resultType ast.Expr, ok bool) {
	if res := t.goResults[asCall(expr)]; res != nil && !res.Fails {
		// A Tuple of values that cannot fail is the thunk's value as it is.
		return nil, nil, false
	}
	raw := t.rawGoCall(expr)
	if block, retType := t.tryWrapGoMultiReturnWithErrorPanic(raw); block != nil {
		return block, retType, true
	}
	if !t.isGoErrorOnlyCall(raw) {
		return nil, nil, false
	}
	// func() std.Void { if _err := call; _err != nil { panic(_err) }; return std.Void{} }
	void := t.stdIdent("Void")
	return &ast.BlockStmt{List: []ast.Stmt{
		&ast.IfStmt{
			Init: &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("_err")}, Tok: token.DEFINE, Rhs: []ast.Expr{raw}},
			Cond: &ast.BinaryExpr{X: ast.NewIdent("_err"), Op: token.NEQ, Y: ast.NewIdent("nil")},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{
				Fun: ast.NewIdent("panic"), Args: []ast.Expr{ast.NewIdent("_err")},
			}}}},
		},
		&ast.ReturnStmt{Results: []ast.Expr{&ast.CompositeLit{Type: void}}},
	}}, void, true
}

// isGoErrorOnlyCall reports whether expr calls a Go function or method whose
// only result is `error` — any callee shape resolveGoCallSignature knows,
// dot-imported and generic ones included.
func (t *galaASTTransformer) isGoErrorOnlyCall(expr ast.Expr) bool {
	sig := t.resolveGoCallSignature(expr)
	if sig == nil || len(sig.Returns) != 1 || sig.Returns[0] == nil || sig.Returns[0].String() != "error" {
		return false
	}
	return !t.isGalaCallee(expr.(*ast.CallExpr))
}

// thunkLit is the zero-parameter func literal returning resultType.
func thunkLit(body *ast.BlockStmt, resultType ast.Expr) *ast.FuncLit {
	return &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: resultType}}}},
		Body: body,
	}
}

// tryThunkIIFE is tryThunkValue as an expression: the func literal, called.
func (t *galaASTTransformer) tryThunkIIFE(expr ast.Expr) ast.Expr {
	body, retType, ok := t.tryThunkValue(expr)
	if !ok {
		return expr
	}
	return &ast.CallExpr{Fun: thunkLit(body, retType)}
}

func asCall(expr ast.Expr) *ast.CallExpr {
	call, _ := expr.(*ast.CallExpr)
	return call
}

// isTryThunkParam reports whether argument argIdx of the call is the thunk of a
// function that turns a panic into a Failure: the parameter is `func() T` and
// the callee returns `Try[T]` — `Try(...)` and `TryApply(...)`.
func isTryThunkParam(callCtx callContext, argIdx int) bool {
	var params []transpiler.Type
	var ret transpiler.Type
	switch {
	case callCtx.applyMethodMeta != nil:
		params, ret = callCtx.applyMethodMeta.ParamTypes, callCtx.applyMethodMeta.ReturnType
	case callCtx.funcMeta != nil:
		params, ret = callCtx.funcMeta.ParamTypes, callCtx.funcMeta.ReturnType
	}
	if argIdx >= len(params) || ret == nil {
		return false
	}
	gen, ok := ret.(transpiler.GenericType)
	if !ok || len(gen.Params) != 1 {
		return false
	}
	if base, ok := gen.Base.(transpiler.NamedType); !ok || base.Name != transpiler.TypeTry {
		return false
	}
	ft, ok := params[argIdx].(transpiler.FuncType)
	return ok && len(ft.Params) == 0 && len(ft.Results) == 1 && ft.Results[0].String() == gen.Params[0].String()
}

// checkGoResultAgainst reports a converted Go call — or a val bound to one —
// standing where its plain value is expected: `val data = os.ReadFile(p)`
// passed to a `[]byte` parameter. Go would report a type mismatch against
// generated code; this names the call and the ways to reach the value.
func (t *galaASTTransformer) checkGoResultAgainst(expr ast.Expr, expected transpiler.Type, ctx antlr.ParserRuleContext) error {
	res := t.goResultOf(expr)
	if res == nil || !t.cannotHold(expected, res.Type) {
		return nil
	}
	return t.goResultMisuse(res, fmt.Sprintf("`%s` is expected here", displayType(expected)), ctx)
}

// checkGoResultGoArg is checkGoResultAgainst for argument i of a call to a Go
// function or method, whose parameter types come from its Go signature rather
// than from a slot (sig may be nil: nothing is checked). It also runs the
// ConstPtr-in-interface check (GALA-E0070) the slot would have run.
func (t *galaASTTransformer) checkGoResultGoArg(sig *transpiler.GoFuncSignature, i int, expr ast.Expr, ctx antlr.ParserRuleContext) error {
	param := goSigParamType(sig, i)
	if param.IsNil() {
		return nil
	}
	if err := t.checkGoResultAgainst(expr, param, ctx); err != nil {
		return err
	}
	// A Go interface parameter is not lowered against (goParamSlot), so a
	// val's read-only address passed for it is checked here.
	return t.checkConstPtrInterface(expr, param, ctx)
}

// cannotHold reports whether a slot of type expected certainly cannot hold a
// value of type got. It is conservative: an interface, a type parameter or an
// unresolved type may hold anything, so only concrete non-interface shapes are
// judged.
func (t *galaASTTransformer) cannotHold(expected, got transpiler.Type) bool {
	if transpiler.IsUnusable(expected) || expected.IsAny() || t.hasTypeParams(expected) {
		return false
	}
	// A val slot, Immutable[T], is filled with a T.
	if gen, ok := expected.(transpiler.GenericType); ok && len(gen.Params) == 1 && t.isImmutableType(expected) {
		return t.cannotHold(gen.Params[0], got)
	}
	if stripPackagePrefix(expected.String()) == stripPackagePrefix(got.String()) {
		return false
	}
	switch e := expected.(type) {
	case transpiler.BasicType:
		return e.Name != "error" && e.Name != "interface{}"
	case transpiler.FuncType:
		// A `func() T` slot takes a plain value as its thunk (`Future(x)`).
		return len(e.Params) > 0
	case transpiler.ArrayType, transpiler.MapType, transpiler.PointerType:
		return true
	case transpiler.GenericType:
		// A GALA generic type (Array[T], Option[T], a Tuple, Try[U]); its
		// type arguments are concrete, checked above.
		return isStructOrSealed(t.getTypeMeta(stripTypeNameDecorations(e.Base.String())))
	case transpiler.NamedType:
		return isStructOrSealed(t.getTypeMeta(e.String()))
	}
	return false
}

// isStructOrSealed reports whether meta describes a struct or sealed type —
// never an interface, which a Try or Tuple might satisfy.
func isStructOrSealed(meta *transpiler.TypeMetadata) bool {
	return meta != nil && (meta.IsSealed || len(meta.FieldNames) > 0)
}

// checkGoResultMember reports a member the converted value does not have, read
// off a converted Go call or a val bound to one: `val resp = http.Get(url)`
// then `resp.StatusCode`. A Try or Tuple member (Map, GetOrElse, V1) is fine.
func (t *galaASTTransformer) checkGoResultMember(base ast.Expr, member string, ctx antlr.ParserRuleContext) error {
	res := t.goResultOf(base)
	if res == nil {
		return nil
	}
	gen, ok := res.Type.(transpiler.GenericType)
	if !ok {
		return nil
	}
	meta := t.getTypeMeta(gen.Base.String())
	if meta == nil || hasMember(meta, member) {
		return nil
	}
	return t.goResultMisuse(res, fmt.Sprintf("a %s has no member `%s`", stripPackagePrefix(gen.Base.String()), member), ctx)
}

// hasMember reports whether a type declares member as a method or field, of
// itself or of one of its sealed cases, or has it synthesized.
func hasMember(meta *transpiler.TypeMetadata, member string) bool {
	if _, ok := meta.Methods[member]; ok {
		return true
	}
	if _, ok := meta.Fields[member]; ok || isSynthesizedMethodName(member) {
		return true
	}
	for _, v := range meta.SealedVariants {
		for _, f := range v.FieldNames {
			if f == member {
				return true
			}
		}
	}
	return false
}

// checkGoResultOperands reports a converted Go call — or a val bound to one —
// used as an operand of op: `strconv.Atoi(s) * 2`. A Tuple compares with
// `==` and `!=` like any Tuple; no other operator applies to a Try or a Tuple.
func (t *galaASTTransformer) checkGoResultOperands(op string, ctx antlr.ParserRuleContext, operands ...ast.Expr) error {
	for _, e := range operands {
		if res := t.goResultOf(e); res != nil && !(!res.Fails && (op == "==" || op == "!=")) {
			return t.goResultMisuse(res, fmt.Sprintf("it cannot be an operand of `%s`", op), ctx)
		}
	}
	return nil
}

// checkGoResultTupleDestructure reports `val (a, b) = goCall()` (or `var`) over a Go call
// that can fail: its value is a Try, not a Tuple.
func (t *galaASTTransformer) checkGoResultTupleDestructure(expr ast.Expr, keyword string, ctx antlr.ParserRuleContext) error {
	res := t.goResultOf(expr)
	if res == nil || !res.Fails {
		return nil
	}
	return t.goResultMisuse(res, fmt.Sprintf("a Try is not a Tuple, so it cannot be destructured with `%s (...)`", keyword), ctx)
}

// goResultMisuse builds the GALA-E0049 diagnostic for a converted Go call used
// as something it is not.
func (t *galaASTTransformer) goResultMisuse(res *goResult, what string, ctx antlr.ParserRuleContext) error {
	shape := fmt.Sprintf("returns %d values", res.count())
	if res.Fails {
		shape = "can fail"
	}
	var msg string
	if res.via == "" {
		msg = fmt.Sprintf("%s %s, so it produces `%s`; %s", res.call(), shape, displayType(res.Type), what)
	} else {
		msg = fmt.Sprintf("`%s` holds the result of %s, which %s, so it is a `%s`; %s", res.via, res.call(), shape, displayType(res.Type), what)
	}
	names := transpiler.PlaceholderNames(res.count(), res.Fails)
	hint := fmt.Sprintf("read the values with `val (%s) = ...` or `.V1`, `.V2`, ...; or bind the results Go-style: `val %s = %s`",
		names, names, res.source())
	if res.Fails {
		hint = fmt.Sprintf("take the value with `.Get()` (panics on failure), `.GetOrElse(default)`, or `match { case Success(v) => ... case Failure(e) => ... }`; or bind the results Go-style: `val %s = %s`",
			names, res.source())
	}
	line, col := t.lastLine, t.lastCol
	if ctx != nil && ctx.GetStart() != nil {
		line, col = ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
	}
	err := galaerr.NewCodedSemanticError(galaerr.CodeGoCallResultAsValue, line, col, msg, hint)
	if ctx != nil && ctx.GetStart() != nil && ctx.GetStop() != nil && ctx.GetStop().GetLine() == line {
		err = err.WithSpan(ctx.GetStop().GetColumn() + len([]rune(ctx.GetStop().GetText())))
	}
	return err
}

// displayType renders a type as GALA source spells it.
func displayType(typ transpiler.Type) string {
	return strings.ReplaceAll(typ.String(), registry.StdPackageName+".", "")
}
