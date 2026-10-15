package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Interface literals. `UserRepo(Find = (id) => Some(u))` builds a value of the
// GALA interface UserRepo from one function per method, each argument named
// after the method it implements. Every method is given, and nothing else.
//
// A literal lowers to a pointer to a private helper struct holding one function
// field per method, whose methods call those functions, converted to the
// interface. A pointer keeps the value comparable: two literals are equal only
// when they are the same one, where a struct of functions would panic on ==.
//
//	type _UserRepo_literal_main struct{ _Find func(int) std.Option[User] }
//	func (l _UserRepo_literal_main) Find(p0 int) std.Option[User] { return l._Find(p0) }
//
//	UserRepo(&_UserRepo_literal_main{_Find: func(id int) std.Option[User] { ... }})
//
// A file declares the helper of each interface it builds literals of, named
// after the file so that two files of a package never declare the same one.

// interfaceLiteralTarget returns the metadata of the GALA interface fun names
// and the interface type the literal builds, with its type arguments: written
// (`Box[int](...)`) or taken from the expected type. ok is false when fun does
// not name a GALA interface with methods.
func (t *galaASTTransformer) interfaceLiteralTarget(fun ast.Expr, expected transpiler.Type) (meta *transpiler.TypeMetadata, resolved string, typeArgs []transpiler.Type, ok bool) {
	typeName, qualifiedName := extractTypeNameFromExpr(fun)
	if typeName == "" {
		return nil, "", nil, false
	}
	base := fun
	switch f := fun.(type) {
	case *ast.IndexExpr:
		base = f.X
		typeArgs = []transpiler.Type{t.astTypeToTranspilerType(f.Index)}
	case *ast.IndexListExpr:
		base = f.X
		for _, idx := range f.Indices {
			typeArgs = append(typeArgs, t.astTypeToTranspilerType(idx))
		}
	}
	lookup := qualifiedName
	if id, isIdent := base.(*ast.Ident); isIdent {
		if _, _, bound := t.scopeLookup(id.Name); bound {
			return nil, "", nil, false
		}
		lookup = typeName
	}
	meta, resolved = t.getTypeMetaResolved(lookup)
	if goMeta, goKey := t.goInterfaceLiteralMeta(base); goMeta != nil {
		meta, resolved = goMeta, goKey
	}
	if meta == nil || !meta.IsInterface || len(meta.Methods) == 0 {
		return nil, "", nil, false
	}
	if len(typeArgs) == 0 && len(meta.TypeParams) > 0 && expected != nil {
		if gen, isGen := t.followAliasChain(expected).(transpiler.GenericType); isGen && stripPackagePrefix(gen.Base.BaseName()) == meta.Name {
			typeArgs = gen.Params
		}
	}
	return meta, resolved, typeArgs, true
}

// goInterfaceLiteralMeta describes the Go interface base names, such as
// fmt.Stringer, as the metadata of an interface with its exported methods, and
// returns the key its helper is filed under; nil when base names no Go
// interface.
func (t *galaASTTransformer) goInterfaceLiteralMeta(base ast.Expr) (*transpiler.TypeMetadata, string) {
	if t.goTypeInfo == nil {
		return nil, ""
	}
	nt, ok := t.followAliasChain(t.astTypeToTranspilerType(base)).(transpiler.NamedType)
	if !ok || t.isLocalPackage(nt.Package) {
		return nil, ""
	}
	key := t.goTypeLookupName(nt)
	td := t.goTypeInfo.GetTypeData(key)
	if td == nil || td.Kind != "interface" {
		return nil, ""
	}
	meta := &transpiler.TypeMetadata{Name: nt.Name, Package: nt.Package, IsInterface: true, TypeParams: td.TypeParams,
		Methods: make(map[string]*transpiler.MethodMetadata, len(td.Methods))}
	for name, sig := range td.Methods {
		m := &transpiler.MethodMetadata{Name: name, Package: nt.Package, ReturnType: transpiler.VoidType{}, GoDeclared: true}
		for _, p := range sig.Params {
			m.ParamTypes = append(m.ParamTypes, p.Type)
		}
		if n := len(m.ParamTypes); sig.IsVariadic && n > 0 {
			if slice, isSlice := m.ParamTypes[n-1].(transpiler.ArrayType); isSlice {
				m.ParamTypes[n-1], m.Variadic = slice.Elem, true
			}
		}
		switch len(sig.Returns) {
		case 0:
		case 1:
			m.ReturnType = sig.Returns[0]
		default:
			m.GoResults = sig.Returns
		}
		meta.Methods[name] = m
	}
	return meta, "go:" + key
}

