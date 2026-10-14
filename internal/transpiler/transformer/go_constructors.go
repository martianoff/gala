package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
	"unicode"

	"martianoff/gala/internal/transpiler"
)

// Go constructors. Go can build a GALA value without calling any GALA code (a
// composite literal, a zero value), which skips the field defaults GALA
// applies. So an exported sealed type gets constructors for Go callers, and a
// doc comment saying to use them:
//
//   - NewT() returns T's default value, when T has a default case;
//   - NewT<Case>(...) builds a case: a field without a default is a
//     parameter, a field with one an option (With<T><Case><Field>) applied
//     over its default.
//
// A constructor whose name the package already declares is not generated.

// sealedGoConstructors returns the Go constructors of the exported sealed type
// name (cases variants, type parameters tParams), with the Apply methods its
// cases were generated with in decls, and the paragraph its doc comment gains.
func (t *galaASTTransformer) sealedGoConstructors(name string, variants []sealedVariantInfo, tParams *ast.FieldList, decls []ast.Decl, line, col int) ([]ast.Decl, string, error) {
	if !ast.IsExported(name) {
		return nil, "", nil
	}
	parentType := t.buildGenericTypeExpr(name, tParams)
	var out []ast.Decl
	var defaultCase *sealedVariantInfo
	var caseCtors []string

	for i := range variants {
		vi := &variants[i]
		if vi.tagValue == 0 {
			defaultCase = vi
		}
		if !ast.IsExported(vi.name) {
			continue
		}
		ctorName := "New" + name + vi.name
		if !t.goNameFree(ctorName) {
			continue
		}
		apply := findCompanionApply(decls, vi.name)
		if apply == nil {
			continue
		}
		ctorDecls, err := t.sealedCaseConstructor(name, vi, ctorName, apply, parentType, tParams, line, col)
		if err != nil {
			return nil, "", err
		}
		out = append(out, ctorDecls...)
		caseCtors = append(caseCtors, ctorName)
	}

	var doc strings.Builder
	if defaultCase != nil && t.goNameFree("New"+name) {
		parent := transpiler.Type(transpiler.BasicType{Name: name})
		if tParams != nil {
			parent = transpiler.GenericType{Base: parent, Params: typeParamTypes(tParams)}
		}
		val, err := t.defaultValue(parent, line, col, map[string]bool{})
		if err != nil {
			return nil, "", err
		}
		if val == nil {
			val = &ast.CompositeLit{Type: parentType}
		}
		described := t.describeDefaultCase(*defaultCase)
		out = append(out, &ast.FuncDecl{
			Doc: docCommentGroup(fmt.Sprintf("New%s returns %s's default value: %s, what GALA uses where it needs a %s and none is given.",
				name, name, described, name)),
			Name: ast.NewIdent("New" + name),
			Type: &ast.FuncType{TypeParams: tParams, Params: &ast.FieldList{}, Results: &ast.FieldList{List: []*ast.Field{{Type: parentType}}}},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{val}}}},
		})
		fmt.Fprintf(&doc, "Create a %s with New%s, its default value (%s)", name, name, described)
		if len(caseCtors) > 0 {
			fmt.Fprintf(&doc, ", or with a case constructor: %s", strings.Join(caseCtors, ", "))
		}
		fmt.Fprintf(&doc, ". A composite literal does not apply GALA's field defaults: %s{} is %s with every field at Go's zero value.", name, defaultCase.name)
	} else if defaultCase == nil {
		fmt.Fprintf(&doc, "%s has no default value", name)
		if len(caseCtors) > 0 {
			fmt.Fprintf(&doc, ": create it with a case constructor, %s", strings.Join(caseCtors, ", "))
		}
		fmt.Fprintf(&doc, ". Its zero value %s{} is none of its cases.", name)
	}
	return out, doc.String(), nil
}

