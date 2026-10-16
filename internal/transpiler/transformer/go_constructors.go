package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strings"
	"unicode"

	"martianoff/gala/internal/transpiler"
)

// Go constructors. Go can build a GALA value without calling any GALA code (a
// composite literal, a zero value), which skips the field defaults GALA
// applies and the wrapping of immutable fields. So an exported sealed type and
// an exported shorthand struct get constructors for Go callers, and a doc
// comment saying to use them:
//
//   - NewT() returns sealed type T's default value, when T has a default case;
//   - NewT<Case>(...) builds a case of sealed type T, and NewT(...) a shorthand
//     struct T whose fields are all exported: a field without a default is a
//     parameter, a field with one an option (With<T><Case><Field>, or
//     With<T><Field>) applied over its default.
//
// A constructor is not generated when the package already declares one of the
// names it needs, or when two types of the package would generate the same
// name.

// sealedGoConstructors returns the Go constructors of the exported sealed type
// name (cases variants, type parameters tParams), with the Apply methods its
// cases were generated with in decls, and the paragraph its doc comment gains.
func (t *galaASTTransformer) sealedGoConstructors(name string, variants []sealedVariantInfo, tParams *ast.FieldList, decls []ast.Decl, line, col int) ([]ast.Decl, string, error) {
	if !ast.IsExported(name) {
		return nil, "", nil
	}
	// The constructors are declared with the type's parameters, so its field
	// defaults are lowered with them in scope.
	defer t.bindTypeParams(fieldListOrNil(tParams)...)()
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
		apply := findCompanionApply(decls, vi.name)
		if apply == nil {
			continue
		}
		ctorName := goCaseCtorName(name, vi.name)
		ctorDecls, err := t.sealedCaseConstructor(name, vi, ctorName, apply, parentType, tParams, line, col)
		if err != nil {
			return nil, "", err
		}
		if ctorDecls != nil {
			out = append(out, ctorDecls...)
			caseCtors = append(caseCtors, ctorName)
		}
	}

	var doc strings.Builder
	zeroLit := types.ExprString(parentType) + "{}"
	switch {
	case defaultCase == nil:
		fmt.Fprintf(&doc, "%s has no default value", name)
		if len(caseCtors) > 0 {
			fmt.Fprintf(&doc, ": create it with a case constructor, %s", strings.Join(caseCtors, ", "))
		}
		fmt.Fprintf(&doc, ". Its zero value %s is none of its cases.", zeroLit)
	case t.goNameAvailable("New" + name):
		val, err := t.defaultValue(genericType(name, tParams), line, col, map[string]bool{})
		if err != nil {
			return nil, "", err
		}
		if val == nil {
			val = &ast.CompositeLit{Type: parentType}
		}
		described := t.describeDefaultCase(*defaultCase)
		ctorDoc := fmt.Sprintf("New%s returns %s's default value", name, name)
		fmt.Fprintf(&doc, "Use New%s for the default value", name)
		if described != "" {
			ctorDoc += ": " + described
			fmt.Fprintf(&doc, " (%s)", described)
		}
		out = append(out, goCtorDecl("New"+name, ctorDoc+", what GALA uses where none is given.",
			tParams, &ast.FieldList{}, parentType, []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{val}}}))
		if len(caseCtors) > 0 {
			fmt.Fprintf(&doc, ", or a case constructor: %s", strings.Join(caseCtors, ", "))
		}
		doc.WriteString(".")
		if apply := t.companionApply(defaultCase.name); apply != nil && len(apply.DefaultExprs) > 0 {
			fmt.Fprintf(&doc, " A composite literal %s does not apply GALA's field defaults: its fields hold Go's zero values.", zeroLit)
		}
	}
	return out, doc.String(), nil
}

