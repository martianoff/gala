package transformer

import (
	"go/ast"
	"martianoff/gala/internal/transpiler"
	"strings"
)

type scope struct {
	vals     map[string]bool
	valTypes map[string]transpiler.Type
	// mutable records names that are genuinely reassignable `var` bindings —
	// only real `var` declarations and `var`-marked parameters. It intentionally
	// does NOT include the many val-by-semantics bindings that also route through
	// addVar (plain parameters, match/pattern binds, lambda params, loop vars),
	// so the concurrency capture-safety check can tell a reassignment race apart
	// from an immutable binding. See isMutableVar.
	mutable  map[string]bool
	// sendable records names whose DECLARED type is the `Sendable[F]` boundary
	// marker. The marker is transparently unwrapped by transformType, so the
	// type stored in valTypes is the bare `F` (e.g. a plain `func() int`); this
	// set is how the transformer remembers that the binding was declared
	// Sendable. The concurrency capture-safety check consults it to accept a
	// forwarded function value: a `Sendable`-typed capture is a caller-vouched
	// safe closure (the `Send`-style bound), so it may cross a further boundary.
	sendable map[string]bool
	parent   *scope
}

func (t *galaASTTransformer) pushScope() {
	t.currentScope = &scope{
		vals:     make(map[string]bool),
		valTypes: make(map[string]transpiler.Type),
		mutable:  make(map[string]bool),
		sendable: make(map[string]bool),
		parent:   t.currentScope,
	}
}

func (t *galaASTTransformer) popScope() {
	if t.currentScope != nil {
		t.currentScope = t.currentScope.parent
	}
}

func (t *galaASTTransformer) addVal(name string, typeName transpiler.Type) {
	if t.currentScope != nil {
		t.currentScope.vals[name] = true
		t.currentScope.valTypes[name] = typeName
	}
	t.recordLSPVarType(name, typeName)
}

func (t *galaASTTransformer) addVar(name string, typeName transpiler.Type) {
	if t.currentScope != nil {
		t.currentScope.vals[name] = false
		t.currentScope.valTypes[name] = typeName
	}
	t.recordLSPVarType(name, typeName)
}

func (t *galaASTTransformer) recordLSPVarType(name string, typeName transpiler.Type) {
	// A default lowered at a use site binds names of its own declaration (its
	// lambda parameters, a method's receiver), not locals of the function it is
	// lowered into; recording them would overwrite that function's real locals.
	if t.lspVarTypes == nil || t.loweringDefault != nil {
		return
	}
	key := name
	if t.lspCurrentFunc != "" {
		key = t.lspCurrentFunc + "." + name
	}
	if transpiler.IsUnusable(typeName) {
		// Record the BINDING even when its type is unknown, but never over a
		// type that did resolve.
		//
		// Consumers ask this map two different questions — what type does this
		// name have, and is this name a local at all — and a name missing from
		// it answers the second one "no". That is how a local whose type could
		// not be inferred came to be documented as the package-level val it
		// shadows. NilType renders as the empty string, which every consumer of
		// the first question already treats as "no hint".
		if _, seen := t.lspVarTypes[key]; !seen {
			t.lspVarTypes[key] = transpiler.NilType{}
		}
		return
	}
	t.lspVarTypes[key] = typeName
}

// getType resolves name as an EXPRESSION: a local binding (val, var,
// parameter, pattern bind) shadows a type of the same name, and the binding's
// type is returned. Callers that hold a name in TYPE position — a type
// annotation, a type argument, a pattern's type, a receiver type — must use
// lookupTypeName instead, or a binding that happens to share the type's name
// (`struct Spec(Align Option[Align])`, `func f(Style Style)`) answers for the
// type.
func (t *galaASTTransformer) getType(name string) transpiler.Type {
	if !strings.Contains(name, ".") {
		// Local variables have the highest priority in expression position.
		for s := t.currentScope; s != nil; s = s.parent {
			if typeName, ok := s.valTypes[name]; ok {
				t.traceType(nil, typeName, "scope:local:"+name)
				return typeName
			}
		}
	}
	return t.lookupTypeName(name)
}

// lookupTypeName resolves name in the TYPE namespace only: a (possibly
// package-qualified) type known to the type metadata. Local bindings are never
// consulted — in Go, as in GALA, a type name in type position cannot be
// shadowed by a value that merely shares the name within the same declaration
// (a struct field, a parameter).
func (t *galaASTTransformer) lookupTypeName(name string) transpiler.Type {
	// 1. If name already has a dot, it might be pkg.Type - resolve alias and check directly
	if strings.Contains(name, ".") {
		resolvedName := name
		parts := strings.Split(name, ".")
		if actual, ok := t.importManager.ResolveAlias(parts[0]); ok {
			resolvedName = actual + "." + parts[1]
		}
		if _, ok := t.typeMetas[resolvedName]; ok {
			result := transpiler.ParseType(resolvedName)
			t.traceType(nil, result, "scope:qualified:"+name)
			return result
		}
		// If it has a dot but not found in metas, don't fall through to other searches
		t.traceType(nil, transpiler.NilType{}, "scope:qualified-miss:"+name)
		return transpiler.NilType{}
	}

	// 2. Use unified type resolution for type metadata lookup
	resolved := t.resolveTypeMetaName(name)
	if resolved != "" {
		result := transpiler.ParseType(resolved)
		t.traceType(nil, result, "scope:type-meta:"+name)
		return result
	}

	t.traceType(nil, transpiler.NilType{}, "scope:miss:"+name)
	return transpiler.NilType{}
}

