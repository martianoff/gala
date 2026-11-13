package transformer

import (
	"go/ast"
	"go/token"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// The generated Copy and Equal of a struct copy and compare it field by
// field. std.Copy and std.Equal are generic, and Go compiles a generic
// function once per type argument shape, so calling them per field compiled
// them once more for every Immutable field type of the program. A field whose
// type the transformer knows needs no such call:
//
//   - a predeclared primitive (`var n int`) is copied as it is and compared
//     with ==, which is what std.Copy and std.Equal do with one;
//   - an Immutable of one (`n int`) likewise, through Get for ==;
//   - any other Immutable calls its own generated Copy or Equal, which is
//     what std.Copy and std.Equal find through Copyable and Equatable.
//
// Every other field keeps the std call.

// fieldTypeCtx tells the field rules what the generated method's type
// parameters are, so that a type parameter spelled like a predeclared type
// (`struct Buf[byte any](...)`) is not taken for one.
type fieldTypeCtx struct {
	typeParams map[string]bool
}

// newFieldTypeCtx is the context of a method of a type with type parameters
// tParams (nil for none).
func newFieldTypeCtx(tParams *ast.FieldList) fieldTypeCtx {
	ctx := fieldTypeCtx{typeParams: map[string]bool{}}
	for _, f := range fieldListOrNil(tParams) {
		for _, n := range f.Names {
			ctx.typeParams[n.Name] = true
		}
	}
	return ctx
}

// isDirectPrimitive reports whether typ spells a predeclared primitive type
// that std.Copy returns as it is and std.Equal compares with ==: any and
// error hold values of other types.
func (c fieldTypeCtx) isDirectPrimitive(typ ast.Expr) bool {
	id, ok := typ.(*ast.Ident)
	return ok && !c.typeParams[id.Name] && id.Name != "any" && id.Name != "error" && transpiler.IsPrimitiveType(id.Name)
}

// immutableElem returns T for a field type std's Immutable[T], spelled as
// the transformer spells std types (stdHelperIdent): `std.Immutable`, or
// `Immutable` where std is in scope.
func (t *galaASTTransformer) immutableElem(typ ast.Expr) (ast.Expr, bool) {
	idx, ok := typ.(*ast.IndexExpr)
	if !ok {
		return nil, false
	}
	switch x := idx.X.(type) {
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		return idx.Index, ok && x.Sel.Name == "Immutable" && pkg.Name == registry.StdPackageName && !t.stdIsInScope()
	case *ast.Ident:
		return idx.Index, x.Name == "Immutable" && t.stdIsInScope()
	}
	return nil, false
}

// fieldCopyExpr is the copy of field, of Go type typ, in a generated Copy.
func (t *galaASTTransformer) fieldCopyExpr(c fieldTypeCtx, typ, field ast.Expr) ast.Expr {
	if c.isDirectPrimitive(typ) {
		return field
	}
	if elem, ok := t.immutableElem(typ); ok {
		if c.isDirectPrimitive(elem) {
			return field
		}
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: field, Sel: ast.NewIdent("Copy")}}
	}
	return &ast.CallExpr{Fun: t.stdIdent("Copy"), Args: []ast.Expr{field}}
}

// fieldEqualExpr compares field a with field b, of Go type typ, in a
// generated Equal.
func (t *galaASTTransformer) fieldEqualExpr(c fieldTypeCtx, typ, a, b ast.Expr) ast.Expr {
	if c.isDirectPrimitive(typ) {
		return &ast.BinaryExpr{X: a, Op: token.EQL, Y: b}
	}
	if elem, ok := t.immutableElem(typ); ok {
		if c.isDirectPrimitive(elem) {
			get := func(x ast.Expr) ast.Expr {
				return &ast.CallExpr{Fun: &ast.SelectorExpr{X: x, Sel: ast.NewIdent("Get")}}
			}
			return &ast.BinaryExpr{X: get(a), Op: token.EQL, Y: get(b)}
		}
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: a, Sel: ast.NewIdent("Equal")}, Args: []ast.Expr{b}}
	}
	return &ast.CallExpr{Fun: t.stdIdent("Equal"), Args: []ast.Expr{a, b}}
}
