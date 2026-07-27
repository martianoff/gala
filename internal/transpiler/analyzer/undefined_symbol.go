package analyzer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
	"martianoff/gala/internal/transpiler/transformer"
)

// ---------------------------------------------------------------------------
// Undefined-symbol check (GALA-E0023)
//
// Before this pass existed, a name that resolved to *nothing* was simply
// carried through: the analyzer produced no metadata for it, the inference
// engine fell back to an unconstrained type variable, and the transformer
// emitted a bare Go identifier. The user's first signal was either `undefined:
// x` from the Go compiler pointed at generated code, or — worse — a silently
// erased `any` in a lambda parameter whose type could only have come from the
// unresolved symbol's signature. This pass turns both into a framed GALA
// diagnostic at the identifier's own source position.
//
// WHAT THIS COVERS
//
// Every *bare identifier used in value position* — a variable read, a call
// target, a bare function reference — must resolve to something the analyzer
// knows about:
//
//   - a binding introduced by an enclosing scope: function/method/lambda
//     parameters and type parameters, method receivers, `val`/`var`, `:=`,
//     `for`/`range` variables, `use`/`bind`/`also` bindings, and every
//     identifier bound by a `match` or partial-function case pattern;
//   - a package-level declaration of the current package, including ones
//     contributed by sibling files: functions, types, sealed variants and
//     their companions, struct shorthands, type aliases, package vals/vars
//     and `embed val`s;
//   - a symbol of any package whose metadata reached this compilation —
//     GALA (`Types` / `Functions` / `CompanionObjects` / `TypeAliases`) or Go
//     (`GoTypeInfo` / `GoExports`). The `std` prelude goes through exactly
//     this lookup like any other package; there is no std special case;
//   - a package qualifier (`os` in `os.Getenv(...)`, or an import alias);
//   - the `<Type>_<Method>` function form the transformer emits for generic
//     and synthesized methods (`Array_FoldLeft`, `Some_Apply`), anchored on a
//     type the compilation knows — see isGeneratedMethodForm;
//   - a language builtin: `Println` / `Print`, the predeclared Go type names
//     used for conversions and type arguments, the loop-control and
//     predeclared-print names GALA has not classified (`break`, `continue`,
//     `println`, `print` — see galaBuiltinValueNames), and the names other
//     coded checks own — the Go builtins of GALA-E0035 and the Go statement
//     keywords of GALA-E0036 — which must keep producing their own, more
//     specific diagnostics.
//
// A name that matches none of the above is reported as GALA-E0023 at its
// `.gala` position. When the name IS declared by a GALA package on the
// module's search paths, the hint names the import to add; that association
// is discovered by reading the candidate packages' own top-level
// declarations, never from a hardcoded name list and never from a path
// substring.
//
// WHAT THIS DELIBERATELY DOES NOT COVER
//
//   - Type compatibility. This is an *existence* check only; whether
//     `add(1, "two")` type-checks is not its business.
//   - Import discipline. Resolution is deliberately permissive about *which*
//     package a name came from: any symbol present in the merged metadata
//     counts. Requiring the current file's own import is GALA-E0025's job
//     (validateExplicitImports), which is unchanged by this pass. So a name
//     visible only because a sibling file imported its package is accepted
//     here and rejected there — one concern, one code.
//   - Selectors. In `x.foo().bar`, only `x` is checked. Field and method
//     names need the receiver's type, which is inference territory.
//   - Type references (`func f(x Foo)`, `val v Foo = ...`, `Foo{}`). An
//     unresolved type in a signature already has a dedicated owner, and the
//     analyzer's type resolution is lossy enough (Go generics, constraints,
//     `map[K]V`, func types) that flagging here would produce false
//     positives. Type positions are skipped wholesale: only identifiers that
//     reach a `primary` in expression position are checked.
//   - Identifiers inside interpolated strings (`s"...$x..."`). The
//     interpolation body is a single lexer token, so those expressions never
//     reach the parse tree this walk sees.
//   - `match` / `case` *patterns*. A pattern both binds names and references
//     constructors, and telling them apart needs the scrutinee's type. Every
//     identifier in a pattern is therefore treated as a binding scoped to
//     that arm — a deliberate under-approximation that trades missed
//     detections for zero false positives.
//   - Composite-literal keys (`Point{X: 1}`), named-argument labels
//     (`f(name = 1)`) and postfix selectors, which are member names rather
//     than free identifiers.
//   - Files whose imports did not all load (see fileImportsFullyLoaded) and
//     the LSP analyzer (see galaAnalyzer.skipUndefinedCheck). In both cases
//     the symbol table is knowingly incomplete or the caller's contract is
//     best-effort, so a hard error would be worse than a missed detection.
//
// ---------------------------------------------------------------------------

// galaBuiltinValueNames are names always in scope in expression position that
// no GALA or Go package declares. Keep this to genuine language surface —
// anything a package declares must resolve through metadata instead.
var galaBuiltinValueNames = map[string]bool{
	// Auto-imported print helpers, rewritten to fmt.Println / fmt.Print by
	// the transformer (see rewriteBuiltinPrintFuncs).
	"Println": true,
	"Print":   true,
	// The blank identifier is a binding sink, never a reference.
	"_": true,
	// Loop control. The grammar has no `break` / `continue` statement, so both
	// parse as a bare identifier expression-statement and survive to the
	// generated Go — the same mechanism GALA-E0036 rejects for `defer` / `go`
	// / `goto`. Unlike those, these two are in active use (examples/for_loops,
	// json/codec, test/bench) and have no GALA-native replacement while `for`
	// loops exist, so they are accepted here. Whether they should be grammar
	// keywords, or join E0036's forbidden set, is a language decision this
	// existence check must not pre-empt.
	"break":    true,
	"continue": true,
	// Go's predeclared `println` / `print`. Like `break` above, these are not
	// GALA surface — they reach the generated Go as bare identifiers and Go
	// resolves them — but they are in use (examples/hello, examples/complex,
	// examples/with_main) and GALA-E0035 has not claimed them the way it
	// claimed `len` / `append` / `make`. Whether they should join that set is
	// that check's decision, not this one's.
	"println": true,
	"print":   true,
}

// goPredeclaredTypeNames are Go's predeclared type names. They reach
// expression position as conversions (`int(x)`) and as explicit type
// arguments (`Unfold[int, string](...)`), both of which route through
// `primary: identifier`.
var goPredeclaredTypeNames = map[string]bool{
	"bool": true, "string": true, "error": true, "any": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "byte": true, "rune": true,
	"float32": true, "float64": true,
	"complex64": true, "complex128": true,
	"comparable": true,
}