// sealedCaseConstructor is NewT<Case> for case vi of sealed type parent: the
// parameters of its companion's Apply, those with a declared default as
// options over it. It returns no declarations when a name it needs is taken.
func (t *galaASTTransformer) sealedCaseConstructor(parent string, vi *sealedVariantInfo, ctorName string, apply *ast.FuncDecl, parentType ast.Expr, tParams *ast.FieldList, line, col int) ([]ast.Decl, error) {
	applyMeta := t.companionApply(vi.name)
	companionType := t.buildGenericTypeExpr(vi.name, tParams)
	var params []goCtorParam
	for _, f := range apply.Type.Params.List {
		for _, n := range f.Names {
			p := goCtorParam{name: n.Name, typ: f.Type}
			if applyMeta != nil {
				if d, ok := applyMeta.DefaultExprs[len(params)]; ok {
					p.def = strings.TrimSpace(d.Text)
				}
			}
			params = append(params, p)
		}
	}
	doc := fmt.Sprintf("%s returns the %s case of %s", ctorName, vi.name, parent)
	var tail string
	if comp := t.getTypeMeta(vi.name); comp != nil && comp.Doc != "" {
		tail = comp.Doc
	}
	return t.optionsConstructor(goOptionsCtor{
		parent:   parent,
		caseName: vi.name,
		params:   params,
		defaultOf: func(i int) (ast.Expr, error) {
			return t.methodDefaultArg(applyMeta, i, &ast.CompositeLit{Type: companionType}, genericType(vi.name, tParams), nil, line, col)
		},
		build:   func(args []ast.Expr) ast.Expr { return applyCall(companionType, args) },
		doc:     doc,
		docTail: tail,
		tParams: tParams,
		result:  parentType,
	})
}

// structGoConstructor is NewT for the exported shorthand struct name, with
// paramTypes the Go types its fields (fieldNames) are passed as: a field
// without a default is a parameter, a field with one an option
// (With<T><Field>) applied over its default. It returns no declarations when
// the struct has a field Go cannot set or a name it needs is taken, and
// otherwise the sentence the struct's doc comment gains.
func (t *galaASTTransformer) structGoConstructor(name string, fieldNames []string, paramTypes []ast.Expr, tParams *ast.FieldList, line, col int) ([]ast.Decl, string, error) {
	if !ast.IsExported(name) {
		return nil, "", nil
	}
	meta := t.getTypeMeta(name)
	if meta == nil || !structCtorEligible(meta, fieldNames) {
		return nil, "", nil
	}
	structType := t.buildGenericTypeExpr(name, tParams)
	fieldTypes, immutFlags := t.structFieldTypes[name], t.structImmutFields[name]
	// A default naming the struct's type parameters means the constructor's.
	selfArgs := map[string]transpiler.Type{}
	for _, tp := range meta.TypeParams {
		selfArgs[tp] = transpiler.BasicType{Name: tp}
	}
	params := make([]goCtorParam, len(fieldNames))
	for i, f := range fieldNames {
		params[i] = goCtorParam{name: f, typ: paramTypes[i]}
		if d, ok := meta.FieldDefaults[f]; ok {
			params[i].def = strings.TrimSpace(d.Text)
		}
	}
	decls, err := t.optionsConstructor(goOptionsCtor{
		parent: name,
		params: params,
		defaultOf: func(i int) (ast.Expr, error) {
			return t.declaredFieldDefault(meta, fieldNames[i], fieldTypes[fieldNames[i]], selfArgs, line, col)
		},
		build: func(args []ast.Expr) ast.Expr {
			elts := make([]ast.Expr, len(args))
			for i, arg := range args {
				if immutFlags[i] {
					arg = t.wrapImmutableFieldValue(arg, fieldTypes[fieldNames[i]], nil)
				}
				elts[i] = &ast.KeyValueExpr{Key: ast.NewIdent(fieldNames[i]), Value: arg}
			}
			return &ast.CompositeLit{Type: structType, Elts: elts}
		},
		doc:     fmt.Sprintf("New%s returns a new %s", name, name),
		tParams: tParams,
		result:  structType,
	})
	if err != nil || decls == nil {
		return nil, "", err
	}
	guidance := fmt.Sprintf("From Go, create it with New%s.", name)
	if len(meta.FieldDefaults) > 0 {
		guidance += fmt.Sprintf(" A composite literal %s{} does not apply GALA's field defaults: its fields hold Go's zero values.", types.ExprString(structType))
	}
	return decls, guidance, nil
}

