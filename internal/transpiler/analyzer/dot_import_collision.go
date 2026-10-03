package analyzer

import (
	"fmt"
	"go/token"
	"maps"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// checkDotImportCollisions reports GALA-E0066 for the first top-level name sf
// declares that a package dot-imported by any file of the package also
// exports. The package's own declarations shadow its imports' names, std's
// included, but a dot import is a Go dot import, and Go allows no
// package-level name that a dot import in any file of the package also
// brings in. dotImports maps each dot-imported package's name to its import
// path; the implicit std import is not a dot import and is not among them.
func checkDotImportCollisions(sf *grammar.SourceFileContext, dotImports map[string]string, richAST *transpiler.RichAST) error {
	if len(dotImports) == 0 || richAST == nil {
		return nil
	}
	pkgs := slices.Sorted(maps.Keys(dotImports))
	exporter := func(name string) string {
		for _, pkg := range pkgs {
			key := pkg + "." + name
			_, isType := richAST.Types[key]
			_, isFunc := richAST.Functions[key]
			_, isVal := richAST.ImportedVals[dotImports[pkg]][name]
			if isType || isFunc || isVal || slices.Contains(richAST.GoExports[pkg], name) {
				return pkg
			}
		}
		return ""
	}
	var err error
	forEachTopLevelName(sf, func(id grammar.IIdentifierContext, kind string) {
		name := id.GetText()
		// A dot import brings in exported names only.
		if err != nil || !token.IsExported(name) {
			return
		}
		if pkg := exporter(name); pkg != "" {
			err = dotImportCollisionError(kind, name, pkg, id)
		}
	})
	return err
}

func dotImportCollisionError(kind, name, pkg string, id grammar.IIdentifierContext) error {
	tok := id.GetStart()
	msg := fmt.Sprintf("%s %s is also exported by %s, which this package dot-imports", kind, name, pkg)
	hint := fmt.Sprintf("rename %s, or import %s under a name instead of `.`; "+
		"Go allows no package-level name that a dot import also brings in", name, pkg)
	return galaerr.NewCodedSemanticError(galaerr.CodeDeclarationCollidesWithDotImport, tok.GetLine(), tok.GetColumn(), msg, hint).
		WithSpan(tok.GetColumn() + len([]rune(name)))
}
