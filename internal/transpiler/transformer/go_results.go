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
//	(T, error)        → Try[T]               std.FromResult(call)
//	(A, B, error)     → Try[Tuple[A, B]]     std.FromResult2(call)   … FromResult10
//	(A, B)            → Tuple[A, B]          std.TupleOf(call)
//	(A, B, C)         → Tuple3[A, B, C]      std.Tuple3Of(call)      … Tuple10Of
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
//     one for one (`template.Must(tmpl.Parse(s))`), which Go spreads,
//   - a statement, whose value is discarded (dropDiscardedGoResults).
//
// A call whose only result is `error` is one value already, and stays an
// `error`: a signature cannot tell a call that fails (`os.Remove`) from one
// that builds an error (`errors.New`). Only Try(...) reads it as a failure.

// goResult records one Go call converted to a GALA value.
type goResult struct {
	raw    *ast.CallExpr   // the Go call itself, yielding every result
	fails  bool            // the last result is `error`: the value is a Try
	typ    transpiler.Type // Try[T], Tuple[A, B], Try[Tuple[A, B]], …; NilType when a result type is unknown
	count  int             // number of results, including a trailing error
	callee string          // the callee as written, for diagnostics ("os.ReadFile"); "" when unavailable
	via    string          // the name the value was read through (`data` of `val data = os.ReadFile(p)`); "" for the call itself
}

// call renders the call for a diagnostic: `os.ReadFile(...)`.
func (r *goResult) call() string {
	if r.callee == "" {
		return "this call"
	}
	return "`" + r.source() + "`"
}

// source renders the call as a pasteable placeholder: `os.ReadFile(...)`, or
// `...` when the callee is not known.
func (r *goResult) source() string {
	if r.callee == "" {
		return "..."
	}
	return r.callee + "(...)"
}

// liftGoResults converts expr, a call just built from its suffix, to one GALA
// value when it calls a Go function with two or more results. Anything else is
// returned unchanged.
func (t *galaASTTransformer) liftGoResults(expr ast.Expr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || t.isImmutableUnwrapCall(call) || t.isGalaCallee(call) {
		return expr, nil
	}
	sig := t.resolveGoCallSignature(call)
	if sig == nil || len(sig.Returns) < 2 {
		return expr, nil
	}
	// The sole argument of a Go function that takes the results one for one
	// stays the raw call; Go spreads it (see markGoSpreadArg).
	if mark := t.goSpreadArg; mark.stop != 0 && suffix != nil && suffix.GetStop() != nil &&
		suffix.GetStop().GetTokenIndex() == mark.stop && len(sig.Returns) == mark.params {
		return expr, nil
	}
	returns := t.resolveGoCallReturnTypes(call)
	if len(returns) != len(sig.Returns) {
		returns = sig.Returns
	}
	value, _ := transpiler.GoResultValueOf(returns)
	res := &goResult{
		raw:    call,
		fails:  value.Fails,
		typ:    value.Type,
		count:  len(returns),
		callee: calleeText(suffix),
	}
	if value.Values > transpiler.MaxGoResultValues {
		line, col := t.lastLine, t.lastCol
		if suffix != nil {
			line, col = suffix.GetStart().GetLine(), suffix.GetStart().GetColumn()
		}
		return nil, galaerr.NewCodedSemanticError(galaerr.CodeGoCallResultAsValue, line, col,
			fmt.Sprintf("%s returns %d values, more than the %d a Tuple holds, so it has no GALA value", res.call(), value.Values, transpiler.MaxGoResultValues),
			fmt.Sprintf("bind the results by name: `val %s = %s`", placeholderNames(res.count, res.fails), res.source()))
	}
	wrapper := &ast.CallExpr{Fun: t.stdHelperIdent(goResultHelper(value.Values, res.fails)), Args: []ast.Expr{call}}
	if t.goResults == nil {
		t.goResults = make(map[*ast.CallExpr]*goResult)
	}
	t.goResults[wrapper] = res
	if !res.typ.IsNil() {
		t.exprTypeCache[wrapper] = res.typ
	}
	return wrapper, nil
}

// goResultHelper names the std function that converts n values (plus an error
// when fails) to one GALA value.
func goResultHelper(n int, fails bool) string {
	switch {
	case fails && n == 1:
		return "FromResult"
	case fails:
		return fmt.Sprintf("FromResult%d", n)
	case n == 2:
		return "TupleOf"
	default:
		return fmt.Sprintf("Tuple%dOf", n)
	}
}

