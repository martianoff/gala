package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Interface literals. `UserRepo(Find = (id) => Some(u))` builds a value of the
// interface UserRepo from one function per method, each argument named after
// the method it implements. Every method is given, and nothing else.
//
// A literal lowers to a pointer to a private helper struct holding one function
// field per method, whose methods call those functions, converted to the
// interface. A pointer keeps the value comparable: two literals are equal only
// when they are the same one, where a struct of functions would panic on ==.
//
//	type _UserRepo_literal_1a2b3c4d struct{ _Find func(int) std.Option[User] }
//	func (l *_UserRepo_literal_1a2b3c4d) Find(p0 int) std.Option[User] { return l._Find(p0) }
//
//	UserRepo(&_UserRepo_literal_1a2b3c4d{_Find: func(id int) std.Option[User] { ... }})
//
// A file declares the helper of each interface it builds literals of, its name
// suffixed per file (codecFileSuffix) so that two files of a package never
// declare the same one.

// interfaceLiteral is the interface a call of its name builds.
type interfaceLiteral struct {
	meta     *transpiler.TypeMetadata
	resolved string // the key its helper is filed under
	base     ast.Expr
	// typeArgs are the interface's type arguments, written or expected;
	// typeArgExprs spell them in Go.
	typeArgs     []transpiler.Type
	typeArgExprs []ast.Expr
	methods      []string // sorted
	// goInexact names the methods of a Go interface whose signature GALA
	// cannot spell exactly (GoFuncSignature.Inexact).
	goInexact []string
	goGeneric bool // a generic Go interface, whose constraints are unknown
}

// interfaceLiteralTarget returns the interface fun names, a GALA interface or
// one of an imported Go package, with its type arguments: written
// (`Box[int](...)`) or taken from the expected type. ok is false when fun names
// no interface with methods.
func (t *galaASTTransformer) interfaceLiteralTarget(fun ast.Expr, expected transpiler.Type) (lit interfaceLiteral, ok bool) {
	base, written := splitCallFunTypeArgs(fun)
	var name string
	switch b := base.(type) {
	case *ast.Ident:
		if _, _, bound := t.scopeLookup(b.Name); bound {
			return lit, false
		}
		name = b.Name
	case *ast.SelectorExpr:
		// Only a package-qualified name names a type; `x.M(...)` is a call.
		pkg, isIdent := b.X.(*ast.Ident)
		if !isIdent || !t.importManager.IsPackage(pkg.Name) {
			return lit, false
		}
		if _, _, bound := t.scopeLookup(pkg.Name); bound {
			return lit, false
		}
		name = pkg.Name + "." + b.Sel.Name
	default:
		return lit, false
	}
	// Most calls name a function: remember the names that are no interface.
	if t.interfaceLiteralMisses[name] {
		return lit, false
	}
	meta, resolved := t.getTypeMetaResolved(name)
	if _, qualified := base.(*ast.SelectorExpr); qualified && (meta == nil || !meta.IsInterface) {
		var goLit interfaceLiteral
		goLit, meta, resolved = t.goInterfaceLiteralMeta(base)
		lit.goInexact, lit.goGeneric = goLit.goInexact, goLit.goGeneric
	}
	if meta == nil || !meta.IsInterface || len(meta.Methods) == 0 {
		t.interfaceLiteralMisses[name] = true
		return lit, false
	}
	lit.meta, lit.resolved, lit.base, lit.typeArgExprs = meta, resolved, base, written
	lit.methods = slices.Sorted(maps.Keys(meta.Methods))
	for _, w := range written {
		lit.typeArgs = append(lit.typeArgs, t.astTypeToTranspilerType(w))
	}
	if len(written) == 0 && len(meta.TypeParams) > 0 && expected != nil {
		if gen, isGen := t.followAliasChain(expected).(transpiler.GenericType); isGen && stripPackagePrefix(gen.Base.BaseName()) == meta.Name {
			lit.typeArgs = gen.Params
			for _, ta := range gen.Params {
				lit.typeArgExprs = append(lit.typeArgExprs, t.typeToExpr(ta))
			}
		}
	}
	return lit, true
}

