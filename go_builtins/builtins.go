// Package go_builtins is a dependency-free leaf package that exposes the
// handful of Go builtin primitives GALA still needs internally but no longer
// accepts as bare identifiers in source.
//
// It exists because bare Go builtins (append, make, new, panic, ...) are a
// hard transpile error in GALA: every symbol must be defined locally, declared
// in std, or imported. `std` itself needs `panic` (Option.Get on None, etc.),
// so the primitive must live in a package std can import. `go_interop` sits
// ABOVE std (it imports std), so putting Panic there would create a
// std -> go_interop -> std cycle. A dedicated leaf package with NO dependencies
// breaks that knot: std imports it like any other library.
//
// This package is NOT auto-imported; import it explicitly:
//
//	import . "martianoff/gala/go_builtins"
package go_builtins

// Panic raises a Go panic carrying v. It is the sanctioned replacement for the
// bare `panic` builtin, which is not part of GALA's surface. Prefer
// Option / Try / Either for recoverable failure and reserve Panic for truly
// unrecoverable invariants.
//
// Panic is void. In a statement position (`if cond { Panic(x) }`) it lowers to
// a plain call. In a no-return tail position — a match-arm tail or an
// expression-bodied function body, where Go's terminating-statement analysis
// requires a construct it recognizes as diverging — the transpiler lowers the
// wrapper to Go's builtin `panic(x)` directly (a void call to Panic would fail
// there with "missing return"). Either way bare `panic` never appears in GALA
// source.
func Panic(v any) {
	panic(v)
}
