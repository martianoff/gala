package transformer

import (
	"go/ast"

	"martianoff/gala/galaerr"
)

// A `_` read as a value that nothing binds (no placeholder lambda, parameter,
// pattern or bind clause) is lowered as Go's blank identifier, which Go cannot
// read. Whether it stays one is decided only after lowering: an argument may
// be lowered on trial before it is known to be a placeholder lambda, and that
// trial lowering is then discarded. So such a read is recorded here as it is
// lowered, and reported by checkBlankReads only if it is in the finished file.

// blankRead is where an unbound `_` is written: at, in file, or in the file
// being transformed when file is "".
type blankRead struct {
	at   srcRange
	file string
}

// recordBlankRead records ident, the lowering of an unbound `_` written at at.
// A `_` in a default lowered at its use site is written in the default's file.
func (t *galaASTTransformer) recordBlankRead(ident *ast.Ident, at srcNode) {
	if t.blankReads == nil {
		t.blankReads = make(map[*ast.Ident]blankRead)
	}
	read := blankRead{at: at.rng()}
	if d := t.loweringDefault; d != nil && d.src.file != t.filePath {
		read.file = d.src.file
	}
	t.blankReads[ident] = read
}

// before reports whether r is reported before o: one in the file being
// transformed first, then by file and source order.
func (r blankRead) before(o blankRead) bool {
	if r.file != o.file {
		return r.file == "" || o.file != "" && r.file < o.file
	}
	return r.at.line < o.at.line || r.at.line == o.at.line && r.at.col < o.at.col
}

// checkBlankReads is GALA-E0139 at the first unbound `_` read that reached
// the finished file. A placeholder lambda renames the `_` it takes to its
// parameter, so a recorded ident still named `_` is a read.
func (t *galaASTTransformer) checkBlankReads(file *ast.File) error {
	if len(t.blankReads) == 0 {
		return nil
	}
	var first *blankRead
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != "_" {
			return true
		}
		if r, ok := t.blankReads[id]; ok && (first == nil || r.before(*first)) {
			first = &r
		}
		return true
	})
	if first == nil {
		return nil
	}
	err := spanErrorAt(galaerr.CodeUnderscoreBindingRead, first.at, first.at,
		"`_` is read here, but nothing binds it: `_` names no value",
		"bind a name and read it instead; for example an explicit lambda `(x) => g(x)`")
	if se, ok := err.(*galaerr.SemanticError); ok && first.file != "" {
		se.FilePath = first.file
	}
	return err
}