// stdHelperIdent references a std function the way stdIdent does, without
// marking the std import as needed: a converted call that ends up discarded is
// unwrapped again, and an import kept only for it would be unused. The import
// is claimed for the wrappers that survive (dropDiscardedGoResults).
func (t *galaASTTransformer) stdHelperIdent(name string) ast.Expr {
	if t.packageName == registry.StdPackageName || t.importManager.IsDotImported(registry.StdPackageName) {
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
	if end < start {
		return ""
	}
	text := postfix.GetStart().GetInputStream().GetText(start, end)
	if text == "" || len(text) > 60 || strings.ContainsAny(text, "\n\r") {
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
		_, key := t.resolveReceiverTypeAndLookupKey(f.X)
		if meta := t.getTypeMeta(key); meta != nil {
			_, ok := meta.Methods[f.Sel.Name]
			return ok
		}
	}
	return false
}

// isImmutableUnwrapCall reports whether expr is the `.Get()` that
// unwrapImmutable inserts to read a val through its std.Immutable wrapper.
func (t *galaASTTransformer) isImmutableUnwrapCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != transpiler.MethodGet {
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
// or reads a val bound to one. nil otherwise.
func (t *galaASTTransformer) goResultOf(expr ast.Expr) *goResult {
	expr = ast.Unparen(expr)
	if call, ok := expr.(*ast.CallExpr); ok {
		if res := t.goResults[call]; res != nil {
			return res
		}
		if t.isImmutableUnwrapCall(call) {
			expr = call.Fun.(*ast.SelectorExpr).X
		}
	}
	if id, ok := expr.(*ast.Ident); ok {
		if res := t.boundGoResult(id.Name); res != nil {
			named := *res
			named.via = id.Name
			return &named
		}
	}
	return nil
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
// would otherwise evaluate at once as an argument of the helper. The std
// import is claimed for the conversions that remain.
func (t *galaASTTransformer) dropDiscardedGoResults(file *ast.File) {
	if len(t.goResults) == 0 {
		return
	}
	unwrap := func(e ast.Expr) (*ast.CallExpr, bool) {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return nil, false
		}
		res := t.goResults[call]
		if res == nil {
			return nil, false
		}
		return res.raw, true
	}
	dropped := make(map[*ast.CallExpr]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.ExprStmt:
			if raw, ok := unwrap(s.X); ok {
				dropped[s.X.(*ast.CallExpr)] = true
				s.X = raw
			}
		case *ast.GoStmt:
			if raw, ok := unwrap(s.Call); ok {
				dropped[s.Call] = true
				s.Call = raw
			}
		case *ast.DeferStmt:
			if raw, ok := unwrap(s.Call); ok {
				dropped[s.Call] = true
				s.Call = raw
			}
		}
		return true
	})
	if t.packageName == registry.StdPackageName || t.importManager.IsDotImported(registry.StdPackageName) {
		return
	}
	// A surviving conversion needs the std import. Walk the file rather than
	// the map: a conversion built while lowering an alternative that was then
	// thrown away is in the map but not in the file.
	ast.Inspect(file, func(n ast.Node) bool {
		if t.needsStdImport {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && t.goResults[call] != nil && !dropped[call] {
			t.needsStdImport = true
			return false
		}
		return true
	})
}

// tryThunkValue is the value a Try thunk computes from expr: Try(...) already
// turns an error into a Failure, so a Go call's error becomes the panic Try
// catches instead of a Try inside the Try. For a call that returns (T, error),
// or (A, B, error) and so on, that is a func literal running the call and
// panicking on the error; for a Go call returning only `error`, one that
// yields Void. ok is false when expr is neither.
func (t *galaASTTransformer) tryThunkValue(expr ast.Expr) (body *ast.BlockStmt, resultType ast.Expr, ok bool) {
	raw := t.rawGoCall(expr)
	if res := t.goResults[asCall(expr)]; res != nil && !res.fails {
		// A Tuple of values that cannot fail is the thunk's value as it is.
		return nil, nil, false
	}
	if block, retType := t.tryWrapGoMultiReturnWithErrorPanic(raw); block != nil {
		return block, retType, true
	}
	if t.goCallReturnsErrorOnly(raw) == "" {
		return nil, nil, false
	}
	// func() std.Void { if err := call; err != nil { panic(err) }; return std.Void{} }
	errIdent := ast.NewIdent("_err")
	void := t.stdIdent("Void")
	return &ast.BlockStmt{List: []ast.Stmt{
		&ast.IfStmt{
			Init: &ast.AssignStmt{Lhs: []ast.Expr{errIdent}, Tok: token.DEFINE, Rhs: []ast.Expr{raw}},
			Cond: &ast.BinaryExpr{X: ast.NewIdent("_err"), Op: token.NEQ, Y: ast.NewIdent("nil")},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{
				Fun: ast.NewIdent("panic"), Args: []ast.Expr{ast.NewIdent("_err")},
			}}}},
		},
		&ast.ReturnStmt{Results: []ast.Expr{&ast.CompositeLit{Type: void}}},
	}}, t.stdIdent("Void"), true
}

