package transformer

import (
	"go/ast"
	"go/token"
	"go/types"
)

// The generated Copy and Equal of a struct copy and compare it field by
// field. std.Copy and std.Equal are generic, and Go compiles a generic
// function once per type argument shape, so calling them per field compiled
// them once for every field type of the program. A field whose type the
// transformer knows needs no such call:
//
//   - a predeclared primitive (`var n int`) is copied as it is and compared
//     with ==, which is what std.Copy and std.Equal do with one;
//   - an Immutable of one (`n int`) likewise, through Get for ==;
//   - any other Immutable calls its own generated Copy or Equal, which is
//     what std.Copy and std.Equal find through Copyable and Equatable.
//
// Every other field keeps the std call.

// directFieldPrimitives are the predeclared types std.Copy returns as they
// are and std.Equal compares with == (see equalPrimitive in std/types.go).
var directFieldPrimitives = map[string]bool{
	"bool": true, "string": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true,
	"byte": true, "rune": true,
}

// isDirectPrimitive reports whether typ spells a predeclared primitive type.
func isDirectPrimitive(typ ast.Expr) bool {
	id, ok := typ.(*ast.Ident)
	return ok && directFieldPrimitives[id.Name]
}

// immutableElem returns T for a field type std's Immutable[T], spelled as
// the transformer spells std types.
func (t *galaASTTransformer) immutableElem(typ ast.Expr) (ast.Expr, bool) {
	idx, ok := typ.(*ast.IndexExpr)
	if !ok || types.ExprString(idx.X) != types.ExprString(t.stdHelperIdent("Immutable")) {
		return nil, false
	}
	return idx.Index, true
}

// fieldCopyExpr is the copy of field, of Go type typ, in a generated Copy.
func (t *galaASTTransformer) fieldCopyExpr(typ, field ast.Expr) ast.Expr {
	if isDirectPrimitive(typ) {
		return field
	}
	if elem, ok := t.immutableElem(typ); ok {
		if isDirectPrimitive(elem) {
			return field
		}
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: field, Sel: ast.NewIdent("Copy")}}
	}
	return &ast.CallExpr{Fun: t.stdIdent("Copy"), Args: []ast.Expr{field}}
}

// fieldEqualExpr compares field a with field b, of Go type typ, in a
// generated Equal.
func (t *galaASTTransformer) fieldEqualExpr(typ, a, b ast.Expr) ast.Expr {
	if isDirectPrimitive(typ) {
		return &ast.BinaryExpr{X: a, Op: token.EQL, Y: b}
	}
	if elem, ok := t.immutableElem(typ); ok {
		if isDirectPrimitive(elem) {
			get := func(x ast.Expr) ast.Expr {
				return &ast.CallExpr{Fun: &ast.SelectorExpr{X: x, Sel: ast.NewIdent("Get")}}
			}
			return &ast.BinaryExpr{X: get(a), Op: token.EQL, Y: get(b)}
		}
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: a, Sel: ast.NewIdent("Equal")}, Args: []ast.Expr{b}}
	}
	return &ast.CallExpr{Fun: t.stdIdent("Equal"), Args: []ast.Expr{a, b}}
}
