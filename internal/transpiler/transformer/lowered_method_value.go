package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// loweredMethodValue lowers the method value `base.method` (not called) of a
// method the generated Go declares as a function: one the analyzer recorded
// as lowered (MethodMetadata.IsGeneric), or one with type parameters of its
// own (`b.Pair` for `Pair[U any](u U) Tuple[T, U]`, `xs.GroupBy`). Go has no
// such method, so the value is a closure over the receiver, evaluated once
// where the method value is, as a Go method value's receiver is:
//
//	func() func(s string) Try[T] { r := base; return func(s string) Try[T] { return Codec_Decode(r, s) } }()
//
// The method's own type parameters take typeArgs, the type arguments written
// (`b.Pair[string]`), and then what slot, the function type of the slot the
// value fills, gives them (`func(string) U` gives `string`). One neither
// determines is GALA-E0067, at at.
//
// It reports !ok for any other member, which lowers as it always has.
func (t *galaASTTransformer) loweredMethodValue(base ast.Expr, xType transpiler.Type, method string, typeArgs []ast.Expr, slot transpiler.Type, at srcNode) (ast.Expr, bool, error) {
	if xType.IsNil() {
		return nil, false, nil
	}
	// Most members named without a call are fields: settle them from the
	// receiver type's metadata before resolving the call-site lookup key.
	plain := t.methodReceiverType(xType, method)
	if ptr, isPtr := plain.(transpiler.PointerType); isPtr {
		plain = ptr.Elem
	}
	meta := t.getTypeMeta(plain.BaseName())
	if meta == nil {
		return nil, false, nil
	}
	mm, ok := meta.Methods[method]
	if !ok || !mm.IsGeneric && len(mm.TypeParams) == 0 || mm.GoResults != nil {
		return nil, false, nil
	}
	line, col := at.start()
	if len(typeArgs) > len(mm.TypeParams) {
		return nil, false, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
			"too many type arguments for the method value %s.%s: it has %d type parameter(s) of its own, not %d",
			stripPackagePrefix(meta.Name), method, len(mm.TypeParams), len(typeArgs)))
	}
	recvType, lookupBaseName := t.resolveReceiverTypeAndLookupKey(base, method)
	var sig transpiler.Type = signatureType(mm.ParamTypes, mm.InoutParams, mm.ReturnType, nil)
	// The method's own type parameters, renamed apart from the receiver's
	// type arguments first: in `func f[U any](b Box[U])`, `b.Pair`'s U is
	// not f's. Their prefix is their own, apart from the placeholders
	// inference leaves for other methods' (freshMethodTypeParamName), and
	// like those a placeholder mentionsUnboundTypeParam catches.
	ownParams := make([]string, len(mm.TypeParams))
	if len(mm.TypeParams) > 0 {
		rename := make(typeSubstMap, len(mm.TypeParams))
		for i, tp := range mm.TypeParams {
			ownParams[i] = methodValueTypeParamPrefix + itoaInt(i)
			rename[tp] = transpiler.NamedType{Name: ownParams[i]}
		}
		sig = t.substituteInType(sig, rename)
	}
	if len(meta.TypeParams) > 0 {
		gen, isGeneric := plain.(transpiler.GenericType)
		if !isGeneric || len(gen.Params) != len(meta.TypeParams) {
			return nil, false, nil
		}
		sig = t.substituteConcreteTypes(sig, meta.TypeParams, gen.Params)
	}
	ft, ok := sig.(transpiler.FuncType)
	if !ok {
		return nil, false, nil
	}

	// A by-name slot (`func() F`) the value is no thunk of is the thunk's
	// value: the slot the value fills is its result.
	if want := t.slotFuncType(slot); want != nil && len(want.Params) == 0 && len(ft.Params) > 0 {
		slot = nil
		if len(want.Results) == 1 {
			slot = want.Results[0]
		}
	}
	if slot != nil {
		if want := t.slotFuncType(slot); want != nil && len(want.Params) != len(ft.Params) {
			return nil, false, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"the method value %s.%s takes %d argument(s), but the function type it is used as takes %d",
				stripPackagePrefix(meta.Name), method, len(ft.Params), len(want.Params)))
		}
	}
	var methodTypeArgs []ast.Expr
	if len(mm.TypeParams) > 0 {
		bound := make(typeSubstMap, len(mm.TypeParams))
		for i, ta := range typeArgs {
			bound[ownParams[i]] = t.astTypeToTranspilerType(ta)
		}
		t.bindMethodValueTypeArgs(ft, slot, ownParams, bound)
		var missing []string
		byName := make(map[string]transpiler.Type, len(mm.TypeParams)) // under the method's own names
		for i, tp := range mm.TypeParams {
			if typ, isBound := bound[ownParams[i]]; isBound {
				methodTypeArgs = append(methodTypeArgs, t.typeToExpr(typ))
				byName[tp] = typ
			} else {
				missing = append(missing, tp)
			}
		}
		// A pointer that may be nil is no type argument of a type parameter
		// whose constraint needs a Ptr receiver, as at a call.
		if owner := methodTypeParamOwner(method, mm); len(missing) == 0 && t.hasPtrConstraint(owner) {
			src := typeArgSource{text: "the function type it is used as"}
			if err := t.checkPtrBoundTypeArgs(owner, len(typeArgs), byName, src, at); err != nil {
				return nil, false, err
			}
		}
		if len(missing) > 0 {
			name := stripPackagePrefix(meta.Name) + "." + method
			return nil, false, galaerr.NewCodedSemanticError(galaerr.CodeUninferredTypeArgument, line, col,
				fmt.Sprintf("cannot infer type argument %s of the method value %s: no type arguments are written, and no function type it is passed to or declared with fixes them", strings.Join(missing, ", "), name),
				fmt.Sprintf("write its type arguments (`%s[...]`), or use it where a function type is expected", method))
		}
		// Substituting into a function type gives a function type.
		ft = t.substituteTranspilerTypeParams(ft, bound).(transpiler.FuncType)
	}

	// The receiver as the function takes it: `&c` for a pointer receiver,
	// as a Go method value of an addressable variable binds.
	recv, err := t.loweredMethodReceiver(base, recvType, mm, method, line, col)
	if err != nil {
		return nil, false, err
	}
	recvName := t.freshTempName("__recv_", &t.tempVarCount)
	params := &ast.FieldList{}
	args := make([]ast.Expr, len(ft.Params))
	for i, p := range ft.Params {
		name := t.freshTempName("__arg_", &t.tempVarCount)
		var typ ast.Expr = t.typeToExpr(p)
		if mm.Variadic && i == len(ft.Params)-1 {
			typ = &ast.Ellipsis{Elt: typ}
		}
		params.List = append(params.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(name)}, Type: typ})
		args[i] = ast.NewIdent(name)
	}
	var results *ast.FieldList
	if len(ft.Results) > 0 {
		results = t.resultFieldList(ft.Results)
	}
	call := t.emitGenericMethodFreeFunc(method, ast.NewIdent(recvName), recvType, lookupBaseName, methodTypeArgs, mm, args, mm.Variadic)
	var body ast.Stmt = &ast.ExprStmt{X: call}
	if results != nil {
		body = &ast.ReturnStmt{Results: []ast.Expr{call}}
	}
	funcType := &ast.FuncType{Params: params, Results: results}
	closure := &ast.FuncLit{Type: funcType, Body: &ast.BlockStmt{List: []ast.Stmt{body}}}
	outerType := &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: funcType}}}}
	return &ast.CallExpr{Fun: &ast.FuncLit{Type: outerType, Body: &ast.BlockStmt{List: []ast.Stmt{
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(recvName)}, Tok: token.DEFINE, Rhs: []ast.Expr{recv}},
		&ast.ReturnStmt{Results: []ast.Expr{closure}},
	}}}}, true, nil
}