// inRepoGalaImportPrefix is the import-path prefix of packages that live in
// this module's own tree, as opposed to external GALA modules resolved
// through gala.mod. It mirrors the split the import scan in Analyze makes.
const inRepoGalaImportPrefix = "martianoff/gala/"

// hintRoot is one directory the import hint may search, together with the
// module import prefix its packages live under.
type hintRoot struct {
	dir    string
	prefix string
}

// undefScope is one lexical scope in the checker's scope chain.
type undefScope struct {
	names  map[string]bool
	parent *undefScope
}

// undefChecker walks a parsed GALA file and reports identifiers in value
// position that resolve to nothing. See the file header for the exact scope.
type undefChecker struct {
	rich *transpiler.RichAST

	scope *undefScope

	// declared indexes every symbol the merged metadata knows about, under
	// both its qualified key ("collection_immutable.Array") and its simple
	// name ("Array"). Indexing the simple name is what makes the check
	// permissive about *which* package a symbol came from — see the header's
	// note on GALA-E0025 owning import discipline.
	declared map[string]bool

	// declaredTypes holds just the simple names of known types. It backs the
	// `<Type>_<Method>` function form the transformer emits for generic and
	// synthesized methods — see isGeneratedMethodForm.
	declaredTypes map[string]bool

	// qualifiers holds names usable as a package qualifier (`pkg.Symbol`):
	// GALA package names, import aliases, and the trailing segment of this
	// file's Go import paths.
	qualifiers map[string]bool

	// hintRoots are searched (only when an error is already being emitted)
	// for a GALA package that declares the unresolved name.
	hintRoots []hintRoot

	// importResolves reports whether an import path maps to a directory, so
	// the hint can suggest a spelling the compiler will accept.
	importResolves func(importPath, dir string) bool

	errs []*galaerr.SemanticError
	// reported dedupes by name, so one misspelling used in twenty places
	// produces one actionable error rather than twenty.
	reported map[string]bool
}

// checkUndefinedSymbols runs the existence check over `sourceFile` and returns
// the collected errors in source order.
func (a *galaAnalyzer) checkUndefinedSymbols(
	sourceFile *grammar.SourceFileContext,
	richAST *transpiler.RichAST,
	filePath string,
) []*galaerr.SemanticError {
	declared := indexDeclaredSymbols(richAST)
	for name := range a.undefinedSymbolLocalGoNames(filePath) {
		declared[name] = true
	}
	for name := range a.importedTopLevelNames(sourceFile) {
		declared[name] = true
	}
	c := &undefChecker{
		rich:          richAST,
		declared:      declared,
		declaredTypes: indexDeclaredTypeNames(richAST),
		qualifiers:     collectQualifiers(sourceFile, richAST),
		hintRoots:      a.hintRoots(),
		importResolves: a.importPathResolvesTo,
		reported:       make(map[string]bool),
	}
	c.push()
	c.bindFileLevelNames(sourceFile)
	c.walkTopLevel(sourceFile)
	c.pop()

	sort.SliceStable(c.errs, func(i, j int) bool {
		if c.errs[i].Line != c.errs[j].Line {
			return c.errs[i].Line < c.errs[j].Line
		}
		return c.errs[i].Column < c.errs[j].Column
	})
	return c.errs
}

// fileImportsFullyLoaded reports whether every import declared by this file
// actually contributed its metadata. It is the precondition for running the
// undefined-symbol check: an import whose package failed to analyze (missing
// from a search path, mid-cycle, or a transpile failure) leaves the symbol
// table without any of that package's exports, and a name the analyzer never
// saw must not be reported as one the author never defined.
//
// Two shapes make a file ineligible:
//
//   - a GALA import with no successfully-analyzed entry in analyzedPkgs, and
//   - a *dot* import of a Go package, whose unqualified exports depend on Go
//     type info that is silently unavailable when no Go SDK is on PATH.
//     Named Go imports stay eligible because their symbols are only ever
//     reachable through a qualifier, which the check accepts on sight.
func (a *galaAnalyzer) fileImportsFullyLoaded(sf *grammar.SourceFileContext) bool {
	// The implicitly dot-imported prelude is subject to the same rule: it
	// never appears in the import list, so check it explicitly. (This is not
	// a special case for std — it is the one import the language adds on the
	// author's behalf, and it must be as loaded as any they wrote.)
	if entry, present := a.analyzedPkgs[registry.StdImportPath]; !present || entry == nil {
		return false
	}
	for _, impDecl := range sf.AllImportDeclaration() {
		ctx, ok := impDecl.(*grammar.ImportDeclarationContext)
		if !ok {
			continue
		}
		for _, spec := range ctx.AllImportSpec() {
			s, ok := spec.(*grammar.ImportSpecContext)
			if !ok || s.STRING() == nil {
				continue
			}
			path := strings.Trim(s.STRING().GetText(), "\"")
			// Same in-repo/external split the import scan in Analyze uses.
			isGala := strings.HasPrefix(path, inRepoGalaImportPrefix) ||
				(a.resolver != nil && a.resolver.IsGalaPackage(path))
			if isGala {
				if entry, present := a.analyzedPkgs[path]; !present || entry == nil {
					return false
				}
				continue
			}
			// Dot import of a Go package: `s.Identifier() == nil` with an
			// extra child is the '.' form (see the dot-import scan in
			// Analyze).
			if s.Identifier() == nil && s.GetChildCount() > 1 {
				return false
			}
		}
	}
	return true
}

// importedTopLevelNames returns every name declared at the top level of the
// GALA packages this file imports, read straight from those packages' sources.
//
// It is a deliberate safety net rather than the primary resolution path. The
// merged metadata is the primary path, but it is not a complete record of what
// a package exports: package-level `val`/`var` declarations, for instance,
// live in RichAST.PackageVals, which Merge does not carry across a package
// boundary (it exists for the current package's Immutable-unwrap decisions, and
// widening it would change how the transformer pre-registers names). Rather
// than reshape that map — and risk changing generated code for a check that
// only needs to know whether a name exists — the declarations are read
// directly. Any future export kind the metadata does not model is covered by
// the same net.
//
// Names are collected only for the *existence* test; nothing here influences
// type resolution or code generation. Results are cached per package directory:
// analyzePackage has already parsed these files, so parseFileCached usually
// hits, and a package's sources do not change during a build.
func (a *galaAnalyzer) importedTopLevelNames(sf *grammar.SourceFileContext) map[string]bool {
	out := make(map[string]bool)
	for _, impDecl := range sf.AllImportDeclaration() {
		ctx, ok := impDecl.(*grammar.ImportDeclarationContext)
		if !ok {
			continue
		}
		for _, spec := range ctx.AllImportSpec() {
			s, ok := spec.(*grammar.ImportSpecContext)
			if !ok || s.STRING() == nil {
				continue
			}
			path := strings.Trim(s.STRING().GetText(), "\"")
			relPath := path
			isInRepo := strings.HasPrefix(path, inRepoGalaImportPrefix)
			if isInRepo {
				relPath = strings.TrimPrefix(path, inRepoGalaImportPrefix)
			} else if a.resolver == nil || !a.resolver.IsGalaPackage(path) {
				continue // Go package — its symbols come from GoTypeInfo
			}
			for name := range a.packageTopLevelNames(relPath) {
				out[name] = true
			}
		}
	}
	// The implicit prelude is subject to the same treatment as any written
	// import — it resolves through the ordinary package path, not a bypass.
	for name := range a.packageTopLevelNames(registry.StdPackageName) {
		out[name] = true
	}
	return out
}

