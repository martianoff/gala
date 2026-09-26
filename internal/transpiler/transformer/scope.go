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
	if t.lspVarTypes == nil {
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

func (t *galaASTTransformer) getType(name string) transpiler.Type {
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

	// 2. Search in current scope (local variables have highest priority)
	s := t.currentScope
	for s != nil {
		if typeName, ok := s.valTypes[name]; ok {
			t.traceType(nil, typeName, "scope:local:"+name)
			return typeName
		}
		s = s.parent
	}

	// 3. Use unified type resolution for type metadata lookup
	resolved := t.resolveTypeMetaName(name)
	if resolved != "" {
		result := transpiler.ParseType(resolved)
		t.traceType(nil, result, "scope:type-meta:"+name)
		return result
	}

	t.traceType(nil, transpiler.NilType{}, "scope:miss:"+name)
	return transpiler.NilType{}
}

func (t *galaASTTransformer) getValType(name string) transpiler.Type {
	s := t.currentScope
	for s != nil {
		if typeName, ok := s.valTypes[name]; ok {
			return typeName
		}
		s = s.parent
	}
	return transpiler.NilType{}
}

func (t *galaASTTransformer) isVal(name string) bool {
	s := t.currentScope
	for s != nil {
		if isImmutable, ok := s.vals[name]; ok {
			return isImmutable
		}
		s = s.parent
	}
	return false
}

func (t *galaASTTransformer) isVar(name string) bool {
	s := t.currentScope
	for s != nil {
		if isImmutable, ok := s.vals[name]; ok {
			return !isImmutable
		}
		s = s.parent
	}
	return false
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
	s := t.currentScope
	for s != nil {
		if s.mutable[name] {
			return true
		}
		// A same-named val/plain-binding in an inner scope shadows an outer
		// mutable var, so stop searching once the name is bound at all.
		if _, ok := s.valTypes[name]; ok {
			return false
		}
		s = s.parent
	}
	return false
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
	s := t.currentScope
	for s != nil {
		if s.sendable[name] {
			return true
		}
		if _, ok := s.valTypes[name]; ok {
			return false
		}
		s = s.parent
	}
	return false
}

// lookupLocalBinding returns the tracked type of a local binding (val/var/param
// or pattern/loop bind) and whether name is bound anywhere in the current scope
// chain. A name that is NOT locally bound (a top-level function, a type, a
// package, an imported symbol) returns (_, false) — the concurrency check uses
// this to ignore captures that are not local bindings.
func (t *galaASTTransformer) lookupLocalBinding(name string) (transpiler.Type, bool) {
	s := t.currentScope
	for s != nil {
		if typ, ok := s.valTypes[name]; ok {
			return typ, true
		}
		s = s.parent
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
	entry, ok := t.importManager.GetByAlias(x)
	if !ok || entry.IsDot {
		return nil
	}
	pv := t.richAST.ImportedVals[entry.Path][sel]
	if pv == nil {
		return nil
	}
	if _, shadowed := t.lookupLocalBinding(x); shadowed {
		return nil
	}
	return pv
}

// resolveBinding resolves a val/var reference written `Name` (a scoped binding,
// dot-imported ones included) or `pkg.Name` (an imported package's binding) to
// its element type and whether it is a val.
func (t *galaASTTransformer) resolveBinding(name string) (typ transpiler.Type, isVal, ok bool) {
	if x, sel, qualified := strings.Cut(name, "."); qualified {
		if pv := t.importedPackageVal(x, sel); pv != nil {
			return pv.Type, pv.IsVal, true
		}
		return transpiler.NilType{}, false, false
	}
	for s := t.currentScope; s != nil; s = s.parent {
		if immutable, bound := s.vals[name]; bound {
			return s.valTypes[name], immutable, true
		}
	}
	return transpiler.NilType{}, false, false
}

// bindingRef returns the source name (`Name` or `pkg.Name`) of the binding that
// expr reads — the reference itself, or a val's `.Get()` unwrap of it — or "".
func (t *galaASTTransformer) bindingRef(expr ast.Expr) string {
	var name string
	switch e := expr.(type) {
	case *ast.CallExpr:
		get, ok := e.Fun.(*ast.SelectorExpr)
		if !ok || get.Sel.Name != transpiler.MethodGet || len(e.Args) != 0 {
			return ""
		}
		if name = t.bindingRef(get.X); name != "" {
			if _, isVal, _ := t.resolveBinding(name); isVal {
				return name
			}
		}
		return ""
	case *ast.Ident:
		name = e.Name
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			return ""
		}
		name = x.Name + "." + e.Sel.Name
	default:
		return ""
	}
	if _, _, ok := t.resolveBinding(name); ok {
		return name
	}
	return ""
}

// bindingRead builds the Go expression reading the binding `Name` or
// `pkg.Name`, with a val unwrapped from its std.Immutable[T].
func (t *galaASTTransformer) bindingRead(name string) ast.Expr {
	if x, sel, qualified := strings.Cut(name, "."); qualified {
		return t.unwrapImmutable(&ast.SelectorExpr{X: ast.NewIdent(x), Sel: ast.NewIdent(sel)})
	}
	if t.isVal(name) {
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent(name), Sel: ast.NewIdent(transpiler.MethodGet)}}
	}
	return ast.NewIdent(name)
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
	return nil
}