// tryThunkIIFE is tryThunkValue as an expression: the func literal, called.
func (t *galaASTTransformer) tryThunkIIFE(expr ast.Expr) (ast.Expr, bool) {
	body, retType, ok := t.tryThunkValue(expr)
	if !ok {
		return expr, false
	}
	return &ast.CallExpr{Fun: &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: retType}}}},
		Body: body,
	}}, true
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
	if !ok || len(gen.Params) != 1 || stripPackagePrefix(gen.Base.String()) != transpiler.TypeTry {
		return false
	}
	ft, ok := params[argIdx].(transpiler.FuncType)
	return ok && len(ft.Params) == 0 && len(ft.Results) == 1 && ft.Results[0].String() == gen.Params[0].String()
}

// markGoSpreadArg notes the sole argument of a call to a Go function taking
// several parameters, which a Go call returning as many results fills one for
// one (`template.Must(tmpl.Parse(s))`). That argument is left unconverted. It
// returns the function restoring the previous mark.
func (t *galaASTTransformer) markGoSpreadArg(base ast.Expr, argList *grammar.ArgumentListContext) func() {
	prev := t.goSpreadArg
	restore := func() { t.goSpreadArg = prev }
	t.goSpreadArg = goSpreadArgMark{}
	if argList == nil || len(argList.AllArgument()) != 1 {
		return restore
	}
	arg := argList.AllArgument()[0].(*grammar.ArgumentContext)
	if arg.Identifier() != nil || arg.GetStop() == nil {
		return restore
	}
	call := &ast.CallExpr{Fun: base}
	sig := t.lookupGoCallSignature(call)
	if sig == nil || sig.IsVariadic || len(sig.Params) < 2 || t.isGalaCallee(call) {
		return restore
	}
	t.goSpreadArg = goSpreadArgMark{stop: arg.GetStop().GetTokenIndex(), params: len(sig.Params)}
	return restore
}

// goSpreadArgMark identifies the argument markGoSpreadArg leaves unconverted:
// the token that ends it, and the parameter count a call there must return.
type goSpreadArgMark struct {
	stop   int // 0 when no argument is marked
	params int
}

// checkGoResultAgainst reports a converted Go call — or a val bound to one —
// standing where its plain value is expected: `val data = os.ReadFile(p)`
// passed to a `[]byte` parameter. Go would report a type mismatch against
// generated code; this names the call and the ways to reach the value.
func (t *galaASTTransformer) checkGoResultAgainst(expr ast.Expr, expected transpiler.Type, ctx antlr.ParserRuleContext) error {
	res := t.goResultOf(expr)
	if res == nil || res.typ.IsNil() || !t.cannotHold(expected, res.typ) {
		return nil
	}
	return t.goResultMisuse(res, fmt.Sprintf("`%s` is expected here", displayType(expected)), ctx)
}