// packageTopLevelNames parses the .gala sources of the package at `relPath`
// and returns the names their top-level declarations introduce.
func (a *galaAnalyzer) packageTopLevelNames(relPath string) map[string]bool {
	if a.resolver == nil {
		return nil
	}
	dirPath, err := a.resolver.ResolvePackagePath(relPath)
	if err != nil || dirPath == "" {
		return nil
	}
	key := canonicalPath(dirPath)
	if cached, ok := a.importedNames[key]; ok {
		return cached
	}
	entries, rerr := os.ReadDir(dirPath)
	if rerr != nil {
		if a.importedNames == nil {
			a.importedNames = make(map[string]map[string]bool)
		}
		a.importedNames[key] = nil
		return nil
	}
	names := make(map[string]bool)
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || filepath.Ext(n) != ".gala" || strings.HasSuffix(n, "_test.gala") {
			continue
		}
		tree, _, perr := a.parseFileCached(filepath.Join(dirPath, n))
		if perr != nil || tree == nil {
			continue
		}
		collectTopLevelDeclaredNames(tree, names)
	}
	if a.importedNames == nil {
		a.importedNames = make(map[string]map[string]bool)
	}
	a.importedNames[key] = names
	return names
}

// collectTopLevelDeclaredNames records into `out` every name the file's
// top-level declarations introduce: free functions, types, struct shorthands,
// sealed types and their case variants, package vals/vars (including
// tuple-pattern destructuring), and embed bindings.
func collectTopLevelDeclaredNames(sf *grammar.SourceFileContext, out map[string]bool) {
	record := func(id grammar.IIdentifierContext) {
		if id != nil {
			out[id.GetText()] = true
		}
	}
	recordList := func(il grammar.IIdentifierListContext) {
		if il == nil {
			return
		}
		for _, id := range il.(*grammar.IdentifierListContext).AllIdentifier() {
			record(id)
		}
	}
	for _, topDecl := range sf.AllTopLevelDeclaration() {
		switch {
		case topDecl.FunctionDeclaration() != nil:
			fc := topDecl.FunctionDeclaration().(*grammar.FunctionDeclarationContext)
			if fc.Receiver() == nil {
				record(fc.Identifier())
			}
		case topDecl.TypeDeclaration() != nil:
			record(topDecl.TypeDeclaration().(*grammar.TypeDeclarationContext).Identifier())
		case topDecl.StructShorthandDeclaration() != nil:
			record(topDecl.StructShorthandDeclaration().(*grammar.StructShorthandDeclarationContext).Identifier())
		case topDecl.SealedTypeDeclaration() != nil:
			sc := topDecl.SealedTypeDeclaration().(*grammar.SealedTypeDeclarationContext)
			record(sc.Identifier())
			for _, cc := range sc.AllSealedCase() {
				record(cc.(*grammar.SealedCaseContext).Identifier())
			}
		case topDecl.ValDeclaration() != nil:
			vc := topDecl.ValDeclaration().(*grammar.ValDeclarationContext)
			recordList(vc.IdentifierList())
			if tp := vc.TuplePattern(); tp != nil {
				recordList(tp.(*grammar.TuplePatternContext).IdentifierList())
			}
		case topDecl.VarDeclaration() != nil:
			vc := topDecl.VarDeclaration().(*grammar.VarDeclarationContext)
			recordList(vc.IdentifierList())
			if tp := vc.TuplePattern(); tp != nil {
				recordList(tp.(*grammar.TuplePatternContext).IdentifierList())
			}
		case topDecl.EmbedDeclaration() != nil:
			record(topDecl.EmbedDeclaration().(*grammar.EmbedDeclarationContext).Identifier())
		}
	}
}

// undefinedSymbolLocalGoNames returns every name declared at the top level of
// the hand-written .go files sitting alongside `filePath` — unexported ones
// included.
//
// This closes a real gap in the analyzer's symbol table rather than papering
// over one. A mixed GALA+Go package may have a .gala file call an unexported
// helper declared in a .go file of the same package (json/codec.gala's
// `toBytes` comes from json/byte_utils.go). That call is legal Go once
// generated, but GoTypeInfo intentionally records only *exported* symbols,
// since those are the ones that cross a package boundary. Widening
// GoTypeInfo would leak unexported names into cross-package resolution, so
// the fuller list is collected here instead and used for existence only.
//
// Results are cached per directory: a package's .go files do not change
// during a build, and a 37-file GALA package would otherwise re-parse them
// once per file.
func (a *galaAnalyzer) undefinedSymbolLocalGoNames(filePath string) map[string]bool {
	if filePath == "" {
		return nil
	}
	dir := canonicalPath(filepath.Dir(filePath))
	if cached, ok := a.localGoNames[dir]; ok {
		return cached
	}
	names := parseLocalGoDeclNames(filepath.Dir(filePath))
	if a.localGoNames == nil {
		a.localGoNames = make(map[string]map[string]bool)
	}
	a.localGoNames[dir] = names
	return names
}

// parseLocalGoDeclNames parses `dir`'s hand-written .go files and returns the
// names their top-level declarations introduce. Generated (.gen.go) and test
// files are excluded: the former restate what the .gala sources already
// contribute, the latter are not part of the package's surface.
func parseLocalGoDeclNames(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names map[string]bool
	record := func(name string) {
		if name == "" || name == "_" {
			return
		}
		if names == nil {
			names = make(map[string]bool)
		}
		names[name] = true
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") ||
			strings.HasSuffix(n, "_test.go") || strings.HasSuffix(n, ".gen.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.SkipObjectResolution)
		if perr != nil || f == nil {
			continue
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name != nil {
					record(d.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name != nil {
							record(s.Name.Name)
						}
					case *ast.ValueSpec:
						for _, id := range s.Names {
							record(id.Name)
						}
					}
				}
			}
		}
	}
	return names
}

