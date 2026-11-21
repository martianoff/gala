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

// recordBlankRead records ident, the lowering of an unbound `_` written at at.
// A default of another package is written in another file, which a position
// here cannot name, so a `_` in one is left to Go.
func (t *galaASTTransformer) recordBlankRead(ident *ast.Ident, at srcNode) {
	if t.loweringForeignDefault() {
		return
	}
	if t.blankReads == nil {
		t.blankReads = make(map[*ast.Ident]srcRange)
	}
	t.blankReads[ident] = at.rng()
}

// checkBlankReads is GALA-E0139 at the first unbound `_` read that reached
// the finished file, in source order. A placeholder lambda renames the `_` it
// takes to its parameter, so a recorded ident still named `_` is a read.
func (t *galaASTTransformer) checkBlankReads(file *ast.File) error {
	if len(t.blankReads) == 0 {
		return nil
	}
	var first *srcRange
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != "_" {
			return true
		}
		if r, ok := t.blankReads[id]; ok && (first == nil || r.line < first.line || r.line == first.line && r.col < first.col) {
			first = &r
		}
		return true
	})
	if first == nil {
		return nil
	}
	return spanErrorAt(galaerr.CodeUnderscoreBindingRead, *first, *first,
		"`_` is read here, but nothing binds it: `_` names no value",
		"bind a name and read it instead; for example an explicit lambda `(x) => g(x)`")
}
