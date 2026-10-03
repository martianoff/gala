package analyzer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/genheader"
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
//   - Import discipline for Go names and for signature types. A bare Go name
//     is accepted if any Go package in the compilation declares it (the Go
//     compiler has the last word), and a signature type whose package this
//     file did not import is GALA-E0025's job (validateExplicitImports).
//
//     Bare GALA names are the exception, because the metadata holds every
//     package the import graph reached, not just this file's imports: a name
//     declared only by GALA packages this file neither belongs to nor
//     dot-imports (std counts as dot-imported) is reported here, in value and
//     in type position. Without that, a file importing only `strings` could
//     call `ArrayOf` unqualified and the transformer would bind it to
//     whichever collection package it found. See galaScope.
//   - Selectors. In `x.foo().bar`, only `x` is checked. Field and method
//     names need the receiver's type, which is inference territory.
//   - The member of a qualified type (`Builder` in `strings.Builder`), which
//     would need the full type surface of every imported Go package. Type
//     names are checked by a separate pass over type positions — the
//     qualifier of a qualified one, and an unqualified one for existence —
//     see checkTypeNames.
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
	// Tok is the spec's first token, so a diagnostic can point at this import
	// rather than at the file.
	Tok antlr.Token
}

// SpelledName is the alias, else the path's last segment, taken as written.
// For one path, equal spelled names mean the same binding repeated, whatever
// package name Go assigns — which is what the duplicate-import check keys on.
func (fi fileImport) SpelledName() string {
	if fi.Alias != "" {
		return fi.Alias
	}
	return transpiler.LastPathSegment(fi.Path)
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
			fi := fileImport{Path: strings.Trim(s.STRING().GetText(), "\""), Tok: s.GetStart()}
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

	// scope says which GALA packages this file can name a symbol of without
	// a qualifier. `declared` spans every package whose metadata reached the
	// compilation — including packages that arrived only because an import
	// imports them — so a name found there is checked against scope before
	// it counts. See galaScope.
	scope galaScope

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

	// hints is the source index behind the import hint, built on the first
	// undefined name and reused for the rest.
	hints *hintIndex

	// importResolves reports whether an import path maps to a directory, so
	// the hint can suggest a spelling the compiler will accept.
	importResolves func(importPath, dir string) bool

	errs []*galaerr.SemanticError
	// reported dedupes by name, so one misspelling used in twenty places
	// produces one actionable error rather than twenty. It maps the name to
	// its error's index in errs.
	reported map[string]int

	// types answers whether an unqualified name denotes a type — see
	// typeNameExists.
	types typeIndex

	// imports are this file's imports, for goTypeHint.
	imports []fileImport

	// sourceTypes holds the types importedTopLevelNames' source scan found in
	// the file's dot-imported packages and std, which the type check accepts
	// even where the metadata did not model them.
	sourceTypes map[string]bool

	// inTypePass is set while checkTypeNames runs; see reportWith.
	inTypePass bool
}