// importPathResolvesTo reports whether `importPath` is one the analyzer would
// resolve to `dir`. The hint uses it to pick, among the plausible spellings of
// a candidate package's import path, one that will actually work when pasted
// into the file — a directory can be reachable under the module's own prefix,
// under the GALA distribution prefix, or bare, depending on which search path
// it came from, and only the resolver knows which.
func (a *galaAnalyzer) importPathResolvesTo(importPath, dir string) bool {
	if a.resolver == nil || importPath == "" {
		return false
	}
	// Mirrors the in-repo/external split the import scan in Analyze makes.
	relPath := strings.TrimPrefix(importPath, inRepoGalaImportPrefix)
	resolved, err := a.resolver.ResolvePackagePath(relPath)
	if err != nil || resolved == "" {
		return false
	}
	return canonicalPath(resolved) == canonicalPath(dir)
}

// hintRoots returns the roots the import hint may scan, each paired with the
// module import prefix its subdirectories live under.
func (a *galaAnalyzer) hintRoots() []hintRoot {
	seen := make(map[string]bool)
	var roots []hintRoot
	add := func(dir string) {
		if dir == "" {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		roots = append(roots, hintRoot{dir: abs, prefix: modulePrefixOf(abs)})
	}
	if a.resolver != nil {
		add(a.resolver.ModuleRoot())
	}
	for _, sp := range a.searchPaths {
		add(sp)
	}
	return roots
}

// modulePrefixOf reads the module name declared by `dir`'s gala.mod (falling
// back to go.mod), which is the prefix its packages are imported under.
func modulePrefixOf(dir string) string {
	for _, name := range []string{"gala.mod", "go.mod"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := cutPrefix(strings.TrimSpace(line), "module "); ok {
				return strings.TrimSpace(rest)
			}
		}
	}
	return ""
}

// indexDeclaredSymbols flattens every symbol in the merged metadata into a
// lookup set keyed by both qualified name and simple name.
func indexDeclaredSymbols(rich *transpiler.RichAST) map[string]bool {
	out := make(map[string]bool)
	add := func(key string) {
		if key == "" {
			return
		}
		out[key] = true
		if idx := strings.LastIndex(key, "."); idx > 0 && idx+1 < len(key) {
			out[key[idx+1:]] = true
		}
	}
	for k := range rich.Functions {
		add(k)
	}
	for k := range rich.Types {
		add(k)
	}
	for k := range rich.CompanionObjects {
		add(k)
	}
	for k := range rich.TypeAliases {
		add(k)
	}
	for k := range rich.PackageVals {
		add(k)
	}
	if gi := rich.GoTypeInfo; gi != nil {
		for k := range gi.Functions {
			add(k)
		}
		for k := range gi.Types {
			add(k)
		}
		for k := range gi.Variables {
			add(k)
		}
		for k := range gi.Constants {
			add(k)
		}
		for k := range gi.TypeAliases {
			add(k)
		}
	}
	for pkg, symbols := range rich.GoExports {
		for _, s := range symbols {
			add(pkg + "." + s)
		}
	}
	return out
}

// collectQualifiers builds the set of identifiers that may legally appear on
// the left of a `pkg.Symbol` selector.
func collectQualifiers(sf *grammar.SourceFileContext, rich *transpiler.RichAST) map[string]bool {
	q := make(map[string]bool)
	q[registry.StdPackageName] = true
	if rich.PackageName != "" {
		q[rich.PackageName] = true
	}
	for _, name := range rich.Packages {
		if name != "" {
			q[name] = true
		}
	}
	for alias := range rich.ImportAliases {
		q[alias] = true
	}
	for pkg := range rich.GoExports {
		q[pkg] = true
	}
	if gi := rich.GoTypeInfo; gi != nil {
		for k := range gi.Functions {
			addQualifierOf(q, k)
		}
		for k := range gi.Types {
			addQualifierOf(q, k)
		}
		for k := range gi.Variables {
			addQualifierOf(q, k)
		}
		for k := range gi.Constants {
			addQualifierOf(q, k)
		}
		for k := range gi.TypeAliases {
			addQualifierOf(q, k)
		}
	}
	// This file's own import declarations: the alias when one is given,
	// otherwise the trailing path segment — how a Go import is referenced.
	for _, impDecl := range sf.AllImportDeclaration() {
		ctx, ok := impDecl.(*grammar.ImportDeclarationContext)
		if !ok {
			continue
		}
		for _, spec := range ctx.AllImportSpec() {
			s, ok := spec.(*grammar.ImportSpecContext)
			if !ok || s.STRING() == nil {
				continue
			}
			if alias := s.Identifier(); alias != nil {
				q[alias.GetText()] = true
				continue
			}
			path := strings.Trim(s.STRING().GetText(), "\"")
			if idx := strings.LastIndex(path, "/"); idx >= 0 {
				path = path[idx+1:]
			}
			if path != "" {
				q[path] = true
			}
		}
	}
	return q
}

// indexDeclaredTypeNames returns the simple names of every type and companion
// object the merged metadata knows about. It anchors the `<Type>_<Method>`
// function form the transformer emits — see isGeneratedMethodForm.
func indexDeclaredTypeNames(rich *transpiler.RichAST) map[string]bool {
	out := make(map[string]bool)
	for k, tm := range rich.Types {
		if tm != nil && tm.Name != "" {
			out[tm.Name] = true
			continue
		}
		out[simpleNameOf(k)] = true
	}
	for k := range rich.CompanionObjects {
		out[simpleNameOf(k)] = true
	}
	if gi := rich.GoTypeInfo; gi != nil {
		for k := range gi.Types {
			out[simpleNameOf(k)] = true
		}
	}
	return out
}

// simpleNameOf strips a "pkg." qualifier from a metadata key.
func simpleNameOf(qualified string) string {
	if idx := strings.LastIndex(qualified, "."); idx > 0 && idx+1 < len(qualified) {
		return qualified[idx+1:]
	}
	return qualified
}

func addQualifierOf(q map[string]bool, qualified string) {
	if idx := strings.LastIndex(qualified, "."); idx > 0 {
		q[qualified[:idx]] = true
	}
}

// --- scope handling ---------------------------------------------------------

func (c *undefChecker) push() {
	c.scope = &undefScope{names: make(map[string]bool), parent: c.scope}
}

func (c *undefChecker) pop() {
	if c.scope != nil {
		c.scope = c.scope.parent
	}
}

func (c *undefChecker) bind(name string) {
	if name == "" || c.scope == nil {
		return
	}
	c.scope.names[name] = true
}

func (c *undefChecker) inScope(name string) bool {
	for s := c.scope; s != nil; s = s.parent {
		if s.names[name] {
			return true
		}
	}
	return false
}

// --- resolution -------------------------------------------------------------