// structCtorEligible reports whether a shorthand struct gets a Go constructor:
// it has fields, and every one is exported, since GALA lets only the declaring
// package set an unexported one. A struct without fields is built as T{}.
func structCtorEligible(meta *transpiler.TypeMetadata, fieldNames []string) bool {
	if !meta.IsShorthand || len(fieldNames) == 0 {
		return false
	}
	for _, f := range fieldNames {
		if !ast.IsExported(f) {
			return false
		}
	}
	return true
}

// goCtorParam is one parameter of a Go constructor: a field, its Go type,
// and its declared default as written ("" when it has none).
type goCtorParam struct {
	name string
	typ  ast.Expr
	def  string
}

// goOptionsCtor describes a Go constructor whose fields with a default are
// options over it: NewT<Case> for case caseName of sealed type parent, or
// NewT for struct parent when caseName is "".
type goOptionsCtor struct {
	parent, caseName string
	params           []goCtorParam
	defaultOf        func(i int) (ast.Expr, error) // params[i]'s default
	build            func(args []ast.Expr) ast.Expr
	doc, docTail     string
	tParams          *ast.FieldList
	result           ast.Expr
}

// goCtorNames returns the names the constructor for case c of parent (c ""
// for a struct) declares: the constructor, and with defaulted fields, its
// option type, its arguments struct and a With function per field.
func goCtorNames(parent, c string, defaulted []string) []string {
	names := []string{goCaseCtorName(parent, c)}
	if len(defaulted) > 0 {
		names = append(names, goOptionTypeName(parent, c), goArgsTypeName(parent, c))
		for _, f := range defaulted {
			names = append(names, goWithName(parent, c, f))
		}
	}
	return names
}

