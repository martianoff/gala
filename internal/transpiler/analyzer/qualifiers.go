package analyzer

import (
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// importBinding is one import of a file.
type importBinding struct {
	Path string
	// Alias is the name written for the import, empty when none was.
	Alias string
	// PkgName is the package's real name when known: for a GALA import the
	// name its metadata is filed under (richAST.Packages; empty when the
	// package failed to load), for a Go import the name its type info reports
	// (richAST.GoImportNames).
	PkgName string
	IsGala  bool
}

// fileQualifiers is a file's import table — the one place the analyzer
// answers "what does qualifier X mean in this file". Everything that needs a
// per-file view of the imports derives from it: the GALA-E0025 explicit set,
// the dot-import set used by type resolution, import aliases, package-level
// val inference, and the import path recorded on a type written against a Go
// import.
type fileQualifiers struct {
	// named maps every qualifier the file binds to its import, ranked as
	// transpiler.ImportNames describes: when the real name of an unaliased
	// import is unknown, every plausible name is bound, below any surer one.
	named map[string]importBinding
	// dots lists the dot imports.
	dots []importBinding
}

// qualifiersForFile builds sf's import table. richAST supplies package names,
// so it must run after the file's imports were loaded.
func (a *galaAnalyzer) qualifiersForFile(sf *grammar.SourceFileContext, richAST *transpiler.RichAST) fileQualifiers {
	names := transpiler.NewRankedNames[importBinding]()
	var q fileQualifiers
	for _, imp := range scanFileImports(sf) {
		b := importBinding{Path: imp.Path, Alias: imp.Alias, IsGala: a.isGalaImport(imp.Path)}
		if b.IsGala {
			b.PkgName = richAST.Packages[imp.Path]
		} else {
			b.PkgName = richAST.GoImportNames[imp.Path]
		}
		if imp.IsDot {
			q.dots = append(q.dots, b)
			continue
		}
		// A keyed package (see transpiler.PackageKey) binds its own name.
		names.Bind(imp.Path, imp.Alias, transpiler.PackageKeyName(b.PkgName, imp.Path), b)
	}
	q.named = names.Map()
	return q
}

// galaPackageNames adds to set the name of every GALA package the file
// imports, in any form — dot, plain or aliased.
func (q fileQualifiers) galaPackageNames(set map[string]bool) {
	add := func(b importBinding) {
		if b.IsGala && b.PkgName != "" {
			set[b.PkgName] = true
		}
	}
	for _, b := range q.named {
		add(b)
	}
	for _, b := range q.dots {
		add(b)
	}
}

// goImport returns the path of the Go import the file binds to qualifier.
func (q fileQualifiers) goImport(qualifier string) (string, bool) {
	b, ok := q.named[qualifier]
	if !ok || b.IsGala {
		return "", false
	}
	return b.Path, true
}

// currentGoTypeInfo is the Go type info of the file being analyzed, nil
// outside Analyze.
func (a *galaAnalyzer) currentGoTypeInfo() *transpiler.GoTypeInfo {
	if a.currentRichAST == nil {
		return nil
	}
	return a.currentRichAST.GoTypeInfo
}

// withGoImportPath records on a package-qualified type written in this file
// the path of the Go import its qualifier is bound to, so the type stays tied
// to that import — `*strings.Builder` after `import "strings"` is Go's type
// even when a GALA package named `strings` is loaded too.
//
// Only a type the Go package declares is tied to it. When the package's type
// info was loaded (its real name is known), `strings.Str` — no such Go type —
// stays untied, so GALA-E0025 still reports it against the GALA package of
// that name. Without type info the Go import is taken at its word. Other
// types are returned unchanged.
func (q fileQualifiers) withGoImportPath(t transpiler.Type, gi *transpiler.GoTypeInfo) transpiler.Type {
	nt, ok := t.(transpiler.NamedType)
	if !ok || nt.ImportPath != "" || nt.Package == "" {
		return t
	}
	b, ok := q.named[nt.Package]
	if !ok || b.IsGala {
		return t
	}
	if b.PkgName != "" && !gi.DeclaresType(b.PkgName, nt.Name) {
		return t
	}
	nt.ImportPath = b.Path
	return nt
}