// resolves reports whether `name` denotes anything at all at this point in the
// walk.
func (c *undefChecker) resolves(name string) bool {
	if name == "" {
		return true
	}
	if c.inScope(name) {
		return true
	}
	if galaBuiltinValueNames[name] || goPredeclaredTypeNames[name] {
		return true
	}
	// Names owned by other coded checks keep their own, more specific
	// diagnostics: the Go builtins of GALA-E0035 and the Go statement
	// keywords of GALA-E0036 must not be downgraded to "undefined".
	if transformer.ForbiddenGoBuiltins()[name] || transformer.ForbiddenStatementKeywords()[name] {
		return true
	}
	// A bare package name in value position is not a value, but rejecting it
	// is a separate concern from this existence check.
	if c.qualifiers[name] {
		return true
	}
	if c.declared[name] {
		return true
	}
	return c.isGeneratedMethodForm(name)
}

// isGeneratedMethodForm reports whether `name` is the function form of a
// method on a known type — `Array_FoldLeft`, `Some_Apply`, `Option_Map`.
//
// Go forbids a method from introducing its own type parameters, so the
// transformer emits such methods (and the synthesized Apply / Unapply / Copy /
// Equal on structs and sealed variants) as top-level `<Type>_<Method>`
// functions, and GALA source may call that form directly. Those names exist
// only after transformation, so the analyzer's metadata has no entry for them.
//
// The test is anchored on real metadata: the part before an underscore has to
// be a type this compilation actually knows. It is deliberately not anchored
// on the suffix, because the suffix may name a method the transformer
// synthesizes rather than one the author declared — which is exactly the set
// the analyzer cannot enumerate. The cost is that a misspelling after the
// underscore (`Array_Fldleft`) is not caught here; the Go compiler still
// rejects it.
func (c *undefChecker) isGeneratedMethodForm(name string) bool {
	for i, r := range name {
		if r != '_' || i == 0 {
			continue
		}
		if c.declaredTypes[name[:i]] {
			return true
		}
	}
	return false
}

func (c *undefChecker) report(name string, tok antlr.Token) {
	if tok == nil || c.reported[name] {
		return
	}
	c.reported[name] = true
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeUndefinedVariable,
		tok.GetLine(), tok.GetColumn(),
		fmt.Sprintf("undefined: %s", name),
		c.hintFor(name),
	).WithSpan(tok.GetColumn() + len([]rune(name)))
	c.errs = append(c.errs, err)
}

// hintFor produces the actionable half of the diagnostic. When the name is
// declared by GALA packages the search paths can see, it names the import(s)
// that would bring it into scope; otherwise it falls back to generic guidance.
func (c *undefChecker) hintFor(name string) string {
	candidates := galaPackagesDeclaring(name, c.hintRoots, c.importResolves)
	switch len(candidates) {
	case 0:
		return "check the spelling, add the import that introduces this name, or declare it — " +
			"every identifier must resolve to a binding, a declaration in this package, or an imported symbol"
	case 1:
		p := candidates[0]
		return fmt.Sprintf(
			"%s is declared in the GALA package %q, which this file does not import. "+
				"Add `import . %q` to use it unqualified, or `import %q` and call it as `%s.%s`.",
			name, p.importPath, p.importPath, p.importPath, p.pkgName, name)
	default:
		var quoted []string
		for _, p := range candidates {
			quoted = append(quoted, fmt.Sprintf("%q", p.importPath))
		}
		return fmt.Sprintf(
			"%s is declared in these GALA packages, none of which this file imports: %s. "+
				"Add `import . \"<the one you want>\"` to use it unqualified, "+
				"or import it plainly and qualify the call.",
			name, strings.Join(quoted, ", "))
	}
}

// --- walking ----------------------------------------------------------------

// bindFileLevelNames registers declarations whose names are not reachable
// through richAST metadata: tuple-pattern package vals (`val (a, b) = ...`,
// which extractPackageVals deliberately skips) and `embed val` directives.
func (c *undefChecker) bindFileLevelNames(sf *grammar.SourceFileContext) {
	for _, d := range c.rich.EmbedDirectives {
		c.bind(d.VarName)
	}
	for _, topDecl := range sf.AllTopLevelDeclaration() {
		var tp grammar.ITuplePatternContext
		switch {
		case topDecl.ValDeclaration() != nil:
			tp = topDecl.ValDeclaration().(*grammar.ValDeclarationContext).TuplePattern()
		case topDecl.VarDeclaration() != nil:
			tp = topDecl.VarDeclaration().(*grammar.VarDeclarationContext).TuplePattern()
		case topDecl.EmbedDeclaration() != nil:
			ctx := topDecl.EmbedDeclaration().(*grammar.EmbedDeclarationContext)
			if ctx.Identifier() != nil {
				c.bind(ctx.Identifier().GetText())
			}
		}
		if tp != nil {
			c.bindIdentifierList(tp.(*grammar.TuplePatternContext).IdentifierList())
		}
	}
}

func (c *undefChecker) walkTopLevel(sf *grammar.SourceFileContext) {
	for _, topDecl := range sf.AllTopLevelDeclaration() {
		switch {
		case topDecl.FunctionDeclaration() != nil:
			c.walkFunctionDeclaration(topDecl.FunctionDeclaration().(*grammar.FunctionDeclarationContext))
		case topDecl.ValDeclaration() != nil:
			c.walkExpr(topDecl.ValDeclaration().(*grammar.ValDeclarationContext).ExpressionList())
		case topDecl.VarDeclaration() != nil:
			c.walkExpr(topDecl.VarDeclaration().(*grammar.VarDeclarationContext).ExpressionList())
		case topDecl.StructShorthandDeclaration() != nil:
			ctx := topDecl.StructShorthandDeclaration().(*grammar.StructShorthandDeclarationContext)
			c.walkParameterDefaults(ctx.Parameters(), ctx.TypeParameters())
		}
		// typeDeclaration / sealedTypeDeclaration / embedDeclaration hold no
		// value-position expressions this check inspects.
	}
}

// walkParameterDefaults checks the default-value expressions of a parameter
// list in a scope holding the declaration's type parameters and the parameter
// names seen so far (a default may reference an earlier parameter).
func (c *undefChecker) walkParameterDefaults(params grammar.IParametersContext, typeParams grammar.ITypeParametersContext) {
	if params == nil {
		return
	}
	c.push()
	defer c.pop()
	c.bindTypeParameters(typeParams)
	c.bindParameters(params, true)
}

