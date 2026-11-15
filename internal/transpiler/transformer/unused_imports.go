package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/interpolation"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// An import the file writes and never refers to is an error (GALA-E0120), as
// in Go. Without it the import was dropped from the generated Go, so the
// package's initialization — its package-level vals — silently did not run.
// `import _ "path"` is the way to import a package only for that.
//
// Use is decided on the source alone, never on the generated Go: an import is
// used when the file names it — a qualifier of a named import followed by
// `.`, in any position (value, call, type, pattern, interpolated string), or,
// for a dot import, one of the package's exported names (sourceNames). So a
// package named only where the Go leaves it out (a type annotation, a
// pattern) is used, and one the generated Go refers to without the source
// naming it (the code std's lowering emits, a method of a value whose type is
// the package's) is not: GALA adds the imports its generated code needs by
// itself. The reading over-approximates use (a local named like a qualifier
// counts), so it can miss an unused import, but does not report a used one.
// Imports the transpiler adds itself are not written in the source and are
// never checked.

// writtenImport is an import spec written at the top of the file: as read,
// as lowered, and the import it added (nil when it added none).
type writtenImport struct {
	decl  importSpecDecl
	spec  *ast.ImportSpec
	entry *ImportEntry
}

// writtenSpecs is the lowered spec of each of written.
func writtenSpecs(written []writtenImport) map[*ast.ImportSpec]bool {
	specs := make(map[*ast.ImportSpec]bool, len(written))
	for _, w := range written {
		specs[w.spec] = true
	}
	return specs
}

// checkUnusedImports is GALA-E0120 for the first of written, the file's
// import specs, that the source does not refer to. imports are the file's
// import declarations.
func (t *galaASTTransformer) checkUnusedImports(written []writtenImport, src walkSource, imports []importDecl) error {
	var names *sourceNames // read on the first import that is not blank
	for _, w := range written {
		if w.entry == nil || w.decl.kind == importAliased && w.decl.alias == "_" {
			continue
		}
		if names == nil {
			names = readSourceNames(src.root.GalaNodeRef, imports)
		}
		if t.sourceUses(w, names) {
			continue
		}
		return t.unusedImportError(w)
	}
	return nil
}

// sourceUses reports whether the source refers to the package of w: through
// any name the import binds followed by `.`, or, for a dot import, through
// one of the package's exported names (any of them, when they are unknown).
func (t *galaASTTransformer) sourceUses(w writtenImport, names *sourceNames) bool {
	if w.entry.IsDot {
		exports := t.importManager.dotImportExports(w.entry.Path, w.entry.PkgName, t.richAST)
		if exports == nil {
			return true
		}
		for name := range names.bare {
			if exportedIn(exports, name) {
				return true
			}
		}
		return false
	}
	return slices.ContainsFunc(t.importManager.NamesOf(w.entry), func(name string) bool { return names.qualifiers[name] })
}

// unusedImportError is GALA-E0120 for w, underlining its spec.
func (t *galaASTTransformer) unusedImportError(w writtenImport) error {
	var msg string
	path := fmt.Sprintf("%q", w.decl.path)
	switch {
	case w.entry.IsDot:
		msg = fmt.Sprintf("unused import . %s: the file uses none of its names", path)
	case w.decl.kind == importAliased:
		msg = fmt.Sprintf("unused import %s %s: the file never refers to %s", w.decl.alias, path, w.decl.alias)
	default:
		msg = fmt.Sprintf("unused import %s: the file never refers to %s", path, w.entry.Alias)
	}
	hint := fmt.Sprintf("remove the import; to run only its initialization, write `import _ %s`", path)
	r := w.decl.at.rng()
	return spanErrorAt(galaerr.CodeUnusedImport, r, r, msg, hint)
}

// sourceNames is the names the file's code writes outside its import
// declarations, the embedded expressions of interpolated strings included.
// A name selected by `.` (the `X` of `a.X`) is in neither set.
type sourceNames struct {
	qualifiers map[string]bool // names followed by `.`: `a` of `a.X`
	bare       map[string]bool // every name written on its own
}

// readSourceNames reads the sourceNames of the file whose root is root;
// imports are its import declarations, which are skipped.
func readSourceNames(root native.GalaNodeRef, imports []importDecl) *sourceNames {
	names := &sourceNames{qualifiers: map[string]bool{}, bare: map[string]bool{}}
	tree := root.Tree()
	skip := map[int]int{} // first token index of an import declaration -> its last
	for _, imp := range imports {
		skip[imp.at.node.StartIndex()] = imp.at.node.StopIndex()
	}
	texts := make([]string, 0, len(tree.Tokens))
	for i := 0; i < len(tree.Tokens); i++ {
		if stop, ok := skip[i]; ok {
			i = stop
			continue
		}
		if tree.Tokens[i].Type == native.GalaEOF {
			break
		}
		texts = append(texts, tree.Text(i))
	}
	names.add(texts)
	return names
}

// add records the names of texts, a run of token texts.
func (n *sourceNames) add(texts []string) {
	for i, text := range texts {
		switch {
		case isInterpolatedLiteral(text):
			n.addInterpolated(text)
		case isName(text):
			if i > 0 && texts[i-1] == "." {
				continue // a selected name: `X` of `a.X`
			}
			n.bare[text] = true
			if i+1 < len(texts) && texts[i+1] == "." {
				n.qualifiers[text] = true
			}
		}
	}
}

// addInterpolated records the names of the embedded expressions of lit, an
// `s"..."` or `f"..."` literal.
func (n *sourceNames) addInterpolated(lit string) {
	for _, part := range interpolation.Split(lit[2:len(lit)-1], lit[0] == 'f') {
		if part.IsLiteral {
			continue
		}
		var texts []string
		parser.VisitNativeTokens(part.Text, func(text string) { texts = append(texts, text) })
		n.add(texts)
	}
}

// isInterpolatedLiteral reports whether text is an `s"..."` or `f"..."` token.
func isInterpolatedLiteral(text string) bool {
	return len(text) >= 3 && (text[0] == 's' || text[0] == 'f') && text[1] == '"'
}

// isName reports whether text is spelled like a name. Go's keywords are
// included: GALA's are not the same, so a GALA name may be one of them.
func isName(text string) bool {
	return token.IsIdentifier(text) || token.Lookup(text).IsKeyword()
}
