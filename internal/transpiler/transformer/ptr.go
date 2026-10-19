package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// Non-null pointers
//
// `Ptr[T]` is a pointer that is never nil. It is a compile-time marker: the
// generated Go spells it `*T`, so receivers, field access, method sets and Go
// calls are those of `*T`. In the type system it is a transpiler.PointerType
// with NonNull set. `&x` produces one, a `Ptr[T]` fills a `*T` slot as it is,
// and a nullable `*T` (or `nil`) never fills a `Ptr[T]` slot (GALA-E0080): a
// Go pointer becomes a `Ptr` only through RequirePtr, which checks it, or an
// OptionPtr through OptionPtrOf. A Ptr has no default value (GALA-E0075).
//
// The generated `*T` of a `Ptr[T]` is recorded in ptrStars, so a type read
// back from generated Go (astTypeToTranspilerType) keeps the marker.

// ptrMayBeAbsentHint is the hint for a nil where a Ptr is needed.
const ptrMayBeAbsentHint = "a pointer that may be absent is an OptionPtr[T] (EmptyOptionPtr(), OptionPtrOf(p)); a Ptr always points somewhere"

// namesPtrType reports whether expr, the base of an index expression in
// expression position (a type argument, `None[Ptr[T]]()`), names the `Ptr`
// marker: `Ptr` or `std.Ptr`, as no binding names it.
func (t *galaASTTransformer) namesPtrType(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == galaparser.PtrTypeName && t.bindingScope(e.Name) == nil
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		return ok && e.Sel.Name == galaparser.PtrTypeName && t.bindingScope(pkg.Name) == nil &&
			registry.Global.IsPreludePackage(pkg.Name)
	}
	return false
}

// ptrStar is the Go type `*elem` of a non-null `Ptr[elem]`.
func (t *galaASTTransformer) ptrStar(elem ast.Expr) *ast.StarExpr {
	star := &ast.StarExpr{X: elem}
	if t.ptrStars == nil {
		t.ptrStars = make(map[*ast.StarExpr]bool)
	}
	t.ptrStars[star] = true
	return star
}

// isPtrTypeExpr reports whether typeExpr is the generated `*T` of a `Ptr[T]`.
func (t *galaASTTransformer) isPtrTypeExpr(typeExpr ast.Expr) bool {
	star, ok := typeExpr.(*ast.StarExpr)
	return ok && t.ptrStars[star]
}

// ptrError is a GALA-E0080 at ctx.
func ptrError(ctx antlr.ParserRuleContext, msg, hint string) error {
	return spanError(galaerr.CodePtrNullability, ctx, ctx, msg, hint)
}

// checkPtrSlot rejects a value that may be nil filling a slot whose type has
// a `Ptr` where the value's type has a nullable pointer (GALA-E0080): `nil`
// or a `*T` where a `Ptr[T]` is expected, or an `Array[*T]` where an
// `Array[Ptr[T]]` is. A value whose type is unknown is left alone.
func (t *galaASTTransformer) checkPtrSlot(expr ast.Expr, expected transpiler.Type, exprCtx antlr.ParserRuleContext) error {
	if transpiler.IsUnusable(expected) || exprCtx == nil {
		return nil
	}
	expected = t.followAliasChain(expected)
	// Most slots hold no Ptr: skip the expression's type inference for them.
	if !hasPtr(expected) {
		return t.checkPtrInterfaceSlot(expr, expected, exprCtx)
	}
	if isNilIdent(expr) {
		if !transpiler.IsPtr(expected) {
			return nil
		}
		return ptrError(exprCtx, fmt.Sprintf("cannot use nil as %s: a Ptr is never nil", displayType(expected)), ptrMayBeAbsentHint)
	}
	actual := t.probeExprType(expr)
	if transpiler.IsUnusable(actual) || !nullableWherePtr(expected, actual) {
		return nil
	}
	text := transpiler.SourceText(exprCtx)
	hint := fmt.Sprintf("pass RequirePtr(%s); RequirePtr panics on nil, and a pointer that may be nil is kept as OptionPtrOf(%s)", text, text)
	if !transpiler.IsPtr(expected) {
		hint = "build the value from Ptr values: `&x`, or RequirePtr(p) for a pointer from Go"
	}
	return ptrError(exprCtx,
		fmt.Sprintf("cannot use %s (%s) as %s: a pointer that may be nil cannot fill a Ptr", text, displayType(actual), displayType(expected)),
		hint)
}

