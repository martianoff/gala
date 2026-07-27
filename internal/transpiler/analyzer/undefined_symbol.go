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
	"martianoff/gala/internal/transpiler/scopewalk"
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
// The traversal is the shared lexical-scope walker in
// internal/transpiler/scopewalk, which also backs the concurrency capture
// analysis. Both passes need the same notion of "a value-position identifier
// that no enclosing binder introduced"; this file supplies the symbol table and
// decides what an unbound reference means. See undefWalkOptions for the few
// places the two passes' policies differ, and why each is set the way it is.
//
// WHAT THIS COVERS
//
// Every *bare identifier used in value position* — a variable read, a call
// target, a bare function reference — must resolve to something the analyzer
// knows about. Identifiers inside interpolated strings (`s"…$x…"`) are included:
// such a literal is a single lexer token, so the walker re-parses each embedded
// expression and walks it in the enclosing scope. A name must resolve to:
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
//   - Type references (`func f(x Foo)`, `val v Foo = ...`, `Foo{}`). The
//     analyzer's type resolution is lossy enough (Go generics, constraints,
//     `map[K]V`, func types) that flagging here would produce false
//     positives. Type positions are skipped wholesale: only identifiers that
//     reach a `primary` in expression position are checked.
//
//     GALA-E0025 does NOT pick up the remainder. It works from resolved
//     metadata, so it catches a signature type whose package reached the
//     compilation but whose import this file omitted — not a type name
//     nothing in the compilation declares. `func total(xs Array[int])` in a
//     package that imports collection_immutable nowhere therefore passes both
//     checks, erases its lambda to `func(acc any, x any) any`, and fails at
//     `go build`. Closing that needs a check that can tell an unresolvable
//     type name from a merely lossy one; widening this one would trade away
//     the zero-false-positive property.
//   - Constructor names in `match` / `case` *patterns*. The shared walker
//     binds the names a pattern introduces and ignores the constructor or
//     extractor it names, because telling them apart in general needs the
//     scrutinee's type. A typo in a pattern's constructor position is not
//     caught; the arm's body is checked normally.
//   - Composite-literal keys (`Point{X: 1}`), named-argument labels
//     (`f(name = 1)`) and postfix selectors, which are member names rather
//     than free identifiers.
//   - Files whose imports did not all load (see fileImportsFullyLoaded) and
//     the LSP analyzer (see galaAnalyzer.skipUndefinedCheck). In both cases
//     the symbol table is knowingly incomplete or the caller's contract is
//     best-effort, so a hard error would be worse than a missed detection.
//
// Four gaps an earlier revision documented are now closed by the move onto the
// shared walker, each covered by a test: interpolated-string bodies, lambda
// parameter defaults, the assigning form of `range`, and the file-wide
// stand-down that any Go dot import used to trigger.
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

// otherCheckOwnedNames are the names other coded checks own: the Go builtins of
// GALA-E0035 and the Go statement keywords of GALA-E0036. Both must keep
// producing their own, more specific diagnostics rather than being downgraded
// to a generic "undefined". The union is materialized once because each
// accessor builds a fresh map per call and the lookup sits on the
// per-identifier path.
var otherCheckOwnedNames = func() map[string]bool {
	out := transformer.ForbiddenGoBuiltins()
	for name := range transformer.ForbiddenStatementKeywords() {
		out[name] = true
	}
	return out
}()

// isGoPredeclaredTypeName reports whether name is one of Go's predeclared type
// names. They reach expression position as conversions (`int(x)`) and as
// explicit type arguments (`Unfold[int, string](...)`), both of which route
// through `primary: identifier`. The primitive set is shared with the rest of
// the transpiler so the two cannot drift; `comparable` is named separately
// because it is a constraint rather than a type, and so is absent there, but it
// does appear in type-argument position.
func isGoPredeclaredTypeName(name string) bool {
	return transpiler.IsPrimitiveType(name) || name == "comparable"
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

// fileImport is one `import` spec of the file under analysis, decoded once so
// the three consumers below — the eligibility precondition, the qualifier set,
// and the imported-declaration index — share a single walk of the import list.
type fileImport struct {
	// Path is the quoted import path with its quotes stripped.
	Path string
	// Alias is the explicit name given to the import (`import ci "…"`), empty
	// when none was written.
	Alias string
	// IsDot marks the `import . "…"` form, which brings the package's exports
	// into scope unqualified.
	IsDot bool
}

// LocalName is how the package is referred to in source: its alias when one was
// given, otherwise the trailing segment of its path.
func (fi fileImport) LocalName() string {
	if fi.Alias != "" {
		return fi.Alias
	}
	name := fi.Path
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

// scanFileImports decodes every import spec the file declares.
func scanFileImports(sf *grammar.SourceFileContext) []fileImport {
	var out []fileImport
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
			fi := fileImport{Path: strings.Trim(s.STRING().GetText(), "\"")}
			if alias := s.Identifier(); alias != nil {
				fi.Alias = alias.GetText()
			} else {
				// No identifier but an extra child is the `.` form (the same
				// test the dot-import scan in Analyze makes).
				fi.IsDot = s.GetChildCount() > 1
			}
			out = append(out, fi)
		}
	}
	return out
}