// sealedCaseConstructor is NewT<Case> for case vi of sealed type parent: the
// parameters of its companion's Apply, those with a declared default as
// options over it.
func (t *galaASTTransformer) sealedCaseConstructor(parent string, vi *sealedVariantInfo, ctorName string, apply *ast.FuncDecl, parentType ast.Expr, tParams *ast.FieldList, line, col int) ([]ast.Decl, error) {
	var applyMeta *transpiler.MethodMetadata
	if comp := t.getTypeMeta(vi.name); comp != nil {
		applyMeta = comp.Methods["Apply"]
	}
	companionType := t.buildGenericTypeExpr(vi.name, tParams)
	companion := transpiler.Type(transpiler.BasicType{Name: vi.name})
	if tParams != nil {
		companion = transpiler.GenericType{Base: companion, Params: typeParamTypes(tParams)}
	}

	type param struct {
		name string
		typ  ast.Expr
		def  *transpiler.DefaultExpr
		i    int
	}
	var required, optional []param
	i := 0
	for _, f := range apply.Type.Params.List {
		for _, n := range f.Names {
			p := param{name: n.Name, typ: f.Type, i: i}
			if applyMeta != nil {
				if d, ok := applyMeta.DefaultExprs[i]; ok {
					p.def = &d
				}
			}
			if p.def != nil {
				optional = append(optional, p)
			} else {
				required = append(required, p)
			}
			i++
		}
	}

	var decls []ast.Decl
	params := &ast.FieldList{}
	for _, p := range required {
		params.List = append(params.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(goParamName(p.name))}, Type: p.typ})
	}
	args := make([]ast.Expr, i)
	for _, p := range required {
		args[p.i] = ast.NewIdent(goParamName(p.name))
	}
	var body []ast.Stmt
	if len(optional) > 0 {
		optionName := parent + vi.name + "Option"
		argsName := lowerFirst(parent) + vi.name + "Args"
		argsType := t.buildGenericTypeExpr(argsName, tParams)
		optionType := t.buildGenericTypeExpr(optionName, tParams)
		argsFields := &ast.FieldList{}
		inits := []ast.Expr{}
		for _, p := range optional {
			argsFields.List = append(argsFields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(p.name)}, Type: p.typ})
			val, err := t.methodDefaultArg(applyMeta, p.i, &ast.CompositeLit{Type: companionType}, companion, nil, line, col)
			if err != nil {
				return nil, err
			}
			inits = append(inits, &ast.KeyValueExpr{Key: ast.NewIdent(p.name), Value: val})
			args[p.i] = &ast.SelectorExpr{X: ast.NewIdent("_args"), Sel: ast.NewIdent(p.name)}
		}
		decls = append(decls,
			&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{Name: ast.NewIdent(argsName), TypeParams: tParams, Type: &ast.StructType{Fields: argsFields}}}},
			&ast.GenDecl{
				Doc: docCommentGroup(fmt.Sprintf("%s sets a field of %s left out of %s; its fields' defaults apply otherwise.", optionName, vi.name, ctorName)),
				Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{Name: ast.NewIdent(optionName), TypeParams: tParams,
					Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Type: &ast.StarExpr{X: argsType}}}}}}},
			})
		for _, p := range optional {
			withName := "With" + parent + vi.name + p.name
			if !t.goNameFree(withName) {
				continue
			}
			decls = append(decls, &ast.FuncDecl{
				Doc:  docCommentGroup(fmt.Sprintf("%s sets %s's %s (default %s).", withName, vi.name, p.name, strings.TrimSpace(p.def.Text))),
				Name: ast.NewIdent(withName),
				Type: &ast.FuncType{TypeParams: tParams,
					Params:  &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("v")}, Type: p.typ}}},
					Results: &ast.FieldList{List: []*ast.Field{{Type: optionType}}}},
				Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.FuncLit{
					Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("_args")}, Type: &ast.StarExpr{X: argsType}}}}},
					Body: &ast.BlockStmt{List: []ast.Stmt{&ast.AssignStmt{
						Lhs: []ast.Expr{&ast.SelectorExpr{X: ast.NewIdent("_args"), Sel: ast.NewIdent(p.name)}},
						Tok: token.ASSIGN, Rhs: []ast.Expr{ast.NewIdent("v")},
					}}},
				}}}}},
			})
		}
		params.List = append(params.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent("_opts")}, Type: &ast.Ellipsis{Elt: optionType}})
		body = append(body,
			&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("_args")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CompositeLit{Type: argsType, Elts: inits}}},
			&ast.RangeStmt{Key: ast.NewIdent("_"), Value: ast.NewIdent("_opt"), Tok: token.DEFINE, X: ast.NewIdent("_opts"),
				Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_opt"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: ast.NewIdent("_args")}}}}}}},
		)
	}
	body = append(body, &ast.ReturnStmt{Results: []ast.Expr{applyCall(companionType, args)}})

	doc := fmt.Sprintf("%s returns the %s case of %s", ctorName, vi.name, parent)
	if len(optional) > 0 {
		var defs []string
		for _, p := range optional {
			defs = append(defs, fmt.Sprintf("%s %s", p.name, strings.TrimSpace(p.def.Text)))
		}
		doc += fmt.Sprintf(", each option applied over its field defaults (%s)", strings.Join(defs, ", "))
	}
	doc += "."
	if caseDoc := t.caseDoc(vi.name); caseDoc != "" {
		doc += "\n\n" + caseDoc
	}
	decls = append(decls, &ast.FuncDecl{
		Doc:  docCommentGroup(doc),
		Name: ast.NewIdent(ctorName),
		Type: &ast.FuncType{TypeParams: tParams, Params: params, Results: &ast.FieldList{List: []*ast.Field{{Type: parentType}}}},
		Body: &ast.BlockStmt{List: body},
	})
	return decls, nil
}

