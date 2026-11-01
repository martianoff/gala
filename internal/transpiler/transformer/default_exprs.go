package transformer

import (
	"errors"
	"fmt"
	"go/ast"
	"path/filepath"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// Default values — for function and method parameters and for shorthand struct
// fields — are recorded by the analyzer as source text plus the position of
// their first token (transpiler.DefaultExpr). They are re-parsed and lowered at
// every call or construction site that omits the argument, so a default like
// `time.Now()` is evaluated per call, and so a default declared in one package
// can be lowered in another.

// defaultSource is one declared default value together with what lowering it
// needs to know about its declaration.
type defaultSource struct {
	transpiler.DefaultExpr
	file        string                     // declaring source file; "" when unknown
	pkg         string                     // declaring package; names it borrows from there are qualified at a use site in another package
	declared    transpiler.Type            // the parameter's or field's declared type, type arguments substituted; nil when unknown
	typeParams  []string                   // type parameters of the declaration; a declared type still mentioning one is not threaded
	typeArgs    map[string]transpiler.Type // the use site's type arguments for typeParams (see bindDefaultTypeParams)
	carried     []string                   // the use site's type parameters the type arguments carry into declared (see carriedTypeParams)
	unspellable []string                   // those of carried the default's source must not spell (see substituteDeclared)

	// A method parameter's default may use the method's receiver. It is
	// lowered with recv bound to recvType, as in the method body, and recvExpr
	// — the call-site receiver — is then put in its place.
	recv     string
	recvType transpiler.Type
	recvExpr ast.Expr
}

// defaultLowering is set on the transformer while a declared default is being
// lowered at a use site. A default is lowered once per use site, but it is ONE
// declaration: nothing it binds (its lambda parameters, a method's receiver)
// is recorded as a variable of the enclosing function, and its lambda
// parameter hints are recorded once, from the declaration (see
// recordDefaultLambdaHints). A foreign default's tokens carry another file's
// positions, so no line markers are emitted for them either.
type defaultLowering struct {
	pkg     string // declaring package: bare function names resolve there (functionByName); they are qualified once the whole default is lowered
	file    string // declaring source file, where its text is parsed (parseExprAt); "" when unknown
	foreign bool   // declared in a file other than the one being transformed
	// useScope is the use site's scope. Its bindings, and those of every
	// scope around it, were not in scope where the default was written, so
	// they do not shadow the functions the default names (see shadowingScope).
	useScope *scope
	// emitTypeParams is the type parameters in scope where the lowered
	// default is emitted (see shadowedByUse).
	emitTypeParams map[string]bool
}

// shadowedByUse returns a package-level val, var, function or type — or a
// dot-imported one — that the lowered default expr reads unqualified and one
// of the use site's type parameters shadows, or "". It runs in the default's
// own scope, where such a name still resolves to its declaration.
//
// The carried type parameters (see carriedTypeParams) are no such reads: the
// lowered default spells them as the type parameters they are.
func (t *galaASTTransformer) shadowedByUse(expr ast.Expr, useSite map[string]bool, carried []string) string {
	if len(useSite) == 0 {
		return ""
	}
	bound := boundNames(expr)
	found := ""
	ast.Inspect(&ast.ParenExpr{X: expr}, func(n ast.Node) bool {
		for _, slot := range referenceSlots(n) {
			id, ok := (*slot).(*ast.Ident)
			if !ok || found != "" || !useSite[id.Name] || bound[id.Name] || slices.Contains(carried, id.Name) {
				continue
			}
			if t.isTopLevelBinding(id.Name) || t.getFunction(id.Name) != nil || t.resolveTypeMetaName(id.Name) != "" {
				found = id.Name
			}
		}
		return found == ""
	})
	return found
}

// substituteDeclared sets src's declared type to declared with the type
// arguments of subst substituted, and records those arguments and the use
// site's type parameters they carry into it (see carriedTypeParams).
func (t *galaASTTransformer) substituteDeclared(src *defaultSource, declared transpiler.Type, subst map[string]transpiler.Type) {
	src.typeArgs = subst
	src.declared = t.substituteInType(declared, subst)
	src.carried = t.carriedTypeParams(src.declared, subst)
	for _, name := range src.carried {
		// A default may spell its own type parameter that the call binds to
		// the use site's type parameter of the same name: in the generated Go
		// the name means that argument.
		if arg, ok := subst[name]; ok && arg.String() == name && slices.Contains(src.typeParams, name) {
			continue
		}
		src.unspellable = append(src.unspellable, name)
	}
}

// carriedTypeParams returns, sorted, the use site's type parameters that the
// type arguments of subst carry into a default's declared type. The default
// is lowered against them — `(a) => a` for a `func(A) A` parameter, at a call
// inside `func g[Some any]` that binds A to Some, is `func(a Some) Some` — so
// they are bound while it is, alongside its own declaration's.
func (t *galaASTTransformer) carriedTypeParams(declared transpiler.Type, subst map[string]transpiler.Type) []string {
	if len(t.activeTypeParams) == 0 {
		return nil
	}
	inArgs := map[string]bool{}
	for _, arg := range subst {
		typeNameMatches(arg, func(name string) bool {
			inArgs[name] = t.activeTypeParams[name]
			return false
		})
	}
	var carried []string
	typeNameMatches(declared, func(name string) bool {
		if inArgs[name] && !slices.Contains(carried, name) {
			carried = append(carried, name)
		}
		return false
	})
	slices.Sort(carried)
	return carried
}

// spellsAny returns the first of names that the default's source refers to —
// as a value or as a type, not as a member, label or binding — or "".
func spellsAny(e exprSrc, names []string) string {
	if len(names) == 0 {
		return ""
	}
	if id := firstReference(e, func(id srcNode) bool { return slices.Contains(names, id.text()) }); id.ok() {
		return id.text()
	}
	return ""
}

// firstReference returns the first identifier of e that refers to a name —
// as a value or as a type, not as a member, label or binding — and that match
// accepts, or no node.
func firstReference(e exprSrc, match func(srcNode) bool) srcNode {
	if !e.node.Ok() {
		return srcNode{}
	}
	var found native.GalaNodeRef
	e.node.Walk(func(n native.GalaNodeRef) bool {
		if found.Ok() {
			return false
		}
		if n.Kind() == native.GalaKIND_identifier && isReferenceNative(n) && match(srcNode{node: n}) {
			found = n
		}
		return true
	})
	return srcNode{node: found}
}

// isReferenceNative reports whether id, an identifier node, refers to a
// name, as a value or as a type (see firstReference).
func isReferenceNative(id native.GalaNodeRef) bool {
	parent := id.Parent()
	if !parent.Ok() {
		return false
	}
	switch parent.Kind() {
	case native.GalaKIND_primary:
		return true
	case native.GalaKIND_qualifiedIdentifier:
		return native.QualifiedIdentifierNode{GalaNodeRef: parent}.Identifier(0).GalaNodeRef == id
	case native.GalaKIND_parameter:
		// `func(Some) int`: an unnamed parameter's type.
		return !native.ParameterNode{GalaNodeRef: parent}.Type_().Ok() && isFuncTypeSignatureNative(parent)
	}
	return false
}

// isQualifier reports whether id, a reference, is followed by a member
// selection: `pb` in `pb.Same()` or in `pb.Point`.
func isQualifier(id srcNode) bool {
	return id.node.Ok() && isQualifierNative(id.node)
}

// isQualifierNative is isQualifier of id, an identifier node.
func isQualifierNative(id native.GalaNodeRef) bool {
	parent := id.Parent()
	if !parent.Ok() {
		return false
	}
	switch parent.Kind() {
	case native.GalaKIND_qualifiedIdentifier:
		return native.QualifiedIdentifierNode{GalaNodeRef: parent}.Identifier(1).Ok()
	case native.GalaKIND_primary:
		primaryExpr := parent.Parent()
		if !primaryExpr.Ok() || primaryExpr.Kind() != native.GalaKIND_primaryExpr {
			return false
		}
		postfix := primaryExpr.Parent()
		if !postfix.Ok() || postfix.Kind() != native.GalaKIND_postfixExpr {
			return false
		}
		suffix, ok := firstNativeKid(postfix, native.GalaKIND_postfixSuffix)
		return ok && native.PostfixSuffixNode{GalaNodeRef: suffix}.Identifier().Ok()
	}
	return false
}

// outOfLexicalScope reports a name that a default from another file reads
// but only this file's imports resolve. A default is resolved where it was
// written (see WithForeignQualifiers), so such a name is undefined there; the
// generated Go would compile through this file's import and silently take
// this file's meaning instead. The analyzer checks a default against its own
// file's imports where it is declared, except for what it cannot check there,
// such as the constructor a `case` pattern names; this catches the rest.
//
// expr is the lowered and qualified default, nil when lowering it failed; its
// bindings are what the source binds. A bare name of the source that nothing
// in the default's scope declares and one of this file's dot imports does is
// such a name; when lowering failed, it is what it failed on (a `case` naming
// a constructor out of scope, say), and is reported instead of the failure. A
// qualifier of the source that the default's file does not bind, and this
// file binds to a package, is read through this file's import. Each is
// reported at its own token, in the default's file.
func (t *galaASTTransformer) outOfLexicalScope(e exprSrc, expr ast.Expr, src defaultSource) error {
	if !t.lexicallyScoped(src) || t.importManager == nil || !e.ok() {
		return nil
	}
	var bound map[string]bool // nil when lowering failed: no bindings known
	if expr != nil {
		bound = boundNames(expr)
	}
	free := func(name string) bool {
		return !bound[name] && !slices.Contains(src.carried, name) && !slices.Contains(src.typeParams, name) && t.shadowingScope(name) == nil
	}
	if at, path := t.bareOutsideScope(e, src, free); at.ok() {
		name := at.text()
		return lexicalScopeError(at, name, fmt.Sprintf(
			"%s is declared in %q, which the file using this default dot-imports but the default's own file does not; a default value resolves its names where it is written, so add `import . %q` to the default's file",
			name, path, path))
	}
	// A qualifier resolves through this file's names as a fallback (see
	// WithForeignQualifiers), so it never fails lowering: it is checked on the
	// lowered default, whose bindings bound holds.
	if expr == nil {
		return nil
	}
	if at, path := t.qualifierOutsideScope(e, src, free); at.ok() {
		name := at.text()
		return lexicalScopeError(at, name, fmt.Sprintf(
			"%s names %q in the file using this default but nothing in the default's own file; a default value resolves its names where it is written, so add `import %q` to the default's file",
			name, path, path))
	}
	return nil
}

// lexicalScopeError is the GALA-E0023 for name, out of a default's scope, at
// its token at in the default's source.
func lexicalScopeError(at srcNode, name, hint string) error {
	line, col := at.start()
	return galaerr.NewCodedSemanticError(galaerr.CodeUndefinedVariable, line, col, "undefined: "+name, hint)
}

// bareOutsideScope finds, for outOfLexicalScope, the token of a bare, free,
// exported name of the default's source that nothing in the default's scope
// declares and one of this file's dot imports — a GALA package the default's
// file does not dot-import — does, and that package's path; nil when there is
// none. Only the source is read: the lowered default also holds names spelled
// for this file (a declared type, through this file's dot import), which the
// default never wrote. A dot import brings in exported names only.
func (t *galaASTTransformer) bareOutsideScope(e exprSrc, src defaultSource, free func(string) bool) (srcNode, string) {
	var outside []*ImportEntry
	for _, e := range t.importManager.dotImports {
		if _, own := src.Scope.DotImports[e.Path]; !own && e.Path != src.Scope.PkgPath && t.galaPkgPaths[e.Path] {
			outside = append(outside, e)
		}
	}
	if len(outside) == 0 {
		return srcNode{}, ""
	}
	path := ""
	prelude := registry.Global.PreludePackages()
	at := firstReference(e, func(id srcNode) bool {
		n := id.text()
		if isQualifier(id) || !ast.IsExported(n) || !free(n) || t.declaredInDefaultScope(n, src, prelude) {
			return false
		}
		for _, e := range outside {
			if t.importDeclares(e.Path, e.PkgName, n) {
				path = e.Path
				return true
			}
		}
		return false
	})
	return at, path
}

// qualifierOutsideScope finds, for outOfLexicalScope, the token of a
// qualifier of the default's source that its file does not bind and this file
// binds to an import it wrote, and that import's path; nil when there is none.
func (t *galaASTTransformer) qualifierOutsideScope(e exprSrc, src defaultSource, free func(string) bool) (srcNode, string) {
	path := ""
	at := firstReference(e, func(id srcNode) bool {
		q := id.text()
		if _, own := src.Scope.Imports[q]; own || !isQualifier(id) || !free(q) {
			return false
		}
		entry, ok := t.importManager.GetByAlias(q)
		if !ok || entry.Implicit() || entry.IsDot {
			return false
		}
		path = entry.Path
		return true
	})
	return at, path
}

// declaredInDefaultScope reports whether name is declared where the default
// src was written: by its package, a package its file dot-imports, or the
// prelude.
func (t *galaASTTransformer) declaredInDefaultScope(name string, src defaultSource, prelude []*registry.PackageInfo) bool {
	if t.packageDeclares(src.pkg, name) || t.functions[name] != nil || t.typeMetas[name] != nil {
		return true
	}
	for path, pkgName := range src.Scope.DotImports {
		if t.importDeclares(path, pkgName, name) {
			return true
		}
	}
	for _, pkg := range prelude {
		if t.importDeclares(pkg.ImportPath, pkg.Name, name) {
			return true
		}
	}
	return false
}

// transformDefaultExpr lowers a default value at a call or construction site.
//
// The declared type is the default's expected type, exactly as it is for an
// argument passed explicitly: a lambda default takes its parameter and result
// types from it — `OnOne func(int) int = (a) => a + 1` lowers as
// `Hooks(OnOne = (a) => a + 1)` would — and `nil` stays `nil`.
//
// A default declared in another package is lowered in THIS package's scope, so
// the names it borrows from its own package are qualified afterwards (see
// qualifyDefaultExpr). useLine/useCol locate the call or construction: a
// diagnostic from a default declared in another file is attributed to that
// file, or to the use site when the declaring file is unknown.
func (t *galaASTTransformer) transformDefaultExpr(src defaultSource, useLine, useCol int) (ast.Expr, error) {
	local := src.Pos.Line > 0 && src.file != "" && t.filePath != "" && filepath.Clean(src.file) == filepath.Clean(t.filePath)
	prev := t.loweringDefault
	// The type parameters in scope where the lowered Go is emitted: a default
	// lowered inside another default's lowering is emitted where that one is.
	useSite := t.activeTypeParams
	if prev != nil {
		useSite = prev.emitTypeParams
	}
	t.loweringDefault = &defaultLowering{pkg: src.pkg, file: src.file, foreign: !local, useScope: t.currentScope, emitTypeParams: useSite}
	defer func() { t.loweringDefault = prev }()
	// The default is lowered in its own declaration's scope: the type
	// parameters bound at the use site do not shadow the names it reads. In
	// the generated Go they do, so a name the default reads unqualified that a
	// use-site type parameter shadows cannot be spelled there (shadowedByUse).
	// The use-site type parameters its declared type carries are the
	// exception: they are bound, and the default must not read their names.
	defer t.onlyTypeParams(append(slices.Clip(src.typeParams), src.carried...))()
	if foreignPkg := t.loweringForeignDefault(); foreignPkg || t.lexicallyScoped(src) {
		// A foreign default reads its own package's vals and vars by bare
		// name. They are bound here, above the use site's scopes (whose
		// locals were not in scope where the default was written), so the
		// default reads them as its package does — a val through its
		// `.Get()` unwrap, with its type — and qualifyDefaultExpr then
		// qualifies them like the package's functions and types. The vals
		// of the packages its file dot-imports are in scope there too, below
		// the package's own; for a default from another file of this
		// package, only those.
		t.pushScope()
		defer t.popScope()
		register := func(vals map[string]*transpiler.PackageValMetadata) {
			for name, meta := range vals {
				if _, bound := t.currentScope.vals[name]; !bound && meta != nil {
					t.registerPackageVal(name, meta)
				}
			}
		}
		if foreignPkg {
			register(t.declaringPackageVals(src))
		}
		if src.Scope != nil {
			for path := range src.Scope.DotImports {
				register(t.importedVals(path))
			}
		}
	}
	if src.recv != "" {
		t.pushScope()
		defer t.popScope()
		_, pointer := src.recvType.(transpiler.PointerType)
		t.addReceiver(src.recv, src.recvType, pointer)
	}

	// Reported at the use site, in the file being transformed, wherever the
	// default was declared.
	shadowed := func(name string) error {
		err := galaerr.NewSemanticErrorAt(useLine, useCol, fmt.Sprintf(
			"the default value of a parameter reads '%s', which the type parameter '%s' of the enclosing declaration shadows here; pass the argument explicitly or rename the type parameter",
			name, name))
		err.FilePath = t.filePath
		return err
	}
	// Parsed once per default per file (parseExprAt): lowering is per use
	// site, but the parse tree is the same at every one of them.
	def, err := t.parseExprAt(src.Text, src.Pos.Line, src.Pos.Column, "default value")
	if err == nil {
		if name := spellsAny(def, src.unspellable); name != "" {
			err = shadowed(name)
		}
	}
	var expr ast.Expr
	if err == nil {
		expr, err = t.lowerDefault(def, src)
		if err != nil {
			// What it failed on may be a name only this file's imports
			// declare: report that, at the name.
			if scopeErr := t.outOfLexicalScope(def, nil, src); scopeErr != nil {
				err = scopeErr
			}
		}
	}
	if err == nil {
		expr, err = t.qualifyDefaultExpr(expr, src)
	}
	if err == nil {
		err = t.outOfLexicalScope(def, expr, src)
	}
	if err == nil {
		if name := t.shadowedByUse(expr, useSite, src.carried); name != "" {
			err = shadowed(name)
		}
	}
	if err != nil {
		if !local {
			err = placeForeignDefaultError(err, src, useLine, useCol)
		}
		return nil, err
	}
	expr, err = t.bindDefaultTypeParams(expr, src, useLine, useCol)
	if err != nil {
		return nil, err
	}
	if src.recv != "" {
		expr = replaceReceiver(expr, src.recv, src.recvExpr)
	}
	return expr, nil
}

// lowerDefault transforms a default's parse tree against its declared type.
// While a default from another file is lowered, the imports of the file that
// wrote it are in scope in place of this file's (see WithForeignQualifiers):
// `strings.Repeat` types as Go's function even where this file has no
// `strings`, or binds the name to another package, and `EmptyArray` resolves
// through that file's dot import of collection_immutable, never through one of
// this file's. Their names are respelled with this file's afterwards (see
// qualifyDefaultExpr).
func (t *galaASTTransformer) lowerDefault(def exprSrc, src defaultSource) (ast.Expr, error) {
	if t.lexicallyScoped(src) && t.importManager != nil {
		defer t.importManager.WithForeignQualifiers(src.Scope.Imports, src.Scope.DotImports)()
	}
	return t.transformWithDeclaredType(def, src.declared, src.unboundTypeParams())
}

// declaringPackageVals returns the package-level vals and vars (exported ones:
// the only ones that cross a package boundary) of the package declaring a
// default from another package: found by its recorded import path, or else by
// this file's import of the package's name. nil when it has none.
func (t *galaASTTransformer) declaringPackageVals(src defaultSource) map[string]*transpiler.PackageValMetadata {
	if src.Scope != nil && src.Scope.PkgPath != "" {
		return t.importedVals(src.Scope.PkgPath)
	}
	if t.importManager == nil {
		return nil
	}
	entry, ok := t.importManager.Qualifier(src.pkg)
	if !ok {
		return nil
	}
	return t.importedVals(entry.Path)
}

// importedVals returns the exported package-level vals and vars of the GALA
// package at path, nil when it has none or is unknown here.
func (t *galaASTTransformer) importedVals(path string) map[string]*transpiler.PackageValMetadata {
	if t.richAST == nil {
		return nil
	}
	return t.richAST.ImportedVals[path]
}

// bindDefaultTypeParams puts the use site's type arguments in place of the
// declaration's own type parameters that a lowered default spells. The default
// was lowered where they are bound; the use site binds none of them, or binds
// a type parameter of its own under the same name: `None[T]()` declared on
// `func f[T any]` is `None[int]()` at a call that binds T to int. One the use
// site leaves unbound has no type to stand for, even where a type parameter of
// the same name is in scope (that one is not the callee's), and is reported
// at useLine/useCol.
//
// It runs after qualifying and the shadowing check, which the arguments are
// not subject to: they are already spelled in the use site's scope. A
// substituted argument is not rewritten again, and the call-site receiver is
// put in place only afterwards, so the use site's own code is never touched.
func (t *galaASTTransformer) bindDefaultTypeParams(expr ast.Expr, src defaultSource, useLine, useCol int) (ast.Expr, error) {
	if len(src.typeParams) == 0 {
		return expr, nil
	}
	bound := boundNames(expr)
	substituted := map[ast.Node]bool{}
	unbound := ""
	holder := &ast.ParenExpr{X: expr}
	ast.Inspect(holder, func(n ast.Node) bool {
		if substituted[n] {
			return false
		}
		for _, slot := range referenceSlots(n) {
			id, ok := (*slot).(*ast.Ident)
			if !ok || bound[id.Name] || !slices.Contains(src.typeParams, id.Name) {
				continue
			}
			arg := src.typeArgs[id.Name]
			switch {
			case transpiler.IsUnusable(arg):
				if unbound == "" {
					unbound = id.Name
				}
			case arg.String() != id.Name:
				// A fresh expression per slot: AST nodes are not shared.
				spelled := t.typeToExpr(arg)
				substituted[spelled] = true
				*slot = spelled
			}
		}
		return true
	})
	if unbound != "" {
		return nil, galaerr.NewCodedSemanticError(galaerr.CodeUninferredTypeArgument, useLine, useCol, fmt.Sprintf(
			"cannot infer type argument %s for a default value that names it; pass the argument, or the type arguments, explicitly",
			unbound), "")
	}
	return holder.X, nil
}

// recordDefaultLambdaHints records the LSP inlay hints for a lambda default's
// unannotated parameters, from the declaration: def is the default as its
// parameter declares it, and each lambda parameter takes its type from the
// declared parameter type. A default is lowered once per use site that omits
// it — possibly with different type arguments — so hints recorded there would
// repeat, and could disagree, on the same parameter.
func (t *galaASTTransformer) recordDefaultLambdaHints(def exprSrc, declared transpiler.Type) {
	if t.lspVarTypes == nil || !def.ok() {
		return
	}
	lambda := t.lambdaIn(def)
	if !lambda.ok() {
		return
	}
	params := lambda.params(t)
	if len(params) == 0 {
		return
	}
	_, expected, ok := t.lambdaExpectation(declared)
	if !ok {
		return
	}
	for i, p := range params {
		if p.typ.ok() || !p.nameAt.ok() || i >= len(expected) || transpiler.IsUnusableOrAny(expected[i]) {
			continue
		}
		line, col := p.nameAt.start()
		t.lspLambdaParamHints = append(t.lspLambdaParamHints, transpiler.LambdaParamHint{
			Line:   line,
			Column: col,
			Name:   p.name,
			Type:   expected[i],
		})
	}
}

// placeForeignDefaultError locates a diagnostic about a default value declared
// outside the file being transformed. With the declaring file known it names
// that file (and, for an error that carries no position of its own, the
// default's first token); otherwise it falls back to the use site.
func placeForeignDefaultError(err error, src defaultSource, useLine, useCol int) error {
	var se *galaerr.SemanticError
	if !errors.As(err, &se) || se.FilePath != "" {
		return err
	}
	if src.file != "" && src.Pos.Line > 0 {
		se.FilePath = src.file
		if se.Line == 0 {
			se.Line, se.Column = src.Pos.Line, src.Pos.Column
		}
	} else {
		se.Line, se.Column = useLine, useCol
	}
	return err
}

// unboundTypeParams is the declaration's type parameters the use site binds
// to no type argument. One it binds, even to a use-site type parameter of the
// same name (`T` of `func mk[T any]`), is a type the declared type may carry.
func (src defaultSource) unboundTypeParams() []string {
	var out []string
	for _, name := range src.typeParams {
		if _, ok := src.typeArgs[name]; !ok {
			out = append(out, name)
		}
	}
	return out
}

// transformWithDeclaredType lowers an expression that stands in a slot of a
// declared type. As in a typed `val` initializer, an unannotated lambda
// parameter that the type does not cover is an error rather than `any`.
//
// A bare `nil` is returned as is: it is already a value of the declared type
// (the absent function, pointer, ...), and must not be lifted into a thunk
// `func() T { return nil }` by the by-name sugar that an explicit argument to a
// zero-argument function parameter gets.
func (t *galaASTTransformer) transformWithDeclaredType(e exprSrc, declared transpiler.Type, typeParams []string) (ast.Expr, error) {
	if declared != nil {
		if inner, isSendable := transpiler.UnwrapSendable(declared); isSendable {
			declared = inner
		}
	}
	if transpiler.IsUnusable(declared) || typeMentionsTypeParam(declared, typeParams) {
		return t.lowerExpr(e)
	}
	if e.isNil() {
		return ast.NewIdent("nil"), nil
	}
	return t.transformArgument(e, typedSlot(declared), true)
}
