package transformer

import (
	"go/ast"
	"go/token"

	"martianoff/gala/internal/transpiler"
)

// loweredMethodValue lowers the method value `base.method` (not called) of a
// method the generated Go declares as a function, one the analyzer recorded
// as lowered (MethodMetadata.IsGeneric) without type parameters of its own:
// `codec.Decode` passed to FlatMap. Go has no such method, so the value is a
// closure over the receiver, evaluated once where the method value is, as a
// Go method value's receiver is:
//
//	func() func(s string) Try[T] { r := base; return func(s string) Try[T] { return Codec_Decode(r, s) } }()
//
// It reports !ok for any other member, which lowers as it always has.
func (t *galaASTTransformer) loweredMethodValue(base ast.Expr, xType transpiler.Type, method string) (ast.Expr, bool) {
	if xType.IsNil() {
		return nil, false
	}
	recvType, lookupBaseName := t.resolveReceiverTypeAndLookupKey(base, method)
	if !t.isGenericMethodWithImports(lookupBaseName, recvType.GetPackage(), method) {
		return nil, false
	}
	plain := recvType
	if ptr, isPtr := plain.(transpiler.PointerType); isPtr {
		plain = ptr.Elem
	}
	meta := t.getTypeMeta(plain.BaseName())
	if meta == nil {
		return nil, false
	}
	mm, ok := meta.Methods[method]
	if !ok || len(mm.TypeParams) > 0 || mm.GoResults != nil {
		return nil, false
	}
	var sig transpiler.Type = signatureType(mm.ParamTypes, mm.ReturnType, nil)
	if len(meta.TypeParams) > 0 {
		gen, isGeneric := plain.(transpiler.GenericType)
		if !isGeneric || len(gen.Params) != len(meta.TypeParams) {
			return nil, false
		}
		sig = t.substituteConcreteTypes(sig, meta.TypeParams, gen.Params)
	}
	ft, ok := sig.(transpiler.FuncType)
	if !ok {
		return nil, false
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
		results = &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(ft.Results[0])}}}
	}
	call := t.emitGenericMethodFreeFunc(method, ast.NewIdent(recvName), recvType, lookupBaseName, nil, mm, args, mm.Variadic)
	var body ast.Stmt = &ast.ExprStmt{X: call}
	if results != nil {
		body = &ast.ReturnStmt{Results: []ast.Expr{call}}
	}
	funcType := &ast.FuncType{Params: params, Results: results}
	closure := &ast.FuncLit{Type: funcType, Body: &ast.BlockStmt{List: []ast.Stmt{body}}}
	outerType := &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: funcType}}}}
	return &ast.CallExpr{Fun: &ast.FuncLit{Type: outerType, Body: &ast.BlockStmt{List: []ast.Stmt{
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(recvName)}, Tok: token.DEFINE, Rhs: []ast.Expr{base}},
		&ast.ReturnStmt{Results: []ast.Expr{closure}},
	}}}}, true
}