// describeDefaultCase renders default case vi as its doc reads it, e.g.
// `On with Power 30 and Room "Living"`, or `Off`.
func (t *galaASTTransformer) describeDefaultCase(vi sealedVariantInfo) string {
	if len(vi.fields) == 0 {
		return vi.name
	}
	var apply *transpiler.MethodMetadata
	if comp := t.getTypeMeta(vi.name); comp != nil {
		apply = comp.Methods["Apply"]
	}
	parts := make([]string, len(vi.fields))
	for i, f := range vi.fields {
		value := "its zero value"
		if apply != nil {
			if d, ok := apply.DefaultExprs[i]; ok {
				value = strings.TrimSpace(d.Text)
			}
		}
		parts[i] = f.name + " " + value
	}
	if len(parts) == 1 {
		return vi.name + " with " + parts[0]
	}
	return vi.name + " with " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// caseDoc returns the doc comment written on the case named name, if any.
func (t *galaASTTransformer) caseDoc(name string) string {
	if comp := t.getTypeMeta(name); comp != nil {
		return comp.Doc
	}
	return ""
}

// goNameFree reports whether this package declares nothing named name, so a
// generated constructor may take it.
func (t *galaASTTransformer) goNameFree(name string) bool {
	key := ownFunctionKey(t.packageName, name)
	return t.functions[key] == nil && t.typeMetas[key] == nil && t.richAST.PackageVals[name] == nil
}

// findCompanionApply returns the Apply method generated for the companion
// type named companion among decls.
func findCompanionApply(decls []ast.Decl, companion string) *ast.FuncDecl {
	for _, d := range decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Apply" || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		recv := fn.Recv.List[0].Type
		if idx, ok := recv.(*ast.IndexExpr); ok {
			recv = idx.X
		} else if idx, ok := recv.(*ast.IndexListExpr); ok {
			recv = idx.X
		}
		if id, ok := recv.(*ast.Ident); ok && id.Name == companion {
			return fn
		}
	}
	return nil
}

// typeParamTypes lists the type parameters tParams declares, as types.
func typeParamTypes(tParams *ast.FieldList) []transpiler.Type {
	var out []transpiler.Type
	for _, f := range tParams.List {
		for _, n := range f.Names {
			out = append(out, transpiler.BasicType{Name: n.Name})
		}
	}
	return out
}

// goParamName is a field name as a Go parameter name: lower-case first, with
// an underscore after one that is then a Go keyword (`Type` -> `type_`).
func goParamName(field string) string {
	name := lowerFirst(field)
	if token.IsKeyword(name) {
		name += "_"
	}
	return name
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}
