package analyzer

import (
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// importBinding is one import of a file.
type importBinding struct {
	Path string
	// PkgName is the package's name: for a GALA import the name its metadata
	// is filed under (richAST.Packages), empty when the package failed to
	// load; for a Go import the name the import binds.
	PkgName string
	IsGala  bool
}

// fileQualifiers is a file's import table — the one place the analyzer
// answers "what does qualifier X mean in this file". Everything that needs a
// per-file view of the imports derives from it: the GALA-E0025 explicit set,
// the dot-import set used by type resolution, and the import path recorded on
// a type written against a Go import.
type fileQualifiers struct {
	// named maps every qualifier the file binds to its import. An unaliased
	// GALA import binds its package name; an unaliased Go import binds every
	// name its path may bind (transpiler.PackageNameCandidates).
	named map[string]importBinding
	// dots lists the dot imports.
	dots []importBinding
}

// qualifiersForFile builds sf's import table. richAST supplies GALA package
// names, so it must run after the file's imports were loaded.
func (a *galaAnalyzer) qualifiersForFile(sf *grammar.SourceFileContext, richAST *transpiler.RichAST) fileQualifiers {
	q := fileQualifiers{named: make(map[string]importBinding)}
	for _, imp := range scanFileImports(sf) {
		b := importBinding{Path: imp.Path, IsGala: a.isGalaImport(imp.Path)}
		names := imp.LocalNames()
		if b.IsGala {
			b.PkgName = richAST.Packages[imp.Path]
			if imp.Alias == "" && b.PkgName != "" {
				names = []string{b.PkgName}
			}
		} else {
			b.PkgName = imp.LocalName()
		}
		if imp.IsDot {
			q.dots = append(q.dots, b)
			continue
		}
		for _, name := range names {
			if _, taken := q.named[name]; !taken && name != "" && name != "_" {
				q.named[name] = b
			}
		}
	}
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

// withGoImportPath records on a package-qualified type written in this file
// the path of the Go import its qualifier is bound to, so the type stays tied
// to that import — `*strings.Builder` after `import "strings"` is Go's type
// even when a GALA package named `strings` is loaded too. Other types are
// returned unchanged.
func (q fileQualifiers) withGoImportPath(t transpiler.Type) transpiler.Type {
	nt, ok := t.(transpiler.NamedType)
	if !ok || nt.ImportPath != "" || nt.Package == "" {
		return t
	}
	if path, ok := q.goImport(nt.Package); ok {
		nt.ImportPath = path
		return nt
	}
	return t
}
