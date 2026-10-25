package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"hash/fnv"
	"path/filepath"
	"strings"

	"martianoff/gala/internal/transpiler"
)

// extractorUnapplyCall builds the call that runs extractor's Unapply on obj.
// A non-generic extractor is called as `Ext{}.Unapply(obj)`.
//
// A generic one, `Some[T]` or a case of a generic sealed type, needs its type
// arguments, and a composite literal cannot infer them: `Right[L, R]{}` names
// L and R, which may be types of a package the file never imports (the
// generated Go must import only what the GALA file does). When Go can infer
// them from obj, the call goes through a generic helper declared once per file
// instead,
//
//	func _unapply_std_Right[_T0 any, _T1 any](v std.Either[_T0, _T1]) std.Option[_T1] {
//		return std.Right[_T0, _T1]{}.Unapply(v)
//	}
//
// and reads `_unapply_std_Right(obj)`, which names none of them.
func (t *galaASTTransformer) extractorUnapplyCall(
	extractorName string,
	meta *transpiler.TypeMetadata,
	inferredTypes []transpiler.Type,
	unapplyMeta *transpiler.MethodMetadata,
	objExpr ast.Expr,
	matchedType transpiler.Type,
) ast.Expr {
	if helper, ok := t.unapplyHelper(extractorName, meta, inferredTypes, unapplyMeta, matchedType); ok {
		return &ast.CallExpr{Fun: ast.NewIdent(helper), Args: []ast.Expr{objExpr}}
	}
	typeArgs := make([]ast.Expr, len(inferredTypes))
	for i, tp := range inferredTypes {
		typeArgs[i] = t.typeToExpr(tp)
	}
	return &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: &ast.CompositeLit{Type: indexType(t.ident(extractorName), typeArgs)}, Sel: ast.NewIdent("Unapply")},
		Args: []ast.Expr{objExpr},
	}
}

// unapplyHelper returns the name of the generic helper that calls the generic
// extractor's Unapply with its type arguments inferred from the argument (see
// extractorUnapplyCall), declaring it on first use. ok is false when Go could
// not infer them: a type parameter missing from Unapply's parameter, a
// constraint other than any or comparable, or a matched value whose type is
// not exactly that parameter's.
func (t *galaASTTransformer) unapplyHelper(
	extractorName string,
	meta *transpiler.TypeMetadata,
	inferredTypes []transpiler.Type,
	unapplyMeta *transpiler.MethodMetadata,
	matchedType transpiler.Type,
) (string, bool) {
	if len(inferredTypes) == 0 || len(inferredTypes) != len(meta.TypeParams) || len(unapplyMeta.ParamTypes) != 1 ||
		matchedType == nil || transpiler.IsUnusable(matchedType) || transpiler.IsUnusable(unapplyMeta.ReturnType) {
		return "", false
	}
	param := unapplyMeta.ParamTypes[0]
	if t.substituteConcreteTypes(param, meta.TypeParams, inferredTypes).String() != matchedType.String() {
		return "", false
	}
	placeholders := make([]transpiler.Type, len(meta.TypeParams))
	for i := range meta.TypeParams {
		placeholders[i] = transpiler.BasicType{Name: fmt.Sprintf("_T%d", i)}
	}
	param = t.substituteConcreteTypes(param, meta.TypeParams, placeholders)
	for i, tp := range meta.TypeParams {
		if c := meta.TypeParamConstraints[tp]; c != "" && c != "any" && c != "comparable" {
			return "", false
		}
		if !mentionsTypeName(param, placeholders[i].String()) {
			return "", false
		}
	}

	callee := t.ident(extractorName)
	name := "_unapply_" + strings.Map(func(r rune) rune {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, meta.Package+"_"+meta.Name) + t.fileDeclSuffix()
	if t.unapplyHelpers[name] {
		return name, true
	}

	typeParams := &ast.FieldList{}
	typeArgs := make([]ast.Expr, len(placeholders))
	for i, p := range placeholders {
		constraint := meta.TypeParamConstraints[meta.TypeParams[i]]
		if constraint == "" {
			constraint = "any"
		}
		typeParams.List = append(typeParams.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(p.String())}, Type: ast.NewIdent(constraint)})
		typeArgs[i] = ast.NewIdent(p.String())
	}
	result := t.substituteConcreteTypes(unapplyMeta.ReturnType, meta.TypeParams, placeholders)
	decl := &ast.FuncDecl{
		Name: ast.NewIdent(name),
		Type: &ast.FuncType{
			TypeParams: typeParams,
			Params:     &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("v")}, Type: t.typeToExpr(param)}}},
			Results:    &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(result)}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: &ast.CompositeLit{Type: indexType(callee, typeArgs)}, Sel: ast.NewIdent("Unapply")},
			Args: []ast.Expr{ast.NewIdent("v")},
		}}}}},
	}
	if t.unapplyHelpers == nil {
		t.unapplyHelpers = make(map[string]bool)
	}
	t.unapplyHelpers[name] = true
	t.unapplyHelperDecls = append(t.unapplyHelperDecls, decl)
	return name, true
}

// fileDeclSuffix tells apart the package-level helpers that each file of a
// package declares for itself: `_` and a hash of the file's name, which is
// unique in its package (its path is not stable across machines), or nothing
// when the file has no path.
func (t *galaASTTransformer) fileDeclSuffix() string {
	if t.filePath == "" {
		return ""
	}
	h := fnv.New32a()
	h.Write([]byte(filepath.Base(filepath.ToSlash(t.filePath))))
	return fmt.Sprintf("_%08x", h.Sum32())
}

// indexType instantiates the generic type x with args: x[a] or x[a, b, ...];
// x itself when there are none.
func indexType(x ast.Expr, args []ast.Expr) ast.Expr {
	switch len(args) {
	case 0:
		return x
	case 1:
		return &ast.IndexExpr{X: x, Index: args[0]}
	default:
		return &ast.IndexListExpr{X: x, Lbrack: token.NoPos, Indices: args}
	}
}

// mentionsTypeName reports whether typ names the type name anywhere.
func mentionsTypeName(typ transpiler.Type, name string) bool {
	switch v := typ.(type) {
	case transpiler.BasicType:
		return v.Name == name
	case transpiler.NamedType:
		return v.Package == "" && v.Name == name
	case transpiler.GenericType:
		if mentionsTypeName(v.Base, name) {
			return true
		}
		for _, p := range v.Params {
			if mentionsTypeName(p, name) {
				return true
			}
		}
	case transpiler.ArrayType:
		return mentionsTypeName(v.Elem, name)
	case transpiler.PointerType:
		return mentionsTypeName(v.Elem, name)
	case transpiler.MapType:
		return mentionsTypeName(v.Key, name) || mentionsTypeName(v.Elem, name)
	case transpiler.FuncType:
		for _, p := range v.Params {
			if mentionsTypeName(p, name) {
				return true
			}
		}
		for _, r := range v.Results {
			if mentionsTypeName(r, name) {
				return true
			}
		}
	}
	return false
}