// ptrReceiverMethodNames returns the names of the methods of types declared
// on a `Ptr` receiver. Only a method of one of these names can need a
// non-null receiver, so the receiver checks skip every other one.
func ptrReceiverMethodNames(types map[string]*transpiler.TypeMetadata) map[string]bool {
	var names map[string]bool
	for _, meta := range types {
		for name, m := range meta.Methods {
			if m == nil || !m.PtrReceiver {
				continue
			}
			if names == nil {
				names = make(map[string]bool)
			}
			names[name] = true
		}
	}
	return names
}

// nullablePtrReceiver reports whether recvType is a pointer that may be nil
// whose type declares method on a `Ptr` receiver, and returns the pointer. A
// `T` value is addressed (`&x` is a Ptr) and a Ptr already is one.
func (t *galaASTTransformer) nullablePtrReceiver(recvType transpiler.Type, method string) (transpiler.PointerType, bool) {
	ptr, ok := recvType.(transpiler.PointerType)
	if !ok || ptr.NonNull || !t.ptrReceiverMethods[method] {
		return ptr, false
	}
	_, meta, _, ok := t.receiverTypeInfo(t.methodReceiverType(ptr.Elem, method))
	if !ok || meta == nil {
		return ptr, false
	}
	m := meta.Methods[method]
	return ptr, m != nil && m.PtrReceiver
}

// ptrOf displays the `Ptr` to ptr's element.
func ptrOf(ptr transpiler.PointerType) string {
	return displayType(transpiler.PointerType{Elem: ptr.Elem, NonNull: true})
}

// checkPtrReceiver rejects selecting a method declared on a `Ptr[T]`
// receiver — a call `p.M()` or a method value `p.M` — on a pointer that may be
// nil (GALA-E0080): the receiver fills a Ptr slot. recv is the lowered
// receiver and recvType its type; suffix is the member access.
func (t *galaASTTransformer) checkPtrReceiver(recv ast.Expr, recvType transpiler.Type, method string, suffix *grammar.PostfixSuffixContext) error {
	ptr, ok := t.nullablePtrReceiver(recvType, method)
	if !ok {
		return nil
	}
	from, to, text := receiverSource(suffix)
	hint := fmt.Sprintf("call it on RequirePtr(%s), which panics on nil; a pointer that may be nil is kept as OptionPtrOf(%s)", text, text)
	if id, isIdent := recv.(*ast.Ident); isIdent && t.fixedBindingOf(id.Name) == fixedPointerReceiver {
		hint = fmt.Sprintf("declare the receiver `%s %s`, or call it on RequirePtr(%s)", id.Name, ptrOf(ptr), text)
	}
	return spanError(galaerr.CodePtrNullability, from, to,
		fmt.Sprintf("cannot call %s on %s (%s): %s has a %s receiver, and a pointer that may be nil cannot fill a Ptr", method, text, displayType(ptr), method, ptrOf(ptr)),
		hint)
}

// checkPtrViewReceiver rejects a match through the sealed view of a pointer
// that may be nil when its MatchView has a `Ptr` receiver (GALA-E0080), as
// checkPtrReceiver rejects calling it. at locates the match.
func (t *galaASTTransformer) checkPtrViewReceiver(subjectType transpiler.Type, at antlr.ParserRuleContext) error {
	ptr, ok := t.nullablePtrReceiver(subjectType, sealedViewMethod)
	if !ok {
		return nil
	}
	return ptrError(at,
		fmt.Sprintf("cannot match a %s through %s: %s has a %s receiver, and a pointer that may be nil cannot fill a Ptr",
			displayType(ptr), sealedViewMethod, sealedViewMethod, ptrOf(ptr)),
		"match RequirePtr(p), which panics on nil, or an OptionPtrOf(p)")
}