// tryTransformInterfaceLiteral lowers a call of a GALA interface's name, an
// interface literal; handled is false for any other call.
func (t *galaASTTransformer) tryTransformInterfaceLiteral(fun ast.Expr, argListCtx *grammar.ArgumentListContext, expected transpiler.Type) (expr ast.Expr, handled bool, err error) {
	meta, resolved, typeArgs, ok := t.interfaceLiteralTarget(fun, expected)
	if !ok {
		return nil, false, nil
	}
	line, col := argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn()
	if fl, fc, exact := primaryStartOf(argListCtx); exact {
		line, col = fl, fc
	}
	name := t.displayTypeName(meta)
	invalid := func(line, col int, msg, hint string) error {
		return galaerr.NewCodedSemanticError(galaerr.CodeInvalidInterfaceLiteral, line, col, msg, hint)
	}

	methods := slices.Sorted(func(yield func(string) bool) {
		for m := range meta.Methods {
			if !yield(m) {
				return
			}
		}
	})
	for _, m := range methods {
		mm := meta.Methods[m]
		switch {
		case len(mm.TypeParams) > 0:
			return nil, true, invalid(line, col, fmt.Sprintf("method %s of %s is generic, and a function cannot implement a generic method", m, name),
				fmt.Sprintf("declare a GALA struct that implements %s", name))
		case !t.isLocalPackage(meta.Package) && !token.IsExported(m):
			return nil, true, invalid(line, col, fmt.Sprintf("method %s of %s is unexported, so only package %s can implement it", m, name, meta.Package),
				fmt.Sprintf("build the value in package %s", meta.Package))
		}
	}
	if len(typeArgs) != len(meta.TypeParams) {
		return nil, true, invalid(line, col, fmt.Sprintf("%s is generic, and the literal does not say its type arguments", name),
			fmt.Sprintf("write them, as in `%s[%s](...)`, or build the value where %s is the expected type", name, strings.Join(meta.TypeParams, ", "), name))
	}
	subst := make(typeSubstMap, len(typeArgs))
	for i, tp := range meta.TypeParams {
		subst[tp] = typeArgs[i]
	}

	given := make(map[string]ast.Expr, len(methods))
	for _, a := range argListCtx.AllArgument() {
		arg := a.(*grammar.ArgumentContext)
		argLine, argCol := arg.GetStart().GetLine(), arg.GetStart().GetColumn()
		if arg.Identifier() == nil {
			return nil, true, invalid(argLine, argCol, fmt.Sprintf("an argument of an interface literal of %s names the method it implements", name),
				fmt.Sprintf("write `%s = ...` for one of %s's methods: %s", methods[0], name, strings.Join(methods, ", ")))
		}
		method := arg.Identifier().GetText()
		mm, isMethod := meta.Methods[method]
		if !isMethod {
			return nil, true, invalid(argLine, argCol, fmt.Sprintf("%s has no method %s", name, method),
				fmt.Sprintf("the methods of %s are %s", name, strings.Join(methods, ", ")))
		}
		if _, dup := given[method]; dup {
			return nil, true, invalid(argLine, argCol, fmt.Sprintf("method %s of %s is given twice", method, name),
				fmt.Sprintf("give %s once", method))
		}
		exprCtx, lambdaCtx, spread, cerr := extractArgContent(arg)
		if cerr != nil {
			return nil, true, cerr
		}
		if spread {
			return nil, true, invalid(argLine, argCol, fmt.Sprintf("method %s of %s is given a spread argument", method, name),
				"give it a function")
		}
		value, lerr := t.lowerArg(exprCtx, lambdaCtx, slot{typ: t.substituteTranspilerTypeParams(interfaceMethodFuncType(mm), subst)}, false)
		if lerr != nil {
			return nil, true, lerr
		}
		given[method] = value
	}
	var missing []string
	for _, m := range methods {
		if _, ok := given[m]; !ok {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		noun := "method"
		if len(missing) > 1 {
			noun = "methods"
		}
		return nil, true, invalid(line, col, fmt.Sprintf("interface literal of %s leaves out %s %s", name, noun, strings.Join(missing, ", ")),
			fmt.Sprintf("give every method of %s, as in `%s = ...`", name, missing[0]))
	}

	helper := t.interfaceLiteralHelper(meta, resolved)
	var typeArgExprs []ast.Expr
	for _, ta := range typeArgs {
		typeArgExprs = append(typeArgExprs, t.typeToExpr(ta))
	}
	lit := &ast.CompositeLit{Type: withTypeArgs(ast.NewIdent(helper), typeArgExprs)}
	for _, m := range methods {
		lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: ast.NewIdent(interfaceLiteralField(m)), Value: given[m]})
	}
	ifaceExpr := fun
	switch f := fun.(type) {
	case *ast.IndexExpr:
		ifaceExpr = f.X
	case *ast.IndexListExpr:
		ifaceExpr = f.X
	}
	call := &ast.CallExpr{Fun: withTypeArgs(ifaceExpr, typeArgExprs), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: lit}}}
	ifaceType := transpiler.Type(transpiler.NamedType{Package: meta.Package, Name: meta.Name})
	if t.isLocalPackage(meta.Package) {
		ifaceType = transpiler.BasicType{Name: meta.Name}
	}
	if len(typeArgs) > 0 {
		ifaceType = transpiler.GenericType{Base: ifaceType, Params: typeArgs}
	}
	t.exprTypeCache[call] = ifaceType
	return call, true, nil
}