// goInterfaceLiteralMeta describes the Go interface base names, such as
// fmt.Stringer, as the metadata of an interface with its exported methods, and
// returns the key its helper is filed under, with what a literal of it cannot
// spell (goInexact, goGeneric); nil when base names no Go interface.
func (t *galaASTTransformer) goInterfaceLiteralMeta(base ast.Expr) (interfaceLiteral, *transpiler.TypeMetadata, string) {
	var lit interfaceLiteral
	if t.goTypeInfo == nil {
		return lit, nil, ""
	}
	nt, ok := t.followAliasChain(t.astTypeToTranspilerType(base)).(transpiler.NamedType)
	if !ok || t.isLocalPackage(nt.Package) {
		return lit, nil, ""
	}
	key := t.goTypeLookupName(nt)
	td := t.goTypeInfo.GetTypeData(key)
	if td == nil || td.Kind != "interface" {
		return lit, nil, ""
	}
	lit.goGeneric = len(td.TypeParams) > 0
	meta := &transpiler.TypeMetadata{Name: nt.Name, Package: nt.Package, IsInterface: true, TypeParams: td.TypeParams,
		Methods: make(map[string]*transpiler.MethodMetadata, len(td.Methods))}
	for name, sig := range td.Methods {
		if sig.Inexact {
			lit.goInexact = append(lit.goInexact, name)
		}
		// A Go signature records a variadic parameter as its element type.
		m := &transpiler.MethodMetadata{Name: name, Package: nt.Package, ReturnType: transpiler.VoidType{}, Variadic: sig.IsVariadic, GoDeclared: true}
		for _, p := range sig.Params {
			m.ParamTypes = append(m.ParamTypes, p.Type)
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
	slices.Sort(lit.goInexact)
	return lit, meta, "go:" + key
}

// tryTransformInterfaceLiteral lowers a call of an interface's name, an
// interface literal; handled is false for any other call.
func (t *galaASTTransformer) tryTransformInterfaceLiteral(fun ast.Expr, argListCtx *grammar.ArgumentListContext, expected transpiler.Type) (expr ast.Expr, handled bool, err error) {
	lit, ok := t.interfaceLiteralTarget(fun, expected)
	if !ok {
		return nil, false, nil
	}
	line, col := argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn()
	if fl, fc, exact := primaryStartOf(argListCtx); exact {
		line, col = fl, fc
	}
	if err := t.checkInterfaceLiteralTarget(lit, line, col); err != nil {
		return nil, true, err
	}
	given, err := t.interfaceLiteralMethods(lit, argListCtx, line, col)
	if err != nil {
		return nil, true, err
	}

	helper := &ast.CompositeLit{Type: withTypeArgs(ast.NewIdent(t.interfaceLiteralHelper(lit, line)), lit.typeArgExprs)}
	for _, m := range lit.methods {
		helper.Elts = append(helper.Elts, &ast.KeyValueExpr{Key: ast.NewIdent(interfaceLiteralField(m)), Value: given[m]})
	}
	call := &ast.CallExpr{Fun: withTypeArgs(lit.base, lit.typeArgExprs), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: helper}}}
	ifaceType := transpiler.Type(transpiler.NamedType{Package: lit.meta.Package, Name: lit.meta.Name})
	if t.isLocalPackage(lit.meta.Package) {
		ifaceType = transpiler.BasicType{Name: lit.meta.Name}
	}
	if len(lit.typeArgs) > 0 {
		ifaceType = transpiler.GenericType{Base: ifaceType, Params: lit.typeArgs}
	}
	t.exprTypeCache[call] = ifaceType
	return call, true, nil
}

// invalidInterfaceLiteral is a GALA-E0078 error.
func invalidInterfaceLiteral(line, col int, msg, hint string) error {
	return galaerr.NewCodedSemanticError(galaerr.CodeInvalidInterfaceLiteral, line, col, msg, hint)
}