// checkPtrInterfaceSlot rejects a pointer that may be nil filling an
// interface slot (GALA-E0080) when its type implements a method of the
// interface on a `Ptr` receiver: a call through the interface would run that
// method on whatever pointer the interface holds.
func (t *galaASTTransformer) checkPtrInterfaceSlot(expr ast.Expr, expected transpiler.Type, exprCtx antlr.ParserRuleContext) error {
	if len(t.ptrReceiverMethods) == 0 || isNilIdent(expr) {
		return nil
	}
	methods, _ := t.interfaceMethodNames(expected)
	methods = slices.DeleteFunc(methods, func(m string) bool { return !t.ptrReceiverMethods[m] })
	if len(methods) == 0 {
		return nil
	}
	actual := t.probeExprType(expr)
	slices.Sort(methods)
	for _, m := range methods {
		ptr, ok := t.nullablePtrReceiver(actual, m)
		if !ok {
			continue
		}
		text := transpiler.SourceText(exprCtx)
		return ptrError(exprCtx,
			fmt.Sprintf("cannot use %s (%s) as %s: its method %s has a %s receiver, and a pointer that may be nil cannot fill a Ptr",
				text, displayType(ptr), displayType(expected), m, ptrOf(ptr)),
			fmt.Sprintf("pass RequirePtr(%s); RequirePtr panics on nil", text))
	}
	return nil
}

// hasPtr reports whether typ has a `Ptr` anywhere in it.
func hasPtr(typ transpiler.Type) bool {
	switch v := typ.(type) {
	case transpiler.PointerType:
		return v.NonNull || hasPtr(v.Elem)
	case transpiler.ArrayType:
		return hasPtr(v.Elem)
	case transpiler.MapType:
		return hasPtr(v.Key) || hasPtr(v.Elem)
	case transpiler.GenericType:
		for _, p := range v.Params {
			if hasPtr(p) {
				return true
			}
		}
	case transpiler.FuncType:
		for _, r := range v.Results {
			if hasPtr(r) {
				return true
			}
		}
	}
	return false
}

// nullableWherePtr reports whether actual has a nullable pointer at a
// position where expected has a `Ptr`. Function results are compared; a
// function parameter is not, since a function taking a Ptr may take any
// pointer the caller checked.
func nullableWherePtr(expected, actual transpiler.Type) bool {
	if transpiler.IsUnusable(expected) || transpiler.IsUnusable(actual) {
		return false
	}
	switch e := expected.(type) {
	case transpiler.PointerType:
		a, ok := actual.(transpiler.PointerType)
		if !ok {
			return false
		}
		return (e.NonNull && !a.NonNull) || nullableWherePtr(e.Elem, a.Elem)
	case transpiler.ArrayType:
		a, ok := actual.(transpiler.ArrayType)
		return ok && nullableWherePtr(e.Elem, a.Elem)
	case transpiler.MapType:
		a, ok := actual.(transpiler.MapType)
		return ok && (nullableWherePtr(e.Key, a.Key) || nullableWherePtr(e.Elem, a.Elem))
	case transpiler.GenericType:
		a, ok := actual.(transpiler.GenericType)
		if !ok || len(a.Params) != len(e.Params) || stripPackagePrefix(a.Base.BaseName()) != stripPackagePrefix(e.Base.BaseName()) {
			return false
		}
		for i := range e.Params {
			if nullableWherePtr(e.Params[i], a.Params[i]) {
				return true
			}
		}
	case transpiler.FuncType:
		a, ok := actual.(transpiler.FuncType)
		if !ok || len(a.Results) != len(e.Results) {
			return false
		}
		for i := range e.Results {
			if nullableWherePtr(e.Results[i], a.Results[i]) {
				return true
			}
		}
	}
	return false
}