// checkUndefinedSymbols runs the existence check over `sourceFile` and returns
// the collected errors in source order. fileDotImportSets maps each file of
// the package (by canonical path) to the GALA package names it dot-imports; a
// method whose receiver type lives in another file may use that file's dot
// imports in its signature, the bare-name form of the allowance GALA-E0025
// makes.
func (a *galaAnalyzer) checkUndefinedSymbols(
	sourceFile *grammar.SourceFileContext,
	richAST *transpiler.RichAST,
	filePath string,
	fileDotImportSets map[string]map[string]bool,
) []*galaerr.SemanticError {
	imports := scanFileImports(sourceFile)
	declared := indexDeclaredSymbols(richAST)
	for name := range a.undefinedSymbolLocalGoNames(filePath) {
		declared[name] = true
	}
	sourceTypes := make(map[string]bool)
	for name, isType := range a.importedTopLevelNames(imports) {
		declared[name] = true
		if isType {
			sourceTypes[name] = true
		}
	}
	scope := a.buildGalaScope(imports, richAST, filePath)
	c := &undefChecker{
		rich:           richAST,
		declared:       declared,
		declaredTypes:  indexDeclaredTypeNames(richAST),
		scope:          scope,
		qualifiers:     collectQualifiers(imports, richAST),
		hintRoots:      a.hintRoots,
		importResolves: a.importPathResolvesTo,
		reported:       make(map[string]int),
		types:          a.indexTypes(imports, richAST, filePath, scope),
		imports:        imports,
		sourceTypes:    sourceTypes,
	}
	c.walker = scopewalk.New(c, undefWalkOptions())
	c.walkSourceFile(sourceFile)

	// Type positions are invisible to the value walker, so they get their own
	// pass over the same qualifier set. Sharing the checker shares its
	// `reported` map too, so a qualifier missing in both a value and a type
	// position is reported once rather than twice — each report walks every
	// search root to build its hint, so the dedupe is worth real work. It runs
	// under this function's eligibility guards, which is why it lives here
	// rather than standing alone. See checkTypeNames.
	c.checkTypeNames(sourceFile, func(recvType string) map[string]bool {
		return receiverFileImports(richAST, recvType, filePath, fileDotImportSets)
	})

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
// Three shapes make a file ineligible, all of them "the analyzer did not learn
// this package's contents", never merely "this package is Go":
//
//   - ANY package that failed to load during this compilation, at any depth —
//     see the packageLoadFailures check below,
//   - a GALA import with no successfully-analyzed entry in analyzedPkgs, and
//   - a *dot* import of a Go package that contributed no symbols at all.
//     Dot-importing is what makes a Go package's exports reachable unqualified,
//     so they have to be enumerable; they normally are, via GoTypeInfo, and
//     then the check stays fully live. They are not when the Go SDK is absent
//     (type inference is silently disabled — see the note in AGENTS.md) or when
//     the package's name differs from its path's last segment, and only then
//     does the file stand down.
//
// A NAMED Go import never disqualifies a file: its symbols are reachable only
// through a qualifier, which the check accepts on sight. An earlier revision
// stood down for any Go dot-import whatsoever, which silently disabled the
// check for whole files over a healthy `import . "math"`.
// notePackageLoadFailure records that a package could not be loaded. Called
// from analyzePackage's single deferred error path, so it catches a failure at
// any depth of the import graph.
func (a *galaAnalyzer) notePackageLoadFailure(relPath string) {
	if a.packageLoadFailures != nil {
		a.packageLoadFailures[relPath] = true
	}
}

func (a *galaAnalyzer) fileImportsFullyLoaded(imports []fileImport, richAST *transpiler.RichAST) bool {
	// Any package that failed to load, at any depth, disqualifies every file
	// in this compilation. The file's own import list is not enough to decide
	// this: the missing package is usually one a DEPENDENCY imported. std
	// dot-imports go_builtins, for instance, so a file that imports nothing
	// still resolves bare `Panic` through std's closure — and if go_builtins
	// did not load, that name goes missing without anything in the file
	// hinting why. Reporting it as undefined would blame the author for an
	// environmental failure.
	if len(a.packageLoadFailures) > 0 {
		return false
	}
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
// Go package at `importPath`. Go metadata is keyed by package name: the real
// one when the analyzer learned it (RichAST.GoImportNames), else one of the
// names the path may bind. A package that renames itself beyond those simply
// reads as "contributed nothing", which is the safe answer for the caller.
func goPackageContributed(rich *transpiler.RichAST, importPath string) bool {
	if rich == nil {
		return false
	}
	for _, n := range transpiler.ImportNames(importPath, "", rich.GoImportNames[importPath]) {
		if n.Name != "" && (len(rich.GoExports[n.Name]) > 0 || goInfoDeclaresPackage(rich.GoTypeInfo, n.Name)) {
			return true
		}
	}
	return false
}

// goPackageName is the real name of the Go package at importPath, read from
// its type info: whichever name the path may bind that the package's symbols
// are filed under. Empty when none is.
func goPackageName(gi *transpiler.GoTypeInfo, importPath string) string {
	for _, n := range transpiler.ImportNames(importPath, "", "") {
		if goInfoDeclaresPackage(gi, n.Name) {
			return n.Name
		}
	}
	return ""
}

// goInfoDeclaresPackage reports whether any symbol in gi is filed under the
// package name `name`.
func goInfoDeclaresPackage(gi *transpiler.GoTypeInfo, name string) bool {
	if gi == nil || name == "" {
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
// a package exports. Package-level `val`/`var` declarations, for instance,
// cross a package boundary only as RichAST.ImportedVals — exported names,
// keyed by the declaring package's import path — never into the importer's
// own PackageVals, which the transformer pre-registers as the current
// package's names. Rather
// than tie this existence check to how each export kind is modelled for code
// generation, the declarations are read directly. Any future export kind the
// metadata does not model is covered by the same net.
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
		// Only a dot import puts a package's names in scope unqualified; a
		// named import is reached through its qualifier, which is checked
		// separately.
		if !imp.IsDot {
			continue
		}
		for name, isType := range a.packageTopLevelNames(strings.TrimPrefix(imp.Path, inRepoGalaImportPrefix)) {
			out[name] = out[name] || isType
		}
	}
	// The implicit prelude is subject to the same treatment as any written
	// import — it resolves through the ordinary package path, not a bypass.
	for name, isType := range a.packageTopLevelNames(registry.StdPackageName) {
		out[name] = out[name] || isType
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
// tuple-pattern destructuring), and embed bindings. A name's value is true
// when it names a type.
func collectTopLevelDeclaredNames(sf *grammar.SourceFileContext, out map[string]bool) {
	record := func(id grammar.IIdentifierContext) {
		if id != nil && !out[id.GetText()] {
			out[id.GetText()] = false
		}
	}
	recordType := func(id grammar.IIdentifierContext) {
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
			recordType(topDecl.TypeDeclaration().(*grammar.TypeDeclarationContext).Identifier())
		case topDecl.StructShorthandDeclaration() != nil:
			recordType(topDecl.StructShorthandDeclaration().(*grammar.StructShorthandDeclarationContext).Identifier())
		case topDecl.OpaqueTypeDeclaration() != nil:
			recordType(topDecl.OpaqueTypeDeclaration().(*grammar.OpaqueTypeDeclarationContext).Identifier())
		case topDecl.SealedTypeDeclaration() != nil:
			sc := topDecl.SealedTypeDeclaration().(*grammar.SealedTypeDeclarationContext)
			recordType(sc.Identifier())
			for _, cc := range sc.AllSealedCase() {
				recordType(cc.(*grammar.SealedCaseContext).Identifier())
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
	// A test file may also use the package's Go test helpers.
	withTests := strings.HasSuffix(filePath, "_test.gala")
	key := canonicalPath(filepath.Dir(filePath))
	if withTests {
		key += "\x00test"
	}
	if cached, ok := a.localGoNames[key]; ok {
		return cached
	}
	names := parseLocalGoDeclNames(filepath.Dir(filePath), withTests)
	if a.localGoNames != nil {
		a.localGoNames[key] = names
	}
	return names
}

// parseLocalGoDeclNames parses `dir`'s hand-written .go files and returns the
// names their top-level declarations introduce. Stale transpiler output is
// excluded, since it restates what the .gala sources contribute; a .gen.go
// another generator wrote is kept (see genheader). In-package test files count
// only withTests.
func parseLocalGoDeclNames(dir string, withTests bool) map[string]bool {
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
		if e.IsDir() || !strings.HasSuffix(n, ".go") || (!withTests && strings.HasSuffix(n, "_test.go")) {
			continue
		}
		path := filepath.Join(dir, n)
		if genheader.StaleFile(path) {
			continue
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil || f == nil || strings.HasSuffix(f.Name.Name, "_test") {
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
	// This file's own imports: every name each may bind (transpiler.ImportNames)
	// — how a Go import is referenced.
	for _, imp := range imports {
		pkgName := rich.GoImportNames[imp.Path]
		if pkgName == "" {
			pkgName = rich.Packages[imp.Path]
		}
		for _, n := range transpiler.ImportNames(imp.Path, imp.Alias, pkgName) {
			if n.Name != "" {
				q[n.Name] = true
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
		return !c.scope.hides(name)
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
	c.reportWith(name, tok, "", "")
}

// isReported reports whether name already has an error.
func (c *undefChecker) isReported(name string) bool {
	_, seen := c.reported[name]
	return seen
}

// reportWith reports name at tok with msg and hint. An empty msg means
// `undefined: name`, an empty hint hintFor's.
func (c *undefChecker) reportWith(name string, tok antlr.Token, msg, hint string) {
	if tok == nil {
		return
	}
	// The span covers the reported token, which is normally the identifier
	// itself. For a name found inside an interpolated string the token is the
	// whole literal (a re-parsed fragment has no file position of its own), so
	// deriving the span from the token's text underlines the literal rather
	// than a name-length slice of it.
	span := tok.GetColumn() + len([]rune(tok.GetText()))
	if idx, seen := c.reported[name]; seen {
		// The type pass runs after the value pass, so a name both report may
		// first be used in a type position earlier in the file: point there.
		prev := c.errs[idx]
		if c.inTypePass && (tok.GetLine() < prev.Line || (tok.GetLine() == prev.Line && tok.GetColumn() < prev.Column)) {
			moved := *prev
			moved.Line, moved.Column = tok.GetLine(), tok.GetColumn()
			c.errs[idx] = moved.WithSpan(span)
		}
		return
	}
	if msg == "" {
		msg = fmt.Sprintf("undefined: %s", name)
	}
	if hint == "" {
		hint = c.hintFor(name)
	}
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeUndefinedVariable,
		tok.GetLine(), tok.GetColumn(), msg, hint,
	).WithSpan(span)
	c.reported[name] = len(c.errs)
	c.errs = append(c.errs, err)
}

// hintFor produces the actionable half of the diagnostic. When the name is
// declared by GALA packages the search paths can see, it names the import(s)
// that would bring it into scope; otherwise it falls back to generic guidance.
func (c *undefChecker) hintFor(name string) string {
	// The file imports a package that declares the name, but by name rather
	// than with a dot, so the name is reached through its qualifier.
	if qualifier, ok := c.scope.namedImportQualifier(name); ok {
		return fmt.Sprintf(
			"%s is declared in a package this file imports by name; call it as `%s.%s`, "+
				"or dot-import that package to use it unqualified.",
			name, qualifier, name)
	}
	if c.hints == nil {
		c.hints = newHintIndex(c.hintRoots())
	}
	candidates := galaPackagesDeclaring(name, c.hints, c.importResolves)
	switch len(candidates) {
	case 0:
		// Not on the search roots, but the compilation loaded a package that
		// declares it (typically another package of this module, reached
		// through the import graph): name its import path.
		if paths := c.scope.declaringImportPaths(name); len(paths) > 0 {
			return fmt.Sprintf(
				"%s is declared in %s, which this file does not import. "+
					"Add `import . \"<path>\"` to use it unqualified, or import it plainly and qualify the call.",
				name, strings.Join(quoteAll(paths), ", "))
		}
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

// bindFileLevelNames registers this file's tuple-pattern package vals and vars
// (`val (a, b) = ...`) from the parse tree, so they bind whatever metadata the
// check runs against, and `embed val` directives, which no richAST metadata
// lists.
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

// hintSource is one source file the import hint may search — a non-test
// .gala file, or a hand-written .go file of a GALA package — reduced to what
// the hint needs: its directory, its package name and its top-level names.
type hintSource struct {
	dir   string
	pkg   string
	names map[string]bool
}

// hintIndex holds, per hint root (in root order), the source files under it.
// It is built once per checker and shared by every undefined name the file
// reports: rescanning the tree for each name cost seconds on a
// repository-sized root once a file had a few typos. A root is read only when
// every earlier root came up empty, so the common case — the name is declared
// under the first root — never walks the later ones.
type hintIndex struct {
	roots []hintRoot
	files [][]hintSource
	read  []bool
}

func newHintIndex(roots []hintRoot) *hintIndex {
	return &hintIndex{roots: roots, files: make([][]hintSource, len(roots)), read: make([]bool, len(roots))}
}

// filesUnder returns root i's source files, reading them on first use. It only
// runs when an error is already being emitted, so a successful compile never
// pays for the directory walk.
func (idx *hintIndex) filesUnder(i int) []hintSource {
	if idx.read[i] {
		return idx.files[i]
	}
	idx.read[i] = true
	root := idx.roots[i]
	if root.dir == "" {
		return nil
	}
	// WalkDir, not Walk: Walk lstats every entry, most of them files this
	// search never opens. WalkDir reads the entry type from the directory
	// listing; like Walk, it does not follow links.
	_ = filepath.WalkDir(root.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
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
		case filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go"):
			keywords = goDeclarationKeywords
		default:
			return nil
		}
		data, rerr := os.ReadFile(path)
		// The transpiler's own output restates a .gala file the walk reads.
		if rerr != nil || genheader.Stale(path, data) {
			return nil
		}
		pkg, names := scanHintSource(string(data), keywords)
		if pkg == "" || pkg == "main" {
			return nil
		}
		idx.files[i] = append(idx.files[i], hintSource{dir: filepath.Dir(path), pkg: pkg, names: names})
		return nil
	})
	return idx.files[i]
}

// galaPackagesDeclaring finds every importable package in idx that declares a
// top-level `name`. Declarations are read from the candidate packages' own
// source — their `func` / `type` / `struct` / `sealed type` / `val` / `var`
// declarations — so the association comes from real declarations rather than
// a name list or a path substring. Roots are tried in order and the first one
// with a match wins.
func galaPackagesDeclaring(name string, idx *hintIndex, resolves func(importPath, dir string) bool) []galaPkgExport {
	var found []galaPkgExport
	for i, root := range idx.roots {
		seenDir := make(map[string]bool)
		for _, f := range idx.filesUnder(i) {
			if seenDir[f.dir] || !f.names[name] {
				continue
			}
			rel, rerr := filepath.Rel(root.dir, f.dir)
			if rerr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
				continue
			}
			importPath := pickImportPath(rel, root.prefix, f.dir, resolves)
			if importPath == "" {
				continue
			}
			seenDir[f.dir] = true
			found = append(found, galaPkgExport{importPath: importPath, pkgName: f.pkg})
		}
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

// scanHintSource returns, in one pass over `src`, its package clause and the
// names it declares at the top level: the identifier right after one of
// `keywords`. Top-level declarations start in column 0 in both GALA and
// gofmt'd Go; anything indented belongs to a nested scope and is not an
// export.
func scanHintSource(src string, keywords []string) (string, map[string]bool) {
	pkg := ""
	names := make(map[string]bool)
	for line := range strings.Lines(src) {
		if pkg == "" {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "package "); ok {
				pkg = strings.TrimSpace(rest)
				continue
			}
		}
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		for _, kw := range keywords {
			rest, ok := strings.CutPrefix(line, kw)
			if !ok {
				continue
			}
			rest = strings.TrimLeft(rest, " \t")
			end := strings.IndexFunc(rest, func(r rune) bool {
				return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
			})
			if end < 0 {
				end = len(rest)
			}
			if end > 0 {
				names[rest[:end]] = true
			}
		}
	}
	return pkg, names
}

// --- type-position names -----------------------------------------------------

// checkTypeNames checks the type names written in TYPE positions. It reports a
// package qualifier that no import brings into scope, so `var sb
// strings.Builder` in a file that never imports `strings` is a GALA diagnostic
// rather than a Go one, and an unqualified type name that does not resolve.
//
// The shared scope walker is a VALUE-reference walker: it short-circuits
// TypeContext by design, because a type name is not a value and leaking one
// into the reference stream would break the capture analysis built on the same
// walker. That left type positions unchecked. The call half of
//
//	var sb strings.Builder                 // unreported
//	sb.WriteString(strings.Repeat("-", n)) // GALA-E0023
//
// was caught and the type half was not; a file whose ONLY offending use was a
// type position got no GALA error at all and fell through to `go build`.
//
// It is a method rather than a free function so it shares the caller's
// qualifier set, error slice and `reported` map; an earlier revision built a
// second checker and reported a doubly-missing qualifier twice.
//
// Only the QUALIFIER is checked, never the member. `strings.Builder` asks
// whether `strings` is in scope, not whether it exports `Builder` — the
// qualifier is unambiguously a package name, while resolving the member would
// need the full type surface of every imported Go package and would misfire
// whenever that surface is incomplete (no Go SDK, an unanalyzed package).
//
// An unqualified type name is reported when nothing declares it (see
// typeNameExists), or when only GALA packages out of this file's scope do
// (see galaScope), unless the file binds it itself (see
// collectFileTypeBinders). It uses the value check's symbol table, so it
// stands down under the same conditions (see fileImportsFullyLoaded).
//
// In the signature of a method whose receiver type is declared in another
// file of the package, the packages that file dot-imports count as well — the
// allowance GALA-E0025 makes for method signatures. receiverImports returns
// them. The method body gets no allowance, for type and value names alike.
func (c *undefChecker) checkTypeNames(sourceFile *grammar.SourceFileContext, receiverImports func(recvType string) map[string]bool) {
	c.inTypePass = true
	defer func() { c.inTypePass = false }()
	typeParams := collectFileTypeBinders(sourceFile)
	var walk func(n antlr.Tree, extra map[string]bool)
	walk = func(n antlr.Tree, extra map[string]bool) {
		if fd, ok := n.(*grammar.FunctionDeclarationContext); ok && fd.Receiver() != nil {
			var sigExtra map[string]bool
			if rc, ok := fd.Receiver().(*grammar.ReceiverContext); ok && rc.Type_() != nil {
				sigExtra = receiverImports(receiverBaseTypeName(rc.Type_().GetText()))
			}
			for i := 0; i < n.GetChildCount(); i++ {
				switch child := n.GetChild(i).(type) {
				case *grammar.BlockContext, *grammar.ExpressionContext:
					walk(child, nil)
				case antlr.ParserRuleContext:
					walk(child, sigExtra)
				}
			}
			return
		}
		// Nested TypeContexts are visited too: `map[string]strings.Builder`
		// and `[]pkg.T` carry their named type below an outer TypeContext.
		if tc, ok := n.(*grammar.TypeContext); ok {
			c.checkTypeName(tc, typeParams, extra)
		}
		c.checkBareTypePositions(n, typeParams, extra)
		skip := tupleDestructureType(n)
		// Only a parser rule can contain a TypeContext; terminals are skipped.
		for i := 0; i < n.GetChildCount(); i++ {
			if child, ok := n.GetChild(i).(antlr.ParserRuleContext); ok && antlr.Tree(child) != skip {
				walk(child, extra)
			}
		}
	}
	walk(sourceFile, nil)
}

// checkTypeName checks the type name a single TypeContext spells, if any.
func (c *undefChecker) checkTypeName(tc *grammar.TypeContext, typeParams, extra map[string]bool) {
	qi := tc.QualifiedIdentifier()
	if qi == nil {
		return
	}
	ids := qi.(*grammar.QualifiedIdentifierContext).AllIdentifier()
	if len(ids) < 2 {
		c.checkBareTypeName(ids[0], typeParams, extra)
		return
	}
	if name := ids[0].GetText(); !c.qualifiers[name] {
		c.report(name, ids[0].GetStart())
	}
}

// checkBareTypeName checks an unqualified type name. Besides a TypeContext's,
// the grammar spells three type positions as a bare identifier: an alias
// target (`type Coord Point`), a type parameter's constraint (`[T Number]`)
// and an unnamed parameter of a function type (`func(Widget) int`).
func (c *undefChecker) checkBareTypeName(id grammar.IIdentifierContext, typeParams, extra map[string]bool) {
	name := id.GetText()
	switch {
	case typeParams[name]:
	case name == "_":
		// `_` is a wildcard the transformer gives its meaning to: any type
		// argument in a type pattern (`case a: Array[_]`), an inferred lambda
		// parameter type (`(x _) => x`), and its own errors elsewhere.
	case c.sourceTypes[name]:
		// The source of a package this file dot-imports declares it as a type,
		// whatever the metadata recorded.
	case c.scope.hidesFrom(name, extra):
		c.report(name, id.GetStart())
	case c.typeNameExists(name, extra):
	case len(c.types.galaOwners[name]) > 0:
		c.report(name, id.GetStart())
	case c.isReported(name):
		// report moves the existing error to the first use; the hint below
		// would be thrown away.
		c.report(name, id.GetStart())
	default:
		if hint := c.goTypeHint(name); hint != "" {
			// Only a Go package this file does not dot-import declares it.
			c.reportWith(name, id.GetStart(), "", hint)
		} else if c.scope.declaresVisibly(name, extra) {
			// A function or value this file can name: say so rather than
			// suggest an import the file may already have.
			c.reportWith(name, id.GetStart(), fmt.Sprintf("%s is not a type", name),
				fmt.Sprintf("%s names a function or value; a type position needs a type", name))
		} else {
			c.report(name, id.GetStart())
		}
	}
}

// checkBareTypePositions checks the bare-identifier type positions n holds
// directly; see checkBareTypeName.
func (c *undefChecker) checkBareTypePositions(n antlr.Tree, typeParams, extra map[string]bool) {
	switch ctx := n.(type) {
	case *grammar.TypeAliasContext:
		if id := ctx.Identifier(); id != nil {
			c.checkBareTypeName(id, typeParams, extra)
		}
	case *grammar.TypeParameterContext:
		if ids := ctx.AllIdentifier(); len(ids) == 2 {
			c.checkBareTypeName(ids[1], typeParams, extra)
		}
	case *grammar.TypeContext:
		sig, ok := ctx.Signature().(*grammar.SignatureContext)
		if !ok {
			return
		}
		params, ok := sig.Parameters().(*grammar.ParametersContext)
		if !ok || params.ParameterList() == nil {
			return
		}
		for _, p := range params.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			if pc, ok := p.(*grammar.ParameterContext); ok && pc.Identifier() != nil && pc.Type_() == nil {
				c.checkBareTypeName(pc.Identifier(), typeParams, extra)
			}
		}
	}
}

// tupleDestructureType returns the type a `val (a, b)` / `var (a, b)`
// declaration was parsed with, if any. A destructuring takes no type, and
// GALA-E0056 rejects one with the message that fits. The grammar is blind to
// newlines, so for `var (a, b)` the first identifier of the next line parses as
// this type: checking it as a type name would report that identifier instead.
func tupleDestructureType(n antlr.Tree) antlr.Tree {
	switch d := n.(type) {
	case *grammar.ValDeclarationContext:
		if d.TuplePattern() != nil && d.Type_() != nil {
			return d.Type_()
		}
	case *grammar.VarDeclarationContext:
		if d.TuplePattern() != nil && d.Type_() != nil {
			return d.Type_()
		}
	}
	return nil
}

// typeNameExists reports whether an unqualified type name denotes a type: a Go
// predeclared one, one of typeIndex's, or one the prelude registers. Type
// parameters and types declared inside function bodies are bound by the
// caller. extra holds the packages a method signature may also use, as for
// galaScope.hidesFrom.
func (c *undefChecker) typeNameExists(name string, extra map[string]bool) bool {
	if isGoPredeclaredTypeName(name) || c.types.has(name, extra) {
		return true
	}
	// A prelude package registers its type surface, which includes types its
	// .gala sources do not declare: the `Sendable[F]` marker, which nothing
	// declares, and Go-defined ones such as std's `EmbeddedFS`, whose Go type
	// info is not always loaded. The transformer resolves prelude types from
	// the same registry.
	_, ok := registry.Global.IsPreludeType(name)
	return ok
}

// typeIndex holds what may denote a type in one file. Unlike the value check's
// table it leaves out functions and values, and every type is checked against
// the packages this file can name it from unqualified, so `func f(d Duration)`
// under a plain `import "time"` is reported.
type typeIndex struct {
	// localGo holds the declarations of this package's own Go files.
	localGo map[string]bool
	// galaOwners maps a name to the GALA packages declaring it as a type,
	// alias or companion, or in their own Go files (GoExports, which holds the
	// scanned Go files of GALA packages only, without telling types apart).
	galaOwners map[string][]string
	// galaVisible is galaScope's visible set: this package, the prelude and
	// the GALA packages this file dot-imports.
	galaVisible map[string]bool
	// goVisible holds the names the Go packages this file dot-imports are
	// filed under in GoTypeInfo.
	goVisible map[string]bool
	goInfo    *transpiler.GoTypeInfo
}

// has reports whether name is a type here. extra adds the GALA packages a
// method signature may also use; a Go dot import is scoped to its own file, in
// Go as here, so it gives no such allowance.
func (ti typeIndex) has(name string, extra map[string]bool) bool {
	if ti.localGo[name] {
		return true
	}
	for _, pkg := range ti.galaOwners[name] {
		if ti.galaVisible[pkg] || extra[pkg] {
			return true
		}
	}
	if gi := ti.goInfo; gi != nil {
		for pkg := range ti.goVisible {
			if gi.Types[pkg+"."+name] != nil {
				return true
			}
			if _, ok := gi.TypeAliases[pkg+"."+name]; ok {
				return true
			}
		}
	}
	// A Go package's type info comes from the host's build context, which
	// lacks the types only another platform's files declare, and, when the
	// package itself did not load, holds just the types other packages
	// mention. So in a file that dot-imports a Go package, a name not found
	// may still be one of its types, and is not reported.
	return len(ti.goVisible) > 0
}

func (a *galaAnalyzer) indexTypes(imports []fileImport, rich *transpiler.RichAST, filePath string, scope galaScope) typeIndex {
	ti := typeIndex{
		localGo:     a.undefinedSymbolLocalGoNames(filePath),
		galaOwners:  make(map[string][]string),
		galaVisible: scope.visible,
		goVisible:   make(map[string]bool),
		goInfo:      rich.GoTypeInfo,
	}
	addGala := func(key, pkg string) {
		name := simpleNameOf(key)
		if pkg == "" {
			pkg = rich.PackageName
			if dot := strings.LastIndexByte(key, '.'); dot > 0 {
				pkg = key[:dot]
			}
		}
		ti.galaOwners[name] = append(ti.galaOwners[name], pkg)
	}
	for k, tm := range rich.Types {
		if tm != nil {
			addGala(k, tm.Package)
		}
	}
	for k := range rich.TypeAliases {
		addGala(k, "")
	}
	for k, co := range rich.CompanionObjects {
		if co != nil {
			addGala(k, co.Package)
		}
	}
	for pkg, symbols := range rich.GoExports {
		for _, s := range symbols {
			ti.galaOwners[s] = append(ti.galaOwners[s], pkg)
		}
	}
	for _, imp := range imports {
		if !imp.IsDot || a.isGalaImport(imp.Path) {
			continue
		}
		for _, n := range transpiler.ImportNames(imp.Path, "", rich.GoImportNames[imp.Path]) {
			ti.goVisible[n.Name] = true
		}
	}
	return ti
}

// goTypeHint is the hint for a type name only Go packages this file does not
// dot-import declare, or "" when none does. It names the qualifier of one the
// file imports by name, if any, and otherwise one that declares it.
func (c *undefChecker) goTypeHint(name string) string {
	gi := c.rich.GoTypeInfo
	if gi == nil {
		return ""
	}
	declares := func(pkg string) bool {
		_, alias := gi.TypeAliases[pkg+"."+name]
		return gi.Types[pkg+"."+name] != nil || alias
	}
	for _, imp := range c.imports {
		if imp.IsDot || imp.Alias == "_" {
			continue
		}
		pkgName := c.rich.GoImportNames[imp.Path]
		if pkgName == "" || !declares(pkgName) {
			continue
		}
		qualifier := imp.Alias
		if qualifier == "" {
			qualifier = pkgName
		}
		return fmt.Sprintf("%s is a type of the Go package %q, which this file imports by name; write `%s.%s`",
			name, imp.Path, qualifier, name)
	}
	var owners []string
	for _, m := range []map[string]bool{goKeyPackages(gi.Types, name), goKeyPackages(gi.TypeAliases, name)} {
		for pkg := range m {
			owners = append(owners, pkg)
		}
	}
	if len(owners) == 0 {
		return ""
	}
	owner := slices.Min(owners)
	return fmt.Sprintf("%s is a type of the Go package %s, which this file does not import; import the package that declares it and write `%s.%s`",
		name, owner, owner, name)
}

// goKeyPackages returns the packages m files name under ("pkg.name" keys).
func goKeyPackages[V any](m map[string]V, name string) map[string]bool {
	out := make(map[string]bool)
	suffix := "." + name
	for k := range m {
		if strings.HasSuffix(k, suffix) && len(k) > len(suffix) {
			out[k[:len(k)-len(suffix)]] = true
		}
	}
	return out
}

// receiverBaseTypeName reduces a receiver's type text (`*Box[T]`) to the bare
// type name (`Box`).
func receiverBaseTypeName(text string) string {
	text = strings.TrimLeft(text, "*")
	if i := strings.IndexByte(text, '['); i >= 0 {
		text = text[:i]
	}
	return text
}

// receiverFileImports returns the GALA package names imported by the file
// that declares recvType, when that is a different file from filePath.
func receiverFileImports(rich *transpiler.RichAST, recvType, filePath string, fileImportSets map[string]map[string]bool) map[string]bool {
	tm := rich.Types[recvType]
	if tm == nil {
		tm = rich.Types[rich.PackageName+"."+recvType]
	}
	if tm == nil || tm.DefinedIn == "" {
		return nil
	}
	declaring := canonicalPath(tm.DefinedIn)
	if declaring == canonicalPath(filePath) {
		return nil
	}
	return fileImportSets[declaring]
}

// collectReceiverTypeArgs adds the names a receiver type's arguments bind:
// `Box[K, V]` and `*Box[T]` bind K, V and T.
func collectReceiverTypeArgs(n antlr.Tree, out map[string]bool) {
	if ta, ok := n.(*grammar.TypeArgumentsContext); ok {
		tl, ok := ta.TypeList().(*grammar.TypeListContext)
		if !ok {
			return
		}
		for _, t := range tl.AllType_() {
			tc, ok := t.(*grammar.TypeContext)
			if !ok || tc.QualifiedIdentifier() == nil {
				continue
			}
			if ids := tc.QualifiedIdentifier().(*grammar.QualifiedIdentifierContext).AllIdentifier(); len(ids) == 1 {
				out[ids[0].GetText()] = true
			}
		}
		return
	}
	for i := 0; i < n.GetChildCount(); i++ {
		if child, ok := n.GetChild(i).(antlr.ParserRuleContext); ok {
			collectReceiverTypeArgs(child, out)
		}
	}
}

// collectFileTypeBinders returns every type name the file binds itself: the
// type parameters it declares on any function, method or type — including the
// names a method receiver binds (`func (b Box[T]) ...` binds T) — and the types
// it declares at any depth, sealed variants included. The package-level types
// are in the metadata as well; one declared inside a function body is only
// here. Like the type parameters, they are recognised file-wide, which can
// only suppress a report.
func collectFileTypeBinders(node antlr.Tree) map[string]bool {
	out := make(map[string]bool)
	bind := func(id grammar.IIdentifierContext) {
		if id != nil {
			out[id.GetText()] = true
		}
	}
	var walk func(antlr.Tree)
	walk = func(n antlr.Tree) {
		switch ctx := n.(type) {
		case *grammar.TypeParameterContext:
			if ids := ctx.AllIdentifier(); len(ids) > 0 {
				bind(ids[0])
			}
			return
		case *grammar.ReceiverContext:
			if rt, ok := ctx.Type_().(*grammar.TypeContext); ok {
				collectReceiverTypeArgs(rt, out)
			}
			return
		case *grammar.TypeDeclarationContext:
			bind(ctx.Identifier())
		case *grammar.StructShorthandDeclarationContext:
			bind(ctx.Identifier())
		case *grammar.SealedTypeDeclarationContext:
			bind(ctx.Identifier())
		case *grammar.OpaqueTypeDeclarationContext:
			bind(ctx.Identifier())
		case *grammar.SealedCaseContext:
			bind(ctx.Identifier())
		}
		for i := 0; i < n.GetChildCount(); i++ {
			if child, ok := n.GetChild(i).(antlr.ParserRuleContext); ok {
				walk(child)
			}
		}
	}
	walk(node)
	return out
}
