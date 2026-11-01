package transformer

import (
	"fmt"
	"go/ast"
	"slices"
	"strings"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
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
//	func _unapply_std_Right_1a2b3c4d[_T0 any, _T1 any](v std.Either[_T0, _T1]) std.Option[_T1] {
//		return std.Right[_T0, _T1]{}.Unapply(v)
//	}
//
// and reads `_unapply_std_Right_1a2b3c4d(obj)`, which names none of them.
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
		Fun:  &ast.SelectorExpr{X: &ast.CompositeLit{Type: withTypeArgs(t.ident(extractorName), typeArgs)}, Sel: ast.NewIdent("Unapply")},
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
	if !typeMentionsAllTypeParams(param, meta.TypeParams) ||
		t.substituteConcreteTypes(param, meta.TypeParams, inferredTypes).String() != matchedType.String() {
		return "", false
	}
	for _, tp := range meta.TypeParams {
		if c := meta.TypeParamConstraints[tp]; c != "" && c != "any" && c != "comparable" {
			return "", false
		}
	}

	// The file names the extractor (extractorName, as the pattern wrote it),
	// so no two extractors it matches share that name.
	if name, ok := t.unapplyHelpers[extractorName]; ok {
		return name, true
	}
	placeholders := make([]transpiler.Type, len(meta.TypeParams))
	for i := range meta.TypeParams {
		placeholders[i] = transpiler.BasicType{Name: fmt.Sprintf("_T%d", i)}
	}
	param = t.substituteConcreteTypes(param, meta.TypeParams, placeholders)
	result := t.substituteConcreteTypes(unapplyMeta.ReturnType, meta.TypeParams, placeholders)
	// The helper's signature writes every type in it but the type
	// parameters: a case's field types are in its result. One naming a
	// package this file does not import would bring the import back.
	if !t.namesOnlyImportedPackages(param) || !t.namesOnlyImportedPackages(result) {
		return "", false
	}

	name := "_unapply_" + strings.ReplaceAll(extractorName, ".", "_") + t.codecFileSuffix()
	typeParams := &ast.FieldList{}
	typeArgs := make([]ast.Expr, len(meta.TypeParams))
	for i, tp := range meta.TypeParams {
		p := placeholders[i].String()
		constraint := meta.TypeParamConstraints[tp]
		if constraint == "" {
			constraint = "any"
		}
		typeParams.List = append(typeParams.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(p)}, Type: ast.NewIdent(constraint)})
		typeArgs[i] = ast.NewIdent(p)
	}
	t.unapplyHelperDecls = append(t.unapplyHelperDecls, &ast.FuncDecl{
		Name: ast.NewIdent(name),
		Type: &ast.FuncType{
			TypeParams: typeParams,
			Params:     &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("v")}, Type: t.typeToExpr(param)}}},
			Results:    &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(result)}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: &ast.CompositeLit{Type: withTypeArgs(t.ident(extractorName), typeArgs)}, Sel: ast.NewIdent("Unapply")},
			Args: []ast.Expr{ast.NewIdent("v")},
		}}}}},
	})
	t.unapplyHelpers[extractorName] = name
	return name, true
}

// namesOnlyImportedPackages reports whether every package-qualified type in
// typ is of a package this file can name without a new import: its own, std,
// or one this file (or, for a GALA package, a sibling file) imports.
func (t *galaASTTransformer) namesOnlyImportedPackages(typ transpiler.Type) bool {
	switch v := typ.(type) {
	case transpiler.NamedType:
		if v.Package == "" || v.Package == t.packageName || v.Package == registry.StdPackageName || t.importManager.IsDotImported(v.Package) {
			return true
		}
		// An implicit entry counts only when a sibling imports its package.
		entry, ok := t.importManager.Qualifier(v.Package)
		return ok && (!entry.Implicit() || t.richAST != nil && t.richAST.PackageImportPaths[entry.Path])
	case transpiler.GenericType:
		if !t.namesOnlyImportedPackages(v.Base) {
			return false
		}
		for _, p := range v.Params {
			if !t.namesOnlyImportedPackages(p) {
				return false
			}
		}
	case transpiler.ArrayType:
		return t.namesOnlyImportedPackages(v.Elem)
	case transpiler.ChanType:
		return t.namesOnlyImportedPackages(v.Elem)
	case transpiler.StructType:
		for _, f := range v.FieldTypes() {
			if !t.namesOnlyImportedPackages(f) {
				return false
			}
		}
	case transpiler.PointerType:
		return t.namesOnlyImportedPackages(v.Elem)
	case transpiler.MapType:
		return t.namesOnlyImportedPackages(v.Key) && t.namesOnlyImportedPackages(v.Elem)
	case transpiler.FuncType:
		for _, p := range append(slices.Clone(v.Params), v.Results...) {
			if !t.namesOnlyImportedPackages(p) {
				return false
			}
		}
	}
	return true
}