// optionsConstructor builds c: the constructor, and when a parameter has a
// default, the option type, its arguments struct and a With function per
// defaulted parameter. It returns no declarations when a name it needs is
// taken.
func (t *galaASTTransformer) optionsConstructor(c goOptionsCtor) ([]ast.Decl, error) {
	type param struct {
		goCtorParam
		i int
	}
	var required, optional []param
	for i, p := range c.params {
		if p.def != "" {
			optional = append(optional, param{p, i})
		} else {
			required = append(required, param{p, i})
		}
	}
	tParams := c.tParams
	defaulted := make([]string, len(optional))
	for i, p := range optional {
		defaulted[i] = p.name
	}
	for _, n := range goCtorNames(c.parent, c.caseName, defaulted) {
		if !t.goNameAvailable(n) {
			return nil, nil
		}
	}
	name := goCaseCtorName(c.parent, c.caseName)
	option, argsName := goOptionTypeName(c.parent, c.caseName), goArgsTypeName(c.parent, c.caseName)
	subject := c.caseName
	if subject == "" {
		subject = c.parent
	}

	args := make([]ast.Expr, len(c.params))
	var inits []ast.Expr
	for _, p := range optional {
		val, err := c.defaultOf(p.i)
		if err != nil {
			return nil, err
		}
		inits = append(inits, &ast.KeyValueExpr{Key: ast.NewIdent(p.name), Value: val})
		args[p.i] = &ast.SelectorExpr{X: ast.NewIdent("_args"), Sel: ast.NewIdent(p.name)}
	}
	// A parameter must not shadow a name the defaults read, nor take another
	// parameter's name.
	taken := map[string]bool{}
	for _, init := range inits {
		ast.Inspect(init, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				taken[id.Name] = true
			}
			return true
		})
	}
	var decls []ast.Decl
	params := &ast.FieldList{}
	for _, p := range required {
		pname := t.goParamName(p.name)
		for taken[pname] {
			pname += "_"
		}
		taken[pname] = true
		params.List = append(params.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(pname)}, Type: p.typ})
		args[p.i] = ast.NewIdent(pname)
	}
	var body []ast.Stmt
	if len(optional) > 0 {
		argsType := t.buildGenericTypeExpr(argsName, tParams)
		argsPtr := &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("_args")}, Type: &ast.StarExpr{X: argsType}}}}
		optionType := t.buildGenericTypeExpr(option, tParams)
		argsFields := &ast.FieldList{}
		for _, p := range optional {
			argsFields.List = append(argsFields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(p.name)}, Type: p.typ})
		}
		decls = append(decls,
			&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{Name: ast.NewIdent(argsName), TypeParams: tParams, Type: &ast.StructType{Fields: argsFields}}}},
			&ast.GenDecl{
				Doc: docCommentGroup(fmt.Sprintf("%s sets a field of %s left out of %s; its fields' defaults apply otherwise.", option, subject, name)),
				Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{Name: ast.NewIdent(option), TypeParams: tParams,
					Type: &ast.FuncType{Params: argsPtr}}},
			})
		for _, p := range optional {
			withName := goWithName(c.parent, c.caseName, p.name)
			set := &ast.FuncLit{
				Type: &ast.FuncType{Params: argsPtr},
				Body: &ast.BlockStmt{List: []ast.Stmt{&ast.AssignStmt{
					Lhs: []ast.Expr{&ast.SelectorExpr{X: ast.NewIdent("_args"), Sel: ast.NewIdent(p.name)}},
					Tok: token.ASSIGN, Rhs: []ast.Expr{ast.NewIdent("v")},
				}}},
			}
			decls = append(decls, goCtorDecl(withName,
				fmt.Sprintf("%s sets %s's %s (default %s).", withName, subject, p.name, p.def),
				tParams, &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("v")}, Type: p.typ}}}, optionType,
				[]ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{set}}}))
		}
		params.List = append(params.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent("_opts")}, Type: &ast.Ellipsis{Elt: optionType}})
		body = append(body,
			&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("_args")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CompositeLit{Type: argsType, Elts: inits}}},
			&ast.RangeStmt{Key: ast.NewIdent("_"), Value: ast.NewIdent("_opt"), Tok: token.DEFINE, X: ast.NewIdent("_opts"),
				Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_opt"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: ast.NewIdent("_args")}}}}}}},
		)
	}
	body = append(body, &ast.ReturnStmt{Results: []ast.Expr{c.build(args)}})

	doc := c.doc
	if len(optional) > 0 {
		defs := make([]string, len(optional))
		for j, p := range optional {
			defs[j] = p.name + " " + p.def
		}
		doc += fmt.Sprintf(", each option applied over its field defaults (%s)", strings.Join(defs, ", "))
	}
	doc += "."
	if c.docTail != "" {
		doc += "\n\n" + c.docTail
	}
	return append(decls, goCtorDecl(name, doc, tParams, params, c.result, body)), nil
}

// describeDefaultCase renders default case vi as its doc reads it, e.g.
// `On with Power 30 and Room "Living"`, or `Off`; "" for a case Go cannot
// name.
func (t *galaASTTransformer) describeDefaultCase(vi sealedVariantInfo) string {
	if !ast.IsExported(vi.name) {
		return ""
	}
	if len(vi.fields) == 0 {
		return vi.name
	}
	apply := t.companionApply(vi.name)
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
	return vi.name + " with " + andJoin(parts)
}

// companionApply returns the metadata of the Apply method of the case named
// name's companion, nil when there is none.
func (t *galaASTTransformer) companionApply(name string) *transpiler.MethodMetadata {
	if comp := t.getTypeMeta(name); comp != nil {
		return comp.Methods["Apply"]
	}
	return nil
}

// goNameAvailable reports whether a Go constructor may take name: this
// package declares nothing named so, in GALA or in a hand-written .go file,
// and no other of its types would generate it.
func (t *galaASTTransformer) goNameAvailable(name string) bool {
	key := ownFunctionKey(t.packageName, name)
	if _, ok := t.structFields[key]; ok {
		return false
	}
	if own := t.richAST.OwnImportPath; own != "" && t.goTypeInfo.HasQualified(own+"."+name) {
		return false
	}
	return t.functions[key] == nil && t.typeMetas[key] == nil && t.richAST.PackageVals[name] == nil && t.goCtorClaimed()[name] <= 1
}