// bindingScope returns the innermost scope that binds name, or nil. Every
// binding is recorded in vals; valTypes may lack an entry when the binding's
// type is unknown (e.g. a match binding over an uninferable scrutinee).
func (t *galaASTTransformer) bindingScope(name string) *scope {
	for s := t.currentScope; s != nil; s = s.parent {
		if _, ok := s.vals[name]; ok {
			return s
		}
	}
	return nil
}

// scopeLookup resolves name in the scope chain: its tracked type (NilType when
// none was recorded), whether it is a val, and whether it is bound at all.
func (t *galaASTTransformer) scopeLookup(name string) (typ transpiler.Type, isVal, bound bool) {
	s := t.bindingScope(name)
	if s == nil {
		return transpiler.NilType{}, false, false
	}
	if typ = s.valTypes[name]; typ == nil {
		typ = transpiler.NilType{}
	}
	return typ, s.vals[name], true
}

func (t *galaASTTransformer) getValType(name string) transpiler.Type {
	typ, _, _ := t.scopeLookup(name)
	return typ
}

func (t *galaASTTransformer) isVal(name string) bool {
	_, isVal, bound := t.scopeLookup(name)
	return bound && isVal
}

func (t *galaASTTransformer) isVar(name string) bool {
	_, isVal, bound := t.scopeLookup(name)
	return bound && !isVal
}

// markMutable flags name in the innermost scope as a genuinely reassignable
// `var` binding. Call it only from real `var` declaration / `var`-parameter
// sites, never from the val-by-semantics bindings that also use addVar.
func (t *galaASTTransformer) markMutable(name string) {
	if t.currentScope != nil {
		t.currentScope.mutable[name] = true
	}
}

// isMutableVar reports whether name resolves to a genuinely reassignable `var`
// binding in the current scope chain. Plain parameters and pattern/loop binds
// (which are immutable by GALA semantics even though they live in the `var`
// bucket) return false.
func (t *galaASTTransformer) isMutableVar(name string) bool {
	// A same-named val/plain-binding in an inner scope shadows an outer
	// mutable var, so only the innermost binding counts.
	s := t.bindingScope(name)
	return s != nil && s.mutable[name]
}

// markSendable flags name in the innermost scope as a binding whose declared
// type is the `Sendable[F]` boundary marker. Call it right after the binding is
// added (addVal/addVar) at any parameter/binding site whose type annotation is
// Sendable — see typeCtxIsSendable.
func (t *galaASTTransformer) markSendable(name string) {
	if t.currentScope != nil {
		t.currentScope.sendable[name] = true
	}
}

// isSendableBinding reports whether name resolves to a binding DECLARED with a
// `Sendable[F]` type in the current scope chain. Shadowing mirrors isMutableVar:
// a same-named binding introduced in an inner scope (without the Sendable
// annotation) shadows an outer Sendable one, so the search stops at the first
// scope that binds the name at all.
func (t *galaASTTransformer) isSendableBinding(name string) bool {
	s := t.bindingScope(name)
	return s != nil && s.sendable[name]
}

// lookupLocalBinding returns the tracked type of a local binding (val/var/param
// or pattern/loop bind) and whether name is bound anywhere in the current scope
// chain. A name that is NOT locally bound (a top-level function, a type, a
// package, an imported symbol) returns (_, false) — the concurrency check uses
// this to ignore captures that are not local bindings.
//
// A binding whose type was never recorded counts as unbound here, so the
// concurrency check does not reject it for an unknown type.
func (t *galaASTTransformer) lookupLocalBinding(name string) (transpiler.Type, bool) {
	if s := t.bindingScope(name); s != nil {
		if typ, ok := s.valTypes[name]; ok && typ != nil {
			return typ, true
		}
	}
	return transpiler.NilType{}, false
}

// importedPackageVal returns the package-level binding the selector `x.sel`
// names in an imported GALA package, or nil when x is not a named import, the
// package declares no such binding, or a local binding named x shadows it.
// Called for every qualified selector, so the map lookups run before the
// scope walk.
func (t *galaASTTransformer) importedPackageVal(x, sel string) *transpiler.PackageValMetadata {
	if t.richAST == nil || len(t.richAST.ImportedVals) == 0 || t.importManager == nil {
		return nil
	}
	entry, isGala, ok := t.importForQualifier(x)
	if !ok || !isGala || entry.IsDot {
		return nil
	}
	pv := t.richAST.ImportedVals[entry.Path][sel]
	if pv == nil || t.bindingScope(x) != nil {
		return nil
	}
	return pv
}