func (c *undefChecker) walkFunctionDeclaration(ctx *grammar.FunctionDeclarationContext) {
	c.push()
	defer c.pop()

	c.bindTypeParameters(ctx.TypeParameters())
	if recv := ctx.Receiver(); recv != nil {
		rc := recv.(*grammar.ReceiverContext)
		if rc.Identifier() != nil {
			c.bind(rc.Identifier().GetText())
		}
		// A method's receiver type may introduce type parameters
		// (`func (s Stack[T]) Push(v T)`); bind their names so `T` resolves
		// in the signature and body.
		c.bindReceiverTypeParams(rc.Type_())
	} else if ctx.Identifier() != nil {
		// A local function may recurse; top-level ones are already
		// package-level, but nested ones are not.
		c.bind(ctx.Identifier().GetText())
	}
	if sig := ctx.Signature(); sig != nil {
		c.bindParameters(sig.(*grammar.SignatureContext).Parameters(), true)
	}
	if b := ctx.Block(); b != nil {
		c.walkBlock(b.(*grammar.BlockContext))
	} else {
		c.walkExpr(ctx.Expression())
	}
}

// bindReceiverTypeParams binds the type-parameter names a method receiver
// introduces (`Stack[T]` → `T`). Every identifier in the receiver type is
// bound, at any nesting depth, because the type parameters can sit behind a
// pointer or a nested type argument (`*List[T]`, `Pair[K, V]`) and because
// binding the type's own name alongside them is harmless — it is a
// declaration in the same package either way.
func (c *undefChecker) bindReceiverTypeParams(t grammar.ITypeContext) {
	if t == nil {
		return
	}
	for _, id := range collectIdentifiers(t) {
		c.bind(id)
	}
}

func (c *undefChecker) bindTypeParameters(tp grammar.ITypeParametersContext) {
	if tp == nil {
		return
	}
	list := tp.(*grammar.TypeParametersContext).TypeParameterList()
	if list == nil {
		return
	}
	for _, p := range list.(*grammar.TypeParameterListContext).AllTypeParameter() {
		if id := p.(*grammar.TypeParameterContext).Identifier(0); id != nil {
			c.bind(id.GetText())
		}
	}
}

// bindParameters binds each named parameter. With walkDefaults set, default
// expressions are checked as they are reached, so a default may reference an
// earlier parameter but not a later one.
func (c *undefChecker) bindParameters(params grammar.IParametersContext, walkDefaults bool) {
	if params == nil {
		return
	}
	pl := params.(*grammar.ParametersContext).ParameterList()
	if pl == nil {
		return
	}
	for _, p := range pl.(*grammar.ParameterListContext).AllParameter() {
		pc := p.(*grammar.ParameterContext)
		if walkDefaults {
			if pd := pc.ParamDefault(); pd != nil {
				c.walkExpr(pd.(*grammar.ParamDefaultContext).Expression())
			}
		}
		if pc.Identifier() != nil {
			c.bind(pc.Identifier().GetText())
		}
	}
}

func (c *undefChecker) bindIdentifierList(il grammar.IIdentifierListContext) {
	if il == nil {
		return
	}
	for _, id := range il.(*grammar.IdentifierListContext).AllIdentifier() {
		c.bind(id.GetText())
	}
}

func (c *undefChecker) walkBlock(b *grammar.BlockContext) {
	if b == nil {
		return
	}
	c.push()
	defer c.pop()
	// Hoist local function and type declarations so mutually recursive local
	// helpers resolve regardless of declaration order.
	for _, st := range b.AllStatement() {
		sc, ok := st.(*grammar.StatementContext)
		if !ok || sc.Declaration() == nil {
			continue
		}
		d := sc.Declaration().(*grammar.DeclarationContext)
		if fd := d.FunctionDeclaration(); fd != nil {
			fc := fd.(*grammar.FunctionDeclarationContext)
			if fc.Identifier() != nil && fc.Receiver() == nil {
				c.bind(fc.Identifier().GetText())
			}
		}
		if td := d.TypeDeclaration(); td != nil {
			tc := td.(*grammar.TypeDeclarationContext)
			if tc.Identifier() != nil {
				c.bind(tc.Identifier().GetText())
			}
		}
	}
	for _, st := range b.AllStatement() {
		c.walkStatement(st.(*grammar.StatementContext))
	}
}

func (c *undefChecker) walkStatement(st *grammar.StatementContext) {
	if st == nil {
		return
	}
	if d := st.Declaration(); d != nil {
		c.walkDeclaration(d.(*grammar.DeclarationContext))
		return
	}
	if r := st.ReturnStatement(); r != nil {
		c.walkExpr(r.(*grammar.ReturnStatementContext).Expression())
	}
}

func (c *undefChecker) walkDeclaration(d *grammar.DeclarationContext) {
	switch {
	case d.ValDeclaration() != nil:
		vc := d.ValDeclaration().(*grammar.ValDeclarationContext)
		c.walkExpr(vc.ExpressionList())
		c.bindIdentifierList(vc.IdentifierList())
		if tp := vc.TuplePattern(); tp != nil {
			c.bindIdentifierList(tp.(*grammar.TuplePatternContext).IdentifierList())
		}
	case d.VarDeclaration() != nil:
		vc := d.VarDeclaration().(*grammar.VarDeclarationContext)
		c.walkExpr(vc.ExpressionList())
		c.bindIdentifierList(vc.IdentifierList())
		if tp := vc.TuplePattern(); tp != nil {
			c.bindIdentifierList(tp.(*grammar.TuplePatternContext).IdentifierList())
		}
	case d.BindDeclaration() != nil:
		bc := d.BindDeclaration().(*grammar.BindDeclarationContext)
		c.walkExpr(bc.Expression())
		if bc.Identifier() != nil {
			c.bind(bc.Identifier().GetText())
		}
	case d.AlsoDeclaration() != nil:
		ac := d.AlsoDeclaration().(*grammar.AlsoDeclarationContext)
		c.walkExpr(ac.Expression())
		if ac.Identifier() != nil {
			c.bind(ac.Identifier().GetText())
		}
	case d.UseDeclaration() != nil:
		uc := d.UseDeclaration().(*grammar.UseDeclarationContext)
		c.walkExpr(uc.Expression())
		if uc.Identifier() != nil {
			c.bind(uc.Identifier().GetText())
		}
	case d.FunctionDeclaration() != nil:
		c.walkFunctionDeclaration(d.FunctionDeclaration().(*grammar.FunctionDeclarationContext))
	case d.IfStatement() != nil:
		c.walkIfStatement(d.IfStatement().(*grammar.IfStatementContext))
	case d.ForStatement() != nil:
		c.walkForStatement(d.ForStatement().(*grammar.ForStatementContext))
	case d.SimpleStatement() != nil:
		c.walkSimpleStatement(d.SimpleStatement().(*grammar.SimpleStatementContext))
	}
	// typeDeclaration / importDeclaration hold no value-position expressions.
}