// isGalaImport reports whether an import path names a GALA package, using the
// same in-repo/external split as the import scan in Analyze.
func (a *galaAnalyzer) isGalaImport(path string) bool {
	return strings.HasPrefix(path, inRepoGalaImportPrefix) ||
		(a.resolver != nil && a.resolver.IsGalaPackage(path))
}

// undefChecker consumes the shared scope walker's stream of unbound
// value-position references and reports the ones that resolve to nothing. See
// the file header for the exact scope.
type undefChecker struct {
	rich *transpiler.RichAST

	// walker owns the scope stack and the traversal; this type only decides
	// what an unbound reference means.
	walker *scopewalk.Walker

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

	// hintRoots yields the roots to search (only when an error is already being
	// emitted) for a GALA package that declares the unresolved name. It is
	// deferred rather than materialized up front because computing the roots
	// reads each candidate's gala.mod/go.mod, and a successful compile must not
	// pay for hint machinery it never uses.
	hintRoots func() []hintRoot

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
	imports := scanFileImports(sourceFile)
	declared := indexDeclaredSymbols(richAST)
	for name := range a.undefinedSymbolLocalGoNames(filePath) {
		declared[name] = true
	}
	for name := range a.importedTopLevelNames(imports) {
		declared[name] = true
	}
	c := &undefChecker{
		rich:           richAST,
		declared:       declared,
		declaredTypes:  indexDeclaredTypeNames(richAST),
		qualifiers:     collectQualifiers(imports, richAST),
		hintRoots:      a.hintRoots,
		importResolves: a.importPathResolvesTo,
		reported:       make(map[string]bool),
	}
	c.walker = scopewalk.New(c, undefWalkOptions())
	c.walkSourceFile(sourceFile)

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
// Exactly two shapes make a file ineligible, both of them "the analyzer did
// not learn this package's contents", never merely "this package is Go":
//
//   - a GALA import with no successfully-analyzed entry in analyzedPkgs, and
//   - a *dot* import of a Go package that contributed no symbols at all.
//     Dot-importing is what makes a Go package's exports reachable unqualified,
//     so they have to be enumerable; they normally are, via GoTypeInfo, and
//     then the check stays fully live. They are not when the Go SDK is absent
//     (type inference is silently disabled — see the note in CLAUDE.md) or when
//     the package's name differs from its path's last segment, and only then
//     does the file stand down.
//
// A NAMED Go import never disqualifies a file: its symbols are reachable only
// through a qualifier, which the check accepts on sight. An earlier revision
// stood down for any Go dot-import whatsoever, which silently disabled the
// check for whole files over a healthy `import . "math"`.
func (a *galaAnalyzer) fileImportsFullyLoaded(imports []fileImport, richAST *transpiler.RichAST) bool {
	// The implicitly dot-imported prelude is subject to the same rule: it
	// never appears in the import list, so check it explicitly. (This is not
	// a special case for std — it is the one import the language adds on the
	// author's behalf, and it must be as loaded as any they wrote.)
	if entry, present := a.analyzedPkgs[registry.StdImportPath]; !present || entry == nil {
		return false
	}
	for _, imp := range imports {
		if a.isGalaImport(imp.Path) {
			if entry, present := a.analyzedPkgs[imp.Path]; !present || entry == nil {
				return false
			}
			continue
		}
		if imp.IsDot && !goPackageContributed(richAST, imp.Path) {
			return false
		}
	}
	return true
}

// goPackageContributed reports whether the analyzer learned any symbol of the
// Go package at `importPath`. Go metadata is keyed by package name, which is
// the path's last segment for the overwhelming majority of packages; a package
// that renames itself simply reads as "contributed nothing", which is the safe
// answer for the caller.
func goPackageContributed(rich *transpiler.RichAST, importPath string) bool {
	if rich == nil {
		return false
	}
	name := importPath
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	if name == "" {
		return false
	}
	if len(rich.GoExports[name]) > 0 {
		return true
	}
	gi := rich.GoTypeInfo
	if gi == nil {
		return false
	}
	prefix := name + "."
	for k := range gi.Functions {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	for k := range gi.Types {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	for k := range gi.Variables {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	for k := range gi.Constants {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	for k := range gi.TypeAliases {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
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
func (a *galaAnalyzer) importedTopLevelNames(imports []fileImport) map[string]bool {
	out := make(map[string]bool)
	for _, imp := range imports {
		if !a.isGalaImport(imp.Path) {
			continue // Go package — its symbols come from GoTypeInfo
		}
		for name := range a.packageTopLevelNames(strings.TrimPrefix(imp.Path, inRepoGalaImportPrefix)) {
			out[name] = true
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
		if a.importedNames != nil {
			a.importedNames[key] = nil
		}
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
	if a.importedNames != nil {
		a.importedNames[key] = names
	}
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
	if a.localGoNames != nil {
		a.localGoNames[dir] = names
	}
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
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
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
		out[simpleNameOf(key)] = true
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
func collectQualifiers(imports []fileImport, rich *transpiler.RichAST) map[string]bool {
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
	// This file's own imports: the alias when one is given, otherwise the
	// trailing path segment — how a Go import is referenced.
	for _, imp := range imports {
		if name := imp.LocalName(); name != "" {
			q[name] = true
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

// --- resolution -------------------------------------------------------------

// Reference implements scopewalk.Visitor. The shared walker calls it for every
// value-position identifier no enclosing scope binds; anything that does not
// then resolve through the compilation's symbol table is the error this pass
// exists to raise. `use` describes how the name was used, which this pass does
// not need — existence is existence however the name is spelled at the call
// site.
func (c *undefChecker) Reference(name string, tok antlr.Token, use scopewalk.Use) {
	if c.resolves(name) {
		return
	}
	c.report(name, tok)
}

// resolves reports whether `name` denotes anything at all. Scope is already
// handled by the shared walker — it only reports names no scope binds — so this
// consults the compilation's symbol table alone.
func (c *undefChecker) resolves(name string) bool {
	if name == "" {
		return true
	}
	if galaBuiltinValueNames[name] || isGoPredeclaredTypeName(name) {
		return true
	}
	// Names owned by other coded checks keep their own, more specific
	// diagnostics — see otherCheckOwnedNames.
	if otherCheckOwnedNames[name] {
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
	candidates := galaPackagesDeclaring(name, c.hintRoots(), c.importResolves)
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

// undefWalkOptions configures the shared scope walker for this pass. Every
// choice is the one that cannot invent a reference, because a false positive
// here is a hard compile error on code that works:
//
//   - ParseInterpolations descends into `s"…$x"` bodies, so a name used only
//     inside an interpolation is checked like any other.
//   - RequireCleanInterpolationParse drops a fragment whose re-parse reported
//     errors, so ANTLR's error recovery can never become a diagnostic. Narrow
//     by nature: the expression parser stops at the longest valid prefix
//     without complaining, so `${x +}` still yields `x`.
//   - SkipCompositeLiteralKeys omits `T{Field: v}`'s key, which names a struct
//     field rather than a value.
//   - SkipTypedPatternArgument omits the identifier of an `x: T` pattern in
//     argument position, which is not clearly a value reference.
//   - BindWholePattern stays FALSE, so a `case` binds only the names it
//     actually introduces instead of every identifier it mentions. Constructor
//     and extractor names in a pattern are still neither bound nor checked —
//     the walker ignores them — but they no longer leak into the arm's scope,
//     so the arm's BODY is checked against the names the pattern really
//     introduces rather than a set inflated by its constructors. Strictly
//     tighter, and it cannot add a reference the blunt form did not have.
//   - EnterFunctionScope binds the two binders the walker does not model: a
//     declaration's type parameters and a method's receiver.
func undefWalkOptions() scopewalk.Options {
	return scopewalk.Options{
		ParseInterpolations:            true,
		RequireCleanInterpolationParse: true,
		SkipCompositeLiteralKeys:       true,
		SkipTypedPatternArgument:       true,
		EnterFunctionScope:             bindFunctionDeclarationScope,
	}
}

// bindFunctionDeclarationScope binds a function or method declaration's own
// type parameters, and, for a method, its receiver name together with any type
// parameters the receiver type introduces (`func (l *List[T]) …` brings both
// `l` and `T` into scope).
func bindFunctionDeclarationScope(w *scopewalk.Walker, fn grammar.IFunctionDeclarationContext) {
	fc, ok := fn.(*grammar.FunctionDeclarationContext)
	if !ok {
		return
	}
	bindTypeParameters(w, fc.TypeParameters())
	recv := fc.Receiver()
	if recv == nil {
		return
	}
	rc, ok := recv.(*grammar.ReceiverContext)
	if !ok {
		return
	}
	w.BindID(rc.Identifier())
	// The receiver's type arguments are the method's view of the type's
	// parameters; they can sit behind a pointer or nest (`*List[T]`,
	// `Pair[K, V]`), so every identifier in the receiver type is bound. Binding
	// the type's own name alongside them is harmless — it is a declaration in
	// this package either way.
	w.BindIdentifiersIn(rc.Type_())
}

func bindTypeParameters(w *scopewalk.Walker, tp grammar.ITypeParametersContext) {
	if tp == nil {
		return
	}
	list := tp.(*grammar.TypeParametersContext).TypeParameterList()
	if list == nil {
		return
	}
	for _, p := range list.(*grammar.TypeParameterListContext).AllTypeParameter() {
		w.BindID(p.(*grammar.TypeParameterContext).Identifier(0))
	}
}

// walkSourceFile drives the shared walker over a whole file. Only declarations
// that hold value-position expressions are entered: a `type` / `sealed type` /
// `embed` declaration contains types and string literals, never a value this
// check inspects.
func (c *undefChecker) walkSourceFile(sf *grammar.SourceFileContext) {
	w := c.walker
	w.PushScope()
	defer w.PopScope()
	c.bindFileLevelNames(sf)

	for _, topDecl := range sf.AllTopLevelDeclaration() {
		switch {
		case topDecl.FunctionDeclaration() != nil:
			w.WalkFunctionDeclaration(topDecl.FunctionDeclaration())
		case topDecl.ValDeclaration() != nil:
			w.Walk(topDecl.ValDeclaration().(*grammar.ValDeclarationContext).ExpressionList())
		case topDecl.VarDeclaration() != nil:
			w.Walk(topDecl.VarDeclaration().(*grammar.VarDeclarationContext).ExpressionList())
		case topDecl.StructShorthandDeclaration() != nil:
			ctx := topDecl.StructShorthandDeclaration().(*grammar.StructShorthandDeclarationContext)
			w.PushScope()
			bindTypeParameters(w, ctx.TypeParameters())
			w.BindParameters(ctx.Parameters())
			w.WalkParameterDefaults(ctx.Parameters())
			w.PopScope()
		}
	}
}

// bindFileLevelNames registers declarations whose names are not reachable
// through richAST metadata: tuple-pattern package vals (`val (a, b) = ...`,
// which extractPackageVals deliberately skips) and `embed val` directives.
func (c *undefChecker) bindFileLevelNames(sf *grammar.SourceFileContext) {
	for _, d := range c.rich.EmbedDirectives {
		c.walker.Bind(d.VarName)
	}
	for _, topDecl := range sf.AllTopLevelDeclaration() {
		var tp grammar.ITuplePatternContext
		switch {
		case topDecl.ValDeclaration() != nil:
			tp = topDecl.ValDeclaration().(*grammar.ValDeclarationContext).TuplePattern()
		case topDecl.VarDeclaration() != nil:
			tp = topDecl.VarDeclaration().(*grammar.VarDeclarationContext).TuplePattern()
		case topDecl.EmbedDeclaration() != nil:
			c.walker.BindID(topDecl.EmbedDeclaration().(*grammar.EmbedDeclarationContext).Identifier())
		}
		if tp != nil {
			c.walker.BindIdentifierList(tp.(*grammar.TuplePatternContext).IdentifierList())
		}
	}
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
			rest, ok := strings.CutPrefix(trimmed, kw)
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
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "package "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