// interfaceMethodFuncType is the function type a literal gives for method m:
// its parameters, and its result or its Go result list. A variadic method's
// function takes the slice its last parameter is, as a variadic function's
// body sees it.
func interfaceMethodFuncType(m *transpiler.MethodMetadata) transpiler.FuncType {
	ft := transpiler.FuncType{Params: m.ParamTypes}
	if n := len(m.ParamTypes); m.Variadic && n > 0 {
		ft.Params = append(slices.Clone(m.ParamTypes[:n-1]), transpiler.ArrayType{Elem: m.ParamTypes[n-1]})
	}
	switch {
	case m.GoResults != nil:
		ft.Results = m.GoResults
	case m.ReturnType != nil && !m.ReturnType.IsNil() && !m.ReturnType.IsVoid():
		ft.Results = []transpiler.Type{m.ReturnType}
	}
	return ft
}

// interfaceLiteralField names the helper struct's field holding the function
// for method m; a field cannot share its type's method name.
func interfaceLiteralField(m string) string { return "_" + m }

// interfaceLiteralHelper returns the name of this file's helper type for the
// interface meta describes (resolved names it), declaring it on first use.
func (t *galaASTTransformer) interfaceLiteralHelper(meta *transpiler.TypeMetadata, resolved string) string {
	if name, ok := t.interfaceLiterals[resolved]; ok {
		return name
	}
	ifaceName := meta.Name
	if !t.isLocalPackage(meta.Package) {
		ifaceName = meta.Package + "_" + meta.Name
	}
	name := "_" + ifaceName + "_literal"
	if tag := fileTag(t.filePath); tag != "" {
		name += "_" + tag
	}
	t.interfaceLiterals[resolved] = name

	restore := t.onlyTypeParams(meta.TypeParams)
	defer restore()
	var typeParams *ast.FieldList
	var recvArgs []ast.Expr
	for _, tp := range meta.TypeParams {
		constraint := ast.Expr(ast.NewIdent("any"))
		if c := meta.TypeParamConstraints[tp]; c != "" {
			constraint = t.typeToExpr(transpiler.ParseType(c))
		}
		if typeParams == nil {
			typeParams = &ast.FieldList{}
		}
		typeParams.List = append(typeParams.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(tp)}, Type: constraint})
		recvArgs = append(recvArgs, ast.NewIdent(tp))
	}
	methods := slices.Sorted(func(yield func(string) bool) {
		for m := range meta.Methods {
			if !yield(m) {
				return
			}
		}
	})
	fields := &ast.FieldList{}
	var decls []ast.Decl
	for _, m := range methods {
		mm := meta.Methods[m]
		fieldType := t.typeToExpr(interfaceMethodFuncType(mm))
		fields.List = append(fields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(interfaceLiteralField(m))}, Type: fieldType})

		sig := &ast.FuncType{Params: &ast.FieldList{}}
		var callArgs []ast.Expr
		for i, pt := range mm.ParamTypes {
			param := fmt.Sprintf("p%d", i)
			ptExpr := t.typeToExpr(pt)
			if mm.Variadic && i == len(mm.ParamTypes)-1 {
				ptExpr = &ast.Ellipsis{Elt: ptExpr}
			}
			sig.Params.List = append(sig.Params.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(param)}, Type: ptExpr})
			callArgs = append(callArgs, ast.NewIdent(param))
		}
		if ft, ok := fieldType.(*ast.FuncType); ok {
			sig.Results = ft.Results
		}
		call := &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("l"), Sel: ast.NewIdent(interfaceLiteralField(m))}, Args: callArgs}
		var body ast.Stmt = &ast.ExprStmt{X: call}
		if sig.Results != nil && len(sig.Results.List) > 0 {
			body = &ast.ReturnStmt{Results: []ast.Expr{call}}
		}
		decls = append(decls, &ast.FuncDecl{
			Recv: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("l")}, Type: withTypeArgs(ast.NewIdent(name), recvArgs)}}},
			Name: ast.NewIdent(m),
			Type: sig,
			Body: &ast.BlockStmt{List: []ast.Stmt{body}},
		})
	}
	typeDecl := &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
		Name:       ast.NewIdent(name),
		TypeParams: typeParams,
		Type:       &ast.StructType{Fields: fields},
	}}}
	t.interfaceLiteralDecls = append(t.interfaceLiteralDecls, append([]ast.Decl{typeDecl}, decls...)...)
	return name
}

// fileTag is the base name of path, without its extension, as identifier
// characters: what keeps the helpers of two files of a package apart.
func fileTag(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if base == "." || base == "" {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return r
		}
		return '_'
	}, base)
}
