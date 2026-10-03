package analyzer

import (
	"fmt"
	"go/token"
	"slices"
	"sort"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// A package-level name that a dot import also brings in
//
// A name the package declares shadows the same name of an import: std's `Seq`
// means the package's own `Seq` once the package declares one. std is imported
// under its name in generated Go, so the two never meet there.
//
// A dot import is a Go dot import, though, and Go puts the names it
// brings in into the file block, where no package-level declaration may also
// be: `List already declared through dot-import of package
// collection_immutable`. That holds for a dot import in ANY file of the
// package, since the package block spans them all. The transpiler cannot
// shadow the imported name without giving up the dot import itself, so the
// declaration is reported here, against the source, rather than left to
// `go build`.

// checkDotImportCollisions reports the first top-level type, sealed variant
// or function that sf declares under a name one of the package's dot imports
// also exports. dotPkgs are the names of the packages, GALA or Go, that any
// file of the package dot-imports; the implicit std import is not among them,
// since it is not a dot import in generated Go.
func checkDotImportCollisions(sf *grammar.SourceFileContext, dotPkgs map[string]bool, richAST *transpiler.RichAST) error {
	if len(dotPkgs) == 0 || richAST == nil {
		return nil
	}
	pkgs := make([]string, 0, len(dotPkgs))
	for pkg := range dotPkgs {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	exporter := func(name string) string {
		for _, pkg := range pkgs {
			key := pkg + "." + name
			if _, ok := richAST.Types[key]; ok {
				return pkg
			}
			if _, ok := richAST.Functions[key]; ok {
				return pkg
			}
			if slices.Contains(richAST.GoExports[pkg], name) {
				return pkg
			}
		}
		return ""
	}
	check := func(kind string, id grammar.IIdentifierContext) error {
		if id == nil {
			return nil
		}
		name := id.GetText()
		// A dot import brings in exported names only.
		if !token.IsExported(name) {
			return nil
		}
		pkg := exporter(name)
		if pkg == "" {
			return nil
		}
		return dotImportCollisionError(kind, name, pkg, id.GetStart())
	}
	for _, top := range sf.AllTopLevelDeclaration() {
		var err error
		switch {
		case top.TypeDeclaration() != nil:
			err = check("type", top.TypeDeclaration().(*grammar.TypeDeclarationContext).Identifier())
		case top.StructShorthandDeclaration() != nil:
			err = check("type", top.StructShorthandDeclaration().(*grammar.StructShorthandDeclarationContext).Identifier())
		case top.SealedTypeDeclaration() != nil:
			sealed := top.SealedTypeDeclaration().(*grammar.SealedTypeDeclarationContext)
			err = check("type", sealed.Identifier())
			for _, c := range sealed.AllSealedCase() {
				if err == nil {
					err = check("sealed variant", c.(*grammar.SealedCaseContext).Identifier())
				}
			}
		case top.FunctionDeclaration() != nil:
			fn := top.FunctionDeclaration().(*grammar.FunctionDeclarationContext)
			if fn.Receiver() == nil {
				err = check("function", fn.Identifier())
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func dotImportCollisionError(kind, name, pkg string, tok antlr.Token) error {
	msg := fmt.Sprintf("%s %s is also exported by %s, which this package dot-imports", kind, name, pkg)
	hint := fmt.Sprintf("rename %s, or import %s under a name instead of `.`; "+
		"Go allows no package-level name that a dot import also brings in", name, pkg)
	return galaerr.NewCodedSemanticError(galaerr.CodeDeclarationCollidesWithDotImport, tok.GetLine(), tok.GetColumn(), msg, hint).
		WithSpan(tok.GetColumn() + len([]rune(name)))
}