// checkInterfaceLiteralTarget rejects an interface a literal cannot build: one
// with a generic method or another package's unexported method, or a generic
// one whose type arguments are unknown.
func (t *galaASTTransformer) checkInterfaceLiteralTarget(lit interfaceLiteral, line, col int) error {
	name := t.displayTypeName(lit.meta)
	if lit.goGeneric {
		return invalidInterfaceLiteral(line, col, fmt.Sprintf("%s is a generic Go interface, and a literal cannot spell its type parameters' constraints", name),
			fmt.Sprintf("declare a GALA struct that implements %s", name))
	}
	if len(lit.goInexact) > 0 {
		return invalidInterfaceLiteral(line, col, fmt.Sprintf("method %s of %s has a Go type GALA cannot spell exactly (a channel, an anonymous struct, an inline interface or a generic type instance)", lit.goInexact[0], name),
			fmt.Sprintf("declare a Go or GALA type that implements %s", name))
	}
	for _, m := range lit.methods {
		switch {
		case len(lit.meta.Methods[m].TypeParams) > 0:
			return invalidInterfaceLiteral(line, col, fmt.Sprintf("method %s of %s is generic, and a function cannot implement a generic method", m, name),
				fmt.Sprintf("declare a GALA struct that implements %s", name))
		case !t.isLocalPackage(lit.meta.Package) && !token.IsExported(m):
			return invalidInterfaceLiteral(line, col, fmt.Sprintf("method %s of %s is unexported, so only package %s can implement it", m, name, lit.meta.Package),
				fmt.Sprintf("build the value in package %s", lit.meta.Package))
		}
	}
	if len(lit.typeArgs) != len(lit.meta.TypeParams) {
		return invalidInterfaceLiteral(line, col, fmt.Sprintf("%s is generic, and the literal does not say its type arguments", name),
			fmt.Sprintf("write them, as in `%s[%s](...)`, or build the value where %s is the expected type", name, strings.Join(lit.meta.TypeParams, ", "), name))
	}
	return nil
}