func (c *undefChecker) walkIfStatement(ctx *grammar.IfStatementContext) {
	c.push()
	defer c.pop()
	if init := ctx.SimpleStatement(); init != nil {
		c.walkSimpleStatement(init.(*grammar.SimpleStatementContext))
	}
	c.walkExpr(ctx.Expression())
	for _, b := range ctx.AllBlock() {
		c.walkBlock(b.(*grammar.BlockContext))
	}
	if elseIf := ctx.IfStatement(); elseIf != nil {
		c.walkIfStatement(elseIf.(*grammar.IfStatementContext))
	}
}

func (c *undefChecker) walkForStatement(ctx *grammar.ForStatementContext) {
	c.push()
	defer c.pop()
	if fc := ctx.ForClause(); fc != nil {
		f := fc.(*grammar.ForClauseContext)
		for _, ss := range f.AllSimpleStatement() {
			c.walkSimpleStatement(ss.(*grammar.SimpleStatementContext))
		}
		c.walkExpr(f.Expression())
	}
	if rc := ctx.RangeClause(); rc != nil {
		r := rc.(*grammar.RangeClauseContext)
		c.walkExpr(r.Expression())
		c.bindIdentifierList(r.IdentifierList())
	}
	if cond := ctx.ForCondition(); cond != nil {
		c.walkExpr(cond.(*grammar.ForConditionContext).Expression())
	}
	if b := ctx.Block(); b != nil {
		c.walkBlock(b.(*grammar.BlockContext))
	}
}

func (c *undefChecker) walkSimpleStatement(ctx *grammar.SimpleStatementContext) {
	if ctx == nil {
		return
	}
	switch {
	case ctx.IncDecStmt() != nil:
		c.walkExpr(ctx.IncDecStmt().(*grammar.IncDecStmtContext).Expression())
	case ctx.Assignment() != nil:
		for _, el := range ctx.Assignment().(*grammar.AssignmentContext).AllExpressionList() {
			c.walkExpr(el)
		}
	case ctx.ShortVarDecl() != nil:
		s := ctx.ShortVarDecl().(*grammar.ShortVarDeclContext)
		c.walkExpr(s.ExpressionList())
		c.bindIdentifierList(s.IdentifierList())
	case ctx.Expression() != nil:
		c.walkExpr(ctx.Expression())
	}
}

// walkExpr is the generic expression walker. It descends the precedence chain
// generically and only takes over at nodes whose identifiers need special
// treatment.
func (c *undefChecker) walkExpr(node antlr.Tree) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	case *grammar.PostfixExprContext:
		c.walkPostfixExpr(n)
	case *grammar.LambdaExpressionContext:
		c.walkLambda(n)
	case *grammar.PartialFunctionLiteralContext:
		for _, cc := range n.AllCaseClause() {
			c.walkCaseClause(cc.(*grammar.CaseClauseContext))
		}
	case *grammar.BlockContext:
		c.walkBlock(n)
	case *grammar.ArgumentContext:
		// `argument: (identifier '=')? (lambdaExpression | pattern)` — the
		// optional leading identifier is a named-argument label, not a
		// reference.
		if lam := n.LambdaExpression(); lam != nil {
			c.walkLambda(lam.(*grammar.LambdaExpressionContext))
			return
		}
		c.walkArgumentPattern(n.Pattern())
	case *grammar.KeyedElementContext:
		// `keyedElement: (expression ':')? expression` — a leading key is a
		// struct field name in the common case, so only the value is checked.
		if exprs := n.AllExpression(); len(exprs) > 0 {
			c.walkExpr(exprs[len(exprs)-1])
		}
	case *grammar.TypeContext:
		// Type positions are out of scope for this check (see file header).
	case *grammar.IfExpressionContext:
		c.walkExpr(n.Expression())
		for _, br := range n.AllIfExprBranch() {
			b := br.(*grammar.IfExprBranchContext)
			if blk := b.Block(); blk != nil {
				c.walkBlock(blk.(*grammar.BlockContext))
			} else {
				c.walkExpr(b.Expression())
			}
		}
	case *grammar.DeclarationContext:
		c.walkDeclaration(n)
	case *grammar.StatementContext:
		c.walkStatement(n)
	case *grammar.SimpleStatementContext:
		c.walkSimpleStatement(n)
	default:
		for i := 0; i < node.GetChildCount(); i++ {
			c.walkExpr(node.GetChild(i))
		}
	}
}

// walkArgumentPattern treats a `pattern` node appearing in *argument* position
// as an ordinary expression. The same grammar rule serves `case` patterns,
// where identifiers bind instead — see walkCaseClause.
func (c *undefChecker) walkArgumentPattern(p grammar.IPatternContext) {
	switch pn := p.(type) {
	case nil:
		return
	case *grammar.ExpressionPatternContext:
		c.walkExpr(pn.Expression())
	case *grammar.RestPatternContext:
		c.walkExpr(pn.Expression())
	case *grammar.TypedPatternContext:
		// `x: T` in argument position is not an expression reference.
	default:
		c.walkExpr(p)
	}
}

func (c *undefChecker) walkLambda(n *grammar.LambdaExpressionContext) {
	c.push()
	defer c.pop()
	c.bindParameters(n.Parameters(), false)
	if b := n.Block(); b != nil {
		c.walkBlock(b.(*grammar.BlockContext))
		return
	}
	c.walkExpr(n.Expression())
}

// walkCaseClause binds every identifier the pattern mentions, then checks the
// guard and body against that scope. Binding constructors as well as capture
// names is deliberate: telling them apart needs the scrutinee's type, and
// over-binding can only miss detections, never invent them.
func (c *undefChecker) walkCaseClause(cc *grammar.CaseClauseContext) {
	c.push()
	defer c.pop()
	if p := cc.Pattern(); p != nil {
		for _, id := range collectIdentifiers(p) {
			c.bind(id)
		}
	}
	c.walkExpr(cc.GetGuard())
	if b := cc.GetBodyBlock(); b != nil {
		c.walkBlock(b.(*grammar.BlockContext))
	}
	if s := cc.GetBodyStmt(); s != nil {
		c.walkSimpleStatement(s.(*grammar.SimpleStatementContext))
	}
}