// bindMethodValueTypeArgs adds to bound what slot, the type of the slot a
// method value of signature ft fills, gives the method's own type parameters
// typeParams: from the slot's parameter types first, then its results. Only a
// type the code can name binds one: not `any` (a callee's type parameter not
// inferred yet), nor another callee's unbound type parameter (Map's `U` in
// the slot of `xs.Map(b.Pair)`, t.calleeTypeParams), even one an enclosing
// declaration has a type parameter of the same name for.
func (t *galaASTTransformer) bindMethodValueTypeArgs(ft transpiler.FuncType, slot transpiler.Type, typeParams []string, bound typeSubstMap) {
	if slot == nil || len(bound) == len(typeParams) {
		return
	}
	want := t.slotFuncType(slot)
	if want == nil || len(want.Params) != len(ft.Params) {
		return
	}
	bind := func(pattern, actual transpiler.Type) {
		inferred := make(map[string]transpiler.Type)
		t.unifyForInference(pattern, actual, typeParams, inferred)
		for tp, typ := range inferred {
			if _, isBound := bound[tp]; isBound || transpiler.IsUnusableOrAny(typ) || transpiler.ContainsUnusable(typ) ||
				t.mentionsUnboundTypeParam(typ) || typeMentionsTypeParam(typ, t.calleeTypeParams) {
				continue
			}
			bound[tp] = typ
		}
	}
	for i, p := range ft.Params {
		bind(p, want.Params[i])
	}
	if len(want.Results) == len(ft.Results) {
		for i, r := range ft.Results {
			bind(r, want.Results[i])
		}
	}
}

// slotFuncType is the function type slot, the type of the slot a method value
// fills, names through any alias (a Sendable one too); nil for no slot or one
// of no function type.
func (t *galaASTTransformer) slotFuncType(slot transpiler.Type) *transpiler.FuncType {
	if slot == nil {
		return nil
	}
	slot, _ = transpiler.UnwrapSendable(t.followAliasChain(slot))
	return t.resolveTranspilerTypeAsFuncType(slot)
}

// methodValueTypeParamPrefix starts the name a method value's own type
// parameter takes while loweredMethodValue binds it.
const methodValueTypeParamPrefix = freshMethodTypeParamPrefix + "v"

// calleeTypeParamPrefix starts the name a callee's own type parameter takes in
// the slot a method value fills in the generic-method pre-pass: one that, like
// freshMethodTypeParamName's, mentionsUnboundTypeParam counts as unbound,
// apart from those placeholders themselves.
const calleeTypeParamPrefix = freshMethodTypeParamPrefix + "c"

// instantiatedMethodValue lowers idx, `recv.m[A]` not called, to the method
// value of m with those type arguments when m is a method with type
// parameters of its own (see loweredMethodValue); slot is the type of the
// slot it fills, nil for none. Any other index is returned as it is.
func (t *galaASTTransformer) instantiatedMethodValue(idx ast.Expr, slot transpiler.Type, at srcNode) (ast.Expr, error) {
	x, indices := splitCallFunTypeArgs(idx)
	sel, ok := x.(*ast.SelectorExpr)
	if !ok || indices == nil || !t.typeParamMethods[sel.Sel.Name] || !t.namesGenericMethod(sel) {
		return idx, nil
	}
	value, ok, err := t.loweredMethodValue(sel.X, t.getExprTypeName(sel.X), sel.Sel.Name, indices, slot, at)
	if err != nil || !ok {
		return idx, err
	}
	return value, nil
}