// interfaceLiteralMethods lowers the literal's arguments, each against the
// function type of the method it names, and checks that they give every method
// once.
func (t *galaASTTransformer) interfaceLiteralMethods(lit interfaceLiteral, argListCtx *grammar.ArgumentListContext, line, col int) (map[string]ast.Expr, error) {
	name := t.displayTypeName(lit.meta)
	subst := make(typeSubstMap, len(lit.typeArgs))
	for i, tp := range lit.meta.TypeParams {
		subst[tp] = lit.typeArgs[i]
	}
	given := make(map[string]ast.Expr, len(lit.methods))
	for _, a := range argListCtx.AllArgument() {
		arg := a.(*grammar.ArgumentContext)
		argLine, argCol := arg.GetStart().GetLine(), arg.GetStart().GetColumn()
		if arg.Identifier() == nil {
			return nil, invalidInterfaceLiteral(argLine, argCol, fmt.Sprintf("an argument of an interface literal of %s names the method it implements", name),
				fmt.Sprintf("write `%s = ...` for one of %s's methods: %s", lit.methods[0], name, strings.Join(lit.methods, ", ")))
		}
		method := arg.Identifier().GetText()
		mm, isMethod := lit.meta.Methods[method]
		if !isMethod {
			return nil, invalidInterfaceLiteral(argLine, argCol, fmt.Sprintf("%s has no method %s", name, method),
				fmt.Sprintf("the methods of %s are %s", name, strings.Join(lit.methods, ", ")))
		}
		if _, dup := given[method]; dup {
			return nil, invalidInterfaceLiteral(argLine, argCol, fmt.Sprintf("method %s of %s is given twice", method, name),
				fmt.Sprintf("give %s once", method))
		}
		exprCtx, lambdaCtx, spread, err := extractArgContent(arg)
		if err != nil {
			return nil, err
		}
		if spread {
			return nil, invalidInterfaceLiteral(argLine, argCol, fmt.Sprintf("method %s of %s is given a spread argument", method, name),
				"give it a function")
		}
		value, err := t.lowerArg(exprCtx, lambdaCtx, slot{typ: t.substituteTranspilerTypeParams(interfaceMethodFuncType(mm), subst)}, false)
		if err != nil {
			return nil, err
		}
		given[method] = value
	}
	var missing []string
	for _, m := range lit.methods {
		if _, ok := given[m]; !ok {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		noun := "method"
		if len(missing) > 1 {
			noun = "methods"
		}
		return nil, invalidInterfaceLiteral(line, col, fmt.Sprintf("interface literal of %s leaves out %s %s", name, noun, strings.Join(missing, ", ")),
			fmt.Sprintf("give every method of %s, as in `%s = ...`", name, missing[0]))
	}
	return given, nil
}

// interfaceMethodFuncType is the function type a literal gives for method m:
// its parameters, and its result or its Go result list. A variadic method's
// function takes the slice its last parameter is, as a variadic function's
// body sees it.
func interfaceMethodFuncType(m *transpiler.MethodMetadata) transpiler.FuncType {
	ft := signatureType(m.ParamTypes, m.ReturnType, m.GoResults)
	if n := len(ft.Params); m.Variadic && n > 0 {
		ft.Params[n-1] = transpiler.ArrayType{Elem: ft.Params[n-1]}
	}
	return ft
}

// interfaceLiteralField names the helper struct's field holding the function
// for method m; a field cannot share its type's method name.
func interfaceLiteralField(m string) string { return "_" + m }

// interfaceLiteralHelper returns the name of this file's helper type for the
// interface lit builds, declaring it on first use, at line: Go reports an
// error in the helper, and a stack trace passes through it, at the literal
// that declared it.
func (t *galaASTTransformer) interfaceLiteralHelper(lit interfaceLiteral, line int) string {
	if name, ok := t.interfaceLiterals[lit.resolved]; ok {
		return name
	}
	meta := lit.meta
	ifaceName := meta.Name
	if !t.isLocalPackage(meta.Package) {
		ifaceName = meta.Package + "_" + meta.Name
	}
	name := "_" + ifaceName + "_literal" + t.codecFileSuffix()
	t.interfaceLiterals[lit.resolved] = name

	restore := t.onlyTypeParams(meta.TypeParams)
	defer restore()
	var typeParams *ast.FieldList
	if len(meta.TypeParams) > 0 {
		typeParams = &ast.FieldList{}
	}
	var recvArgs []ast.Expr
	for _, tp := range meta.TypeParams {
		constraint := ast.Expr(ast.NewIdent("any"))
		if c := meta.TypeParamConstraints[tp]; c != "" {
			constraint = t.typeToExpr(t.inPackage(transpiler.ParseType(c), meta))
		}
		typeParams.List = append(typeParams.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(tp)}, Type: constraint})
		recvArgs = append(recvArgs, ast.NewIdent(tp))
	}
	recvType := &ast.StarExpr{X: withTypeArgs(ast.NewIdent(name), recvArgs)}
	fields := &ast.FieldList{}
	var decls []ast.Decl
	if t.emitLineMarkers() {
		decls = append(decls, lineMarkerDecl(line))
	}
	decls = append(decls, &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
		Name:       ast.NewIdent(name),
		TypeParams: typeParams,
		Type:       &ast.StructType{Fields: fields},
	}}})
	for _, m := range lit.methods {
		mm := meta.Methods[m]
		ft := interfaceMethodFuncType(mm)
		fieldType := t.typeToExpr(ft).(*ast.FuncType)
		fields.List = append(fields.List, &ast.Field{Names: []*ast.Ident{ast.NewIdent(interfaceLiteralField(m))}, Type: fieldType})

		sig := &ast.FuncType{Params: &ast.FieldList{}, Results: fieldType.Results}
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
		call := &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("l"), Sel: ast.NewIdent(interfaceLiteralField(m))}, Args: callArgs}
		var body ast.Stmt = &ast.ExprStmt{X: call}
		if len(ft.Results) > 0 {
			body = &ast.ReturnStmt{Results: []ast.Expr{call}}
		}
		if t.emitLineMarkers() {
			decls = append(decls, lineMarkerDecl(line))
		}
		decls = append(decls, &ast.FuncDecl{
			Recv: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("l")}, Type: recvType}}},
			Name: ast.NewIdent(m),
			Type: sig,
			Body: &ast.BlockStmt{List: []ast.Stmt{body}},
		})
	}
	t.interfaceLiteralDecls = append(t.interfaceLiteralDecls, decls...)
	return name
}

// inPackage qualifies the names typ uses that meta's package declares, so a
// type written in that package (an imported interface's constraint, as its
// metadata records it) means the same here.
func (t *galaASTTransformer) inPackage(typ transpiler.Type, meta *transpiler.TypeMetadata) transpiler.Type {
	if t.isLocalPackage(meta.Package) {
		return typ
	}
	switch v := typ.(type) {
	case transpiler.BasicType:
		if !transpiler.IsPrimitiveType(v.Name) && !slices.Contains(meta.TypeParams, v.Name) && t.getTypeMeta(meta.Package+"."+v.Name) != nil {
			return transpiler.NamedType{Package: meta.Package, Name: v.Name}
		}
	case transpiler.GenericType:
		params := make([]transpiler.Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = t.inPackage(p, meta)
		}
		return transpiler.GenericType{Base: t.inPackage(v.Base, meta), Params: params}
	}
	return typ
}