func (c *undefChecker) walkPostfixExpr(n *grammar.PostfixExprContext) {
	suffixes := n.AllPostfixSuffix()
	if prim := n.PrimaryExpr(); prim != nil {
		pe := prim.(*grammar.PrimaryExprContext)
		if p := pe.Primary(); p != nil {
			pc := p.(*grammar.PrimaryContext)
			if id := pc.Identifier(); id != nil {
				name := id.GetText()
				// `pkg.Symbol` — the head is a package qualifier, not a value.
				isQualifier := len(suffixes) > 0 &&
					isSelectorSuffix(suffixes[0]) &&
					!c.inScope(name) &&
					c.qualifiers[name]
				if !isQualifier && !c.resolves(name) {
					c.report(name, id.GetStart())
				}
			} else {
				c.walkExpr(pc)
			}
		} else {
			c.walkExpr(pe)
		}
	}
	for _, sfx := range suffixes {
		sc := sfx.(*grammar.PostfixSuffixContext)
		if sc.Identifier() != nil {
			continue // member selector — not a free identifier
		}
		if al := sc.ArgumentList(); al != nil {
			for _, arg := range al.(*grammar.ArgumentListContext).AllArgument() {
				c.walkExpr(arg)
			}
			continue
		}
		// Either an index (`xs[i]`) or an explicit type-argument list
		// (`Unfold[int, string](...)`). Both route through primary
		// identifiers, and type names resolve through the declared index.
		c.walkExpr(sc.ExpressionList())
	}
	for _, cc := range n.AllCaseClause() {
		c.walkCaseClause(cc.(*grammar.CaseClauseContext))
	}
}

func isSelectorSuffix(s grammar.IPostfixSuffixContext) bool {
	sc, ok := s.(*grammar.PostfixSuffixContext)
	return ok && sc.Identifier() != nil
}

// collectIdentifiers returns every `identifier` leaf under `node`.
func collectIdentifiers(node antlr.Tree) []string {
	var out []string
	var walk func(antlr.Tree)
	walk = func(t antlr.Tree) {
		if t == nil {
			return
		}
		if id, ok := t.(*grammar.IdentifierContext); ok {
			out = append(out, id.GetText())
			return
		}
		for i := 0; i < t.GetChildCount(); i++ {
			walk(t.GetChild(i))
		}
	}
	walk(node)
	return out
}

// --- import hint discovery --------------------------------------------------

// galaPkgExport names a GALA package that declares a sought-after symbol.
type galaPkgExport struct {
	importPath string
	pkgName    string
}

// galaDeclarationKeywords are the top-level declaration forms of a .gala
// source file; goDeclarationKeywords the same for the hand-written .go half of
// a GALA package (go_interop and friends export their symbols that way).
var (
	galaDeclarationKeywords = []string{"func ", "type ", "struct ", "sealed type ", "val ", "var "}
	goDeclarationKeywords   = []string{"func ", "type ", "var ", "const "}
)

// galaPackagesDeclaring finds every importable package under one of `roots`
// that declares a top-level `name`. Declarations are read from the candidate
// packages' own source — their `func` / `type` / `struct` / `sealed type` /
// `val` / `var` declarations — so the association comes from real
// declarations rather than a name list or a path substring. It only runs when
// an error is already being emitted, so a successful compile never pays for
// the directory walk.
func galaPackagesDeclaring(name string, roots []hintRoot, resolves func(importPath, dir string) bool) []galaPkgExport {
	var found []galaPkgExport
	for _, root := range roots {
		if root.dir == "" {
			continue
		}
		seenDir := make(map[string]bool)
		_ = filepath.Walk(root.dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if info.IsDir() {
				base := filepath.Base(path)
				if path != root.dir && (strings.HasPrefix(base, ".") || strings.HasPrefix(base, "bazel-") ||
					base == "node_modules" || base == "testdata") {
					return filepath.SkipDir
				}
				return nil
			}
			var keywords []string
			switch {
			case filepath.Ext(path) == ".gala" && !strings.HasSuffix(path, "_test.gala"):
				keywords = galaDeclarationKeywords
			case filepath.Ext(path) == ".go" &&
				!strings.HasSuffix(path, "_test.go") && !strings.HasSuffix(path, ".gen.go"):
				keywords = goDeclarationKeywords
			default:
				return nil
			}
			dir := filepath.Dir(path)
			if seenDir[dir] {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			src := string(data)
			if !declaresTopLevel(src, name, keywords) {
				return nil
			}
			pkg := packageClauseOf(src)
			if pkg == "" || pkg == "main" {
				return nil
			}
			rel, rerr := filepath.Rel(root.dir, dir)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
				return nil
			}
			importPath := pickImportPath(rel, root.prefix, dir, resolves)
			if importPath == "" {
				return nil
			}
			seenDir[dir] = true
			found = append(found, galaPkgExport{importPath: importPath, pkgName: pkg})
			return nil
		})
		if len(found) > 0 {
			break
		}
	}
	// Lexicographic order keeps the hint stable across runs and filesystems.
	sort.Slice(found, func(i, j int) bool { return found[i].importPath < found[j].importPath })
	return found
}

// pickImportPath chooses the spelling of `dir`'s import path that the resolver
// actually maps back to `dir`, so the hint never suggests a path the compiler
// would reject. Candidates, in preference order: the containing module's own
// prefix, the GALA distribution prefix (how the packages shipped with the
// toolchain are written), and the bare relative path. When the resolver cannot
// confirm any of them — it is absent, or the package sits somewhere it does not
// search — the module-prefixed form is returned as the best available guess,
// since suppressing the hint entirely would be less useful than an approximate
// one.
func pickImportPath(rel, modulePrefix, dir string, resolves func(importPath, dir string) bool) string {
	var candidates []string
	if modulePrefix != "" {
		candidates = append(candidates, modulePrefix+"/"+rel)
	}
	candidates = append(candidates, inRepoGalaImportPrefix+rel, rel)
	if resolves != nil {
		for _, c := range candidates {
			if resolves(c, dir) {
				return c
			}
		}
	}
	return candidates[0]
}

// declaresTopLevel reports whether `src` declares `name` at the top level.
// Top-level declarations start in column 0 in both GALA and gofmt'd Go;
// anything indented belongs to a nested scope and is not an export.
func declaresTopLevel(src, name string, keywords []string) bool {
	for _, line := range strings.Split(src, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		trimmed := strings.TrimRight(line, " \t\r")
		for _, kw := range keywords {
			rest, ok := cutPrefix(trimmed, kw)
			if !ok {
				continue
			}
			if declaredNameIs(strings.TrimLeft(rest, " \t"), name) {
				return true
			}
		}
	}
	return false
}

func cutPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// declaredNameIs reports whether the declaration text `rest` names `name`
// first — i.e. the identifier is followed by a non-identifier character.
func declaredNameIs(rest, name string) bool {
	if !strings.HasPrefix(rest, name) {
		return false
	}
	if len(rest) == len(name) {
		return true
	}
	c := rest[len(name)]
	return !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'))
}

func packageClauseOf(src string) string {
	for _, line := range strings.Split(src, "\n") {
		if rest, ok := cutPrefix(strings.TrimSpace(line), "package "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
