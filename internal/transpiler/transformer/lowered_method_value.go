package transformer

import (
	"fmt"
	"go/ast"
	"go/token"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// A method GALA lowers to a function (MethodMetadata.IsGeneric: one that
// needs a receiver type parameter comparable, or would otherwise start a Go
// instantiation cycle) is no method in Go, so `m.ToGoMap` named without a
// call has no Go method value to stand for. It becomes a function value that
// calls the lowered function on the receiver, evaluated once where the value
// is taken, as Go evaluates a method value's receiver:
//
//	func() func() map[K]V { r := m; return func() map[K]V { return HashMap_ToGoMap[K, V](r) } }()
//
// A method with type parameters of its own has no single signature to take a
// value of, and one with a Go result list is a Go call; both are left as
// they are.

// loweredMethodValue returns sel, the method value `x.method` the suffix
// names, as such a function value when the method is lowered, or sel itself.
func (t *galaASTTransformer) loweredMethodValue(sel *ast.SelectorExpr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	method := sel.Sel.Name
	recvType, lookupBaseName := t.resolveReceiverTypeAndLookupKey(sel.X, method)
	typeMeta, resolved := t.getTypeMetaResolved(lookupBaseName)
	if typeMeta == nil {
		return sel, nil
	}
	mm := typeMeta.Methods[method]
	if mm == nil || !mm.IsGeneric || len(mm.TypeParams) > 0 || mm.GoResults != nil {
		return sel, nil
	}
	if err := t.checkComparableTypeArgs(method, recvType, lookupBaseName, suffix); err != nil {
		return nil, err
	}
	recvArgs := t.getReceiverTypeArgTypes(recvType)
	if len(recvArgs) != len(typeMeta.TypeParams) {
		return sel, nil
	}
	concrete := func(typ transpiler.Type) transpiler.Type {
		return t.substituteConcreteTypes(typ, typeMeta.TypeParams, recvArgs)
	}

	recv := ast.NewIdent(t.nextTempVar())
	params := &ast.FieldList{}
	var args []ast.Expr
	for i, pt := range mm.ParamTypes {
		name := ast.NewIdent(fmt.Sprintf("%s%d", recv.Name, i))
		typeExpr := t.typeToExpr(concrete(pt))
		if mm.Variadic && i == len(mm.ParamTypes)-1 {
			typeExpr = &ast.Ellipsis{Elt: typeExpr}
		}
		params.List = append(params.List, &ast.Field{Names: []*ast.Ident{name}, Type: typeExpr})
		args = append(args, name)
	}
	fnType := &ast.FuncType{Params: params}
	call := t.emitGenericMethodFreeFunc(method, recv, recvType, resolved, nil, mm, args, mm.Variadic)
	body := &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: call}}}
	if mm.ReturnType != nil && !mm.ReturnType.IsVoid() {
		fnType.Results = &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(concrete(mm.ReturnType))}}}
		body = &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{call}}}}
	}
	inner := &ast.FuncLit{Type: fnType, Body: body}
	outer := &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: fnType}}}},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.AssignStmt{Lhs: []ast.Expr{recv}, Tok: token.DEFINE, Rhs: []ast.Expr{sel.X}},
			&ast.ReturnStmt{Results: []ast.Expr{inner}},
		}},
	}
	return &ast.CallExpr{Fun: outer}, nil
}