// binding is a val/var reference resolved to its declaration. pkg is the
// import qualifier of an imported package-level binding, "" for a scoped one.
type binding struct {
	pkg, name string
	typ       transpiler.Type // element type; NilType when unknown
	isVal     bool
}

// String is the binding's source spelling: `name` or `pkg.name`.
func (b binding) String() string {
	if b.pkg == "" {
		return b.name
	}
	return b.pkg + "." + b.name
}

// lookupBinding resolves `name` from scope (dot-imported bindings included)
// when pkg is "", else `pkg.name` from the imported package's bindings.
func (t *galaASTTransformer) lookupBinding(pkg, name string) (binding, bool) {
	if pkg != "" {
		if pv := t.importedPackageVal(pkg, name); pv != nil {
			return binding{pkg: pkg, name: name, typ: pv.Type, isVal: pv.IsVal}, true
		}
		return binding{}, false
	}
	typ, isVal, bound := t.scopeLookup(name)
	return binding{name: name, typ: typ, isVal: isVal}, bound
}

// bindingRef resolves the binding expr reads: the reference itself (`name`,
// `pkg.Name`) or a val's `.Get()` unwrap of it.
func (t *galaASTTransformer) bindingRef(expr ast.Expr) (binding, bool) {
	switch e := expr.(type) {
	case *ast.CallExpr:
		// Only a `.Get()` directly on the reference is the val's unwrap; in
		// `v.Get().Get()` the outer call is a method of the unwrapped value.
		get, ok := e.Fun.(*ast.SelectorExpr)
		if !ok || get.Sel.Name != transpiler.MethodGet || len(e.Args) != 0 {
			return binding{}, false
		}
		if _, nested := get.X.(*ast.CallExpr); nested {
			return binding{}, false
		}
		b, ok := t.bindingRef(get.X)
		return b, ok && b.isVal
	case *ast.Ident:
		return t.lookupBinding("", e.Name)
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return t.lookupBinding(x.Name, e.Sel.Name)
		}
	}
	return binding{}, false
}

// bindingRead builds the Go expression reading b, with a val unwrapped from its
// std.Immutable[T].
func (t *galaASTTransformer) bindingRead(b binding) ast.Expr {
	if b.pkg != "" {
		return t.unwrapImmutable(&ast.SelectorExpr{X: ast.NewIdent(b.pkg), Sel: ast.NewIdent(b.name)})
	}
	if b.isVal {
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent(b.name), Sel: ast.NewIdent(transpiler.MethodGet)}}
	}
	return ast.NewIdent(b.name)
}

// registerPackageVal binds a package-level val/var in the current scope.
func (t *galaASTTransformer) registerPackageVal(name string, meta *transpiler.PackageValMetadata) {
	if meta.IsVal {
		t.addVal(name, meta.Type)
	} else {
		t.addVar(name, meta.Type)
	}
}

// registerDotImportedVals binds the bindings of every dot-imported GALA package
// under their bare names, after the current package's own (which win a clash).
func (t *galaASTTransformer) registerDotImportedVals() {
	if t.richAST == nil || len(t.richAST.ImportedVals) == 0 || t.currentScope == nil {
		return
	}
	for _, entry := range t.importManager.All() {
		if !entry.IsDot {
			continue
		}
		for name, meta := range t.richAST.ImportedVals[entry.Path] {
			if _, bound := t.currentScope.vals[name]; !bound && meta != nil {
				t.registerPackageVal(name, meta)
			}
		}
	}
}

func (t *galaASTTransformer) getFunction(name string) *transpiler.FunctionMetadata {
	// Use unified resolution to find the function
	resolved, found := t.resolveTypeName(name, func(n string) bool {
		_, ok := t.functions[n]
		return ok
	})
	if found {
		return t.functions[resolved]
	}
	fm, _ := t.functionByName(name)
	return fm
}

// functionByName looks up a GALA function referenced by a bare name. While a
// default declared in another package is being lowered, a bare name there is
// one of THAT package's functions: `OnClose func() int = DefaultClose`
// references lib's DefaultClose, which this package knows only as
// "lib.DefaultClose". Type queries on the lowered default (the by-name sugar's
// "is this already a function?", a call's result type) resolve it through
// here; the name itself is qualified once the whole default is lowered.
func (t *galaASTTransformer) functionByName(name string) (*transpiler.FunctionMetadata, bool) {
	if fm, ok := t.functions[name]; ok {
		return fm, true
	}
	if d := t.loweringDefault; d != nil && d.pkg != "" && d.pkg != t.packageName {
		fm, ok := t.functions[d.pkg+"."+name]
		return fm, ok
	}
	return nil, false
}
