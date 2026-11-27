package analyzer

import (
	"fmt"
	"go/token"
	"maps"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// dotImportCollisions reports GALA-E0066 for the first top-level name root, a
// file's native tree, declares (forEachTopLevelNameNative) that a package
// dot-imported by any file of the package also exports. The package's own
// declarations shadow its imports' names, std's included, but a dot import is
// a Go dot import, and Go allows no package-level name that a dot import in
// any file of the package also brings in. dotImports maps each dot-imported
// GALA package's name to its import paths; the implicit std import is not a
// dot import and is not among them. goDots holds each dot-imported Go
// package by its import path: its names are its own exports, never the GALA
// metadata of a package of its name (GALA `strings` beside Go's).
//
// The check reads the import tables as written, before the transformer
// prunes dot imports it finds unused, so a leftover dot import counts too:
// whether one is kept depends on the bare names the file uses, and the
// colliding declaration is such a name.
func dotImportCollisions(root native.GalaNodeRef, dotImports map[string][]string, goDots map[string]goDotImport, richAST *transpiler.RichAST) error {
	if len(dotImports) == 0 && len(goDots) == 0 || richAST == nil {
		return nil
	}
	pkgs := slices.Sorted(maps.Keys(dotImports))
	goPaths := slices.Sorted(maps.Keys(goDots))
	exporter := func(name string) string {
		for _, pkg := range pkgs {
			key := pkg + "." + name
			_, isType := richAST.Types[key]
			_, isFunc := richAST.Functions[key]
			isVal := slices.ContainsFunc(dotImports[pkg], func(path string) bool {
				_, ok := richAST.ImportedVals[path][name]
				return ok
			})
			// GoExports holds the names of a GALA package's own .go files.
			if isType || isFunc || isVal || slices.Contains(richAST.GoExports[pkg], name) {
				return pkg
			}
		}
		for _, path := range goPaths {
			if slices.Contains(goDots[path].exports, name) {
				return goDots[path].label
			}
		}
		return ""
	}
	var err error
	forEachTopLevelNameNative(root, topLevelVisitor{name: func(name string, at transpiler.SourcePos, kind string) {
		// A dot import brings in exported names only.
		if err != nil || !token.IsExported(name) {
			return
		}
		if pkg := exporter(name); pkg != "" {
			err = dotImportCollisionError(kind, name, pkg, at)
		}
	}})
	return err
}

// goDotImport is a Go package a file of the package dot-imports: the label
// an error names it by, and the exported names it declares.
type goDotImport struct {
	label   string
	exports []string
}

// goDotImportOf is the Go package b dot-imports, named by the name the
// file's tables give it, else its type info's, else its import path; and
// its exports (analyzeGoImport), which a sibling file's tables do not hold.
func (a *galaAnalyzer) goDotImportOf(b importBinding) goDotImport {
	info, exports := a.analyzeGoImport(b.Path)
	label := b.PkgName
	if label == "" {
		label = goPackageName(info, b.Path)
	}
	if label == "" {
		label = b.Path
	}
	return goDotImport{label: label, exports: exports}
}

func dotImportCollisionError(kind, name, pkg string, at transpiler.SourcePos) error {
	msg := fmt.Sprintf("%s %s is also exported by %s, which this package dot-imports", kind, name, pkg)
	hint := fmt.Sprintf("rename %s, or import %s under a name instead of `.`; "+
		"Go allows no package-level name that a dot import also brings in", name, pkg)
	return galaerr.NewCodedSemanticError(galaerr.CodeDeclarationCollidesWithDotImport, at.Line, at.Column, msg, hint).
		WithSpan(at.Column + len([]rune(name)))
}