// goCtorClaimed counts, for each name, the sealed types and shorthand structs
// of this package (across all its files) whose Go constructors would use it.
func (t *galaASTTransformer) goCtorClaimed() map[string]int {
	if t.goCtorClaims != nil {
		return t.goCtorClaims
	}
	claims := map[string]int{}
	for key, meta := range t.typeMetas {
		if meta.Package != t.packageName || !ast.IsExported(meta.Name) || key != ownFunctionKey(t.packageName, meta.Name) {
			continue
		}
		if structCtorEligible(meta, meta.FieldNames) {
			for _, n := range goCtorNames(meta.Name, "", slices.Sorted(maps.Keys(meta.FieldDefaults))) {
				claims[n]++
			}
			continue
		}
		if len(meta.SealedVariants) == 0 {
			continue
		}
		if meta.HasDefaultCase() {
			claims["New"+meta.Name]++
		}
		for _, v := range meta.SealedVariants {
			if !ast.IsExported(v.Name) {
				continue
			}
			var defaulted []string
			if comp := t.typeMetas[ownFunctionKey(t.packageName, v.Name)]; comp != nil && comp.Methods["Apply"] != nil {
				apply := comp.Methods["Apply"]
				for i := range apply.DefaultExprs {
					if i < len(apply.ParamNames) {
						defaulted = append(defaulted, apply.ParamNames[i])
					}
				}
			}
			for _, n := range goCtorNames(meta.Name, v.Name, defaulted) {
				claims[n]++
			}
		}
	}
	t.goCtorClaims = claims
	return claims
}

func goCaseCtorName(parent, c string) string    { return "New" + parent + c }
func goOptionTypeName(parent, c string) string  { return parent + c + "Option" }
func goArgsTypeName(parent, c string) string    { return lowerInitial(parent) + c + "Args" }
func goWithName(parent, c, field string) string { return "With" + parent + c + field }

// goCtorDecl is a generic-aware function declaration returning result.
func goCtorDecl(name, doc string, tParams, params *ast.FieldList, result ast.Expr, body []ast.Stmt) *ast.FuncDecl {
	return &ast.FuncDecl{
		Doc:  docCommentGroup(doc),
		Name: ast.NewIdent(name),
		Type: &ast.FuncType{TypeParams: tParams, Params: params, Results: &ast.FieldList{List: []*ast.Field{{Type: result}}}},
		Body: &ast.BlockStmt{List: body},
	}
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
		switch idx := recv.(type) {
		case *ast.IndexExpr:
			recv = idx.X
		case *ast.IndexListExpr:
			recv = idx.X
		}
		if id, ok := recv.(*ast.Ident); ok && id.Name == companion {
			return fn
		}
	}
	return nil
}

// genericType is the type name instantiated with the type parameters tParams
// declares.
func genericType(name string, tParams *ast.FieldList) transpiler.Type {
	base := transpiler.Type(transpiler.BasicType{Name: name})
	if tParams == nil {
		return base
	}
	var params []transpiler.Type
	for _, f := range tParams.List {
		for _, n := range f.Names {
			params = append(params, transpiler.BasicType{Name: n.Name})
		}
	}
	return transpiler.GenericType{Base: base, Params: params}
}

// goParamName is a field name as a Go parameter name: its leading capitals
// lower-cased the way Go spells an initialism (`SKU` -> `sku`, `URLPath` ->
// `urlPath`), with an underscore after one that is then a Go keyword or an
// imported package's name (`Type` -> `type_`).
func (t *galaASTTransformer) goParamName(field string) string {
	name := lowerInitial(field)
	if token.IsKeyword(name) || t.importManager.IsPackage(name) {
		name += "_"
	}
	return name
}

// lowerInitial lower-cases s's leading run of capitals, keeping the last one
// when a lower-case letter follows it: `SKU` -> `sku`, `URLPath` -> `urlPath`,
// `Qty` -> `qty`.
func lowerInitial(s string) string {
	runes := []rune(s)
	n := 0
	for n < len(runes) && unicode.IsUpper(runes[n]) {
		n++
	}
	if n > 1 && n < len(runes) && unicode.IsLower(runes[n]) {
		n--
	}
	for i := 0; i < n; i++ {
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}