// checkGoResultGoArg is checkGoResultAgainst for argument i of a call to a Go
// function or method, whose parameter types come from its Go signature rather
// than from a slot (sig may be nil: nothing is checked).
func (t *galaASTTransformer) checkGoResultGoArg(sig *transpiler.GoFuncSignature, i int, expr ast.Expr, ctx antlr.ParserRuleContext) error {
	if sig == nil || len(sig.Params) == 0 {
		return nil
	}
	var param transpiler.Type
	switch {
	case i < len(sig.Params) && !(sig.IsVariadic && i == len(sig.Params)-1):
		param = sig.Params[i].Type
	case sig.IsVariadic && i >= len(sig.Params)-1:
		// Stored element-wise: `...string` is recorded as string.
		param = sig.Params[len(sig.Params)-1].Type
	default:
		return nil
	}
	return t.checkGoResultAgainst(expr, param, ctx)
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
	if expected.String() == got.String() || stripPackagePrefix(expected.String()) == stripPackagePrefix(got.String()) {
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
	if res == nil || res.typ.IsNil() {
		return nil
	}
	gen, ok := res.typ.(transpiler.GenericType)
	if !ok {
		return nil
	}
	meta := t.getTypeMeta(gen.Base.String())
	if meta == nil {
		return nil
	}
	if _, ok := meta.Methods[member]; ok {
		return nil
	}
	if _, ok := meta.Fields[member]; ok || isSynthesizedMethodName(member) {
		return nil
	}
	for _, v := range meta.SealedVariants {
		for _, f := range v.FieldNames {
			if f == member {
				return nil
			}
		}
	}
	return t.goResultMisuse(res, fmt.Sprintf("a %s has no member `%s`", genericBaseName(res.typ), member), ctx)
}

// checkGoResultOperands reports a converted Go call — or a val bound to one —
// used as an operand of op: `strconv.Atoi(s) * 2`. No operator applies to a
// Try or a Tuple.
func (t *galaASTTransformer) checkGoResultOperands(op string, ctx antlr.ParserRuleContext, operands ...ast.Expr) error {
	for _, e := range operands {
		if res := t.goResultOf(e); res != nil && !res.typ.IsNil() {
			return t.goResultMisuse(res, fmt.Sprintf("it cannot be an operand of `%s`", op), ctx)
		}
	}
	return nil
}

// checkGoResultTupleDestructure reports `val (a, b) = goCall()` over a Go call
// that can fail: its value is a Try, not a Tuple.
func (t *galaASTTransformer) checkGoResultTupleDestructure(expr ast.Expr, ctx antlr.ParserRuleContext) error {
	res := t.goResultOf(expr)
	if res == nil || !res.fails {
		return nil
	}
	return t.goResultMisuse(res, "a Try is not a Tuple, so it cannot be destructured with `val (...)`", ctx)
}

// goResultMisuse builds the GALA-E0049 diagnostic for a converted Go call used
// as something it is not.
func (t *galaASTTransformer) goResultMisuse(res *goResult, what string, ctx antlr.ParserRuleContext) error {
	var msg, hint string
	shape := fmt.Sprintf("returns %d values", res.count)
	if res.fails {
		shape = "can fail"
	}
	if res.via == "" {
		msg = fmt.Sprintf("%s %s, so it produces `%s`; %s", res.call(), shape, displayType(res.typ), what)
	} else {
		msg = fmt.Sprintf("`%s` holds the result of %s, which %s, so it is a `%s`; %s", res.via, res.call(), shape, displayType(res.typ), what)
	}
	if res.fails {
		hint = fmt.Sprintf("take the value with `.Get()` (panics on failure), `.GetOrElse(default)`, or `match { case Success(v) => ... case Failure(e) => ... }`; or bind the results Go-style: `val %s = %s`",
			placeholderNames(res.count, true), res.source())
	} else {
		names := placeholderNames(res.count, false)
		hint = fmt.Sprintf("read the values with `val (%s) = ...` or `.V1`, `.V2`, ...; or bind the results Go-style: `val %s = %s`",
			names, names, res.source())
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

// genericBaseName is the bare name of a generic type: Try, Tuple3.
func genericBaseName(typ transpiler.Type) string {
	if gen, ok := typ.(transpiler.GenericType); ok {
		return stripPackagePrefix(gen.Base.String())
	}
	return displayType(typ)
}

// displayType renders a type as GALA source spells it.
func displayType(typ transpiler.Type) string {
	return strings.ReplaceAll(typ.String(), registry.StdPackageName+".", "")
}

// placeholderNames renders placeholder binding names for n results, the last
// one `err` when the call fails: `a, b, c` / `v, err` / `a, b, err`.
func placeholderNames(n int, fails bool) string {
	if fails && n == 2 {
		return "v, err"
	}
	names := make([]string, n)
	for i := range names {
		names[i] = string(rune('a' + i))
	}
	if fails {
		names[n-1] = "err"
	}
	return strings.Join(names, ", ")
}