// checkPtrNilComparison rejects `p == nil` and `p != nil` on a `Ptr`
// (GALA-E0080): a Ptr is never nil, so the comparison asks nothing.
func (t *galaASTTransformer) checkPtrNilComparison(tok token.Token, ctx antlr.ParserRuleContext, left, right ast.Expr) error {
	if tok != token.EQL && tok != token.NEQ {
		return nil
	}
	operand := left
	switch {
	case isNilIdent(right):
	case isNilIdent(left):
		operand = right
	default:
		return nil
	}
	typ := t.probeExprType(operand)
	if !transpiler.IsPtr(typ) {
		return nil
	}
	return ptrError(ctx,
		fmt.Sprintf("%s compares a %s with nil, but a Ptr is never nil", transpiler.SourceText(ctx), displayType(typ)),
		"drop the comparison; "+ptrMayBeAbsentHint)
}

// ptrEntryChecks returns, for each parameter of params that is a `Ptr` and
// whose position guarded selects, the statement `if p == nil { panic(...) }`.
// It keeps a Ptr parameter non-null when Go is the caller: of an exported
// function or method, which Go code can call, and of a lambda filling a
// function slot whose parameter is a nullable `*T`, which its caller fills
// with whatever pointer it has. A GALA-to-GALA call needs no check.
func (t *galaASTTransformer) ptrEntryChecks(params *ast.FieldList, owner string, guarded func(pos int) bool) []ast.Stmt {
	if params == nil {
		return nil
	}
	var checks []ast.Stmt
	pos := 0
	for _, field := range params.List {
		if !t.isPtrTypeExpr(field.Type) {
			pos += max(len(field.Names), 1)
			continue
		}
		typ := displayType(t.astTypeToTranspilerType(field.Type))
		for _, n := range field.Names {
			if n.Name != "_" && guarded(pos) {
				checks = append(checks, ptrNilCheck(n.Name, fmt.Sprintf("%s: parameter %s is a %s, but the caller passed nil", owner, n.Name, typ)))
			}
			pos++
		}
	}
	return checks
}

// ptrReceiverEntryCheck returns, for a `Ptr` receiver recv of an exported
// method, the statement `if r == nil { panic(...) }`. GALA never calls such a
// method on a pointer that may be nil (checkPtrReceiver) nor turns one into an
// interface it implements (checkPtrSlot), so only Go code can call it with a
// nil receiver.
func (t *galaASTTransformer) ptrReceiverEntryCheck(recv *ast.Field, owner string) []ast.Stmt {
	if recv == nil || !t.isPtrTypeExpr(recv.Type) || len(recv.Names) != 1 || recv.Names[0].Name == "_" {
		return nil
	}
	name := recv.Names[0].Name
	typ := displayType(t.astTypeToTranspilerType(recv.Type))
	return []ast.Stmt{ptrNilCheck(name, fmt.Sprintf("%s: receiver %s is a %s, but the caller passed nil", owner, name, typ))}
}

// ptrNilCheck is `if name == nil { panic(msg) }`.
func ptrNilCheck(name, msg string) ast.Stmt {
	return &ast.IfStmt{
		Cond: &ast.BinaryExpr{X: ast.NewIdent(name), Op: token.EQL, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{List: panicStmt(msg)},
	}
}

// withPtrEntryChecks prepends checks to body.
func withPtrEntryChecks(body *ast.BlockStmt, checks []ast.Stmt) *ast.BlockStmt {
	if body == nil || len(checks) == 0 {
		return body
	}
	return &ast.BlockStmt{Lbrace: body.Lbrace, List: append(checks, body.List...), Rbrace: body.Rbrace}
}
