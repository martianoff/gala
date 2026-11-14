// Package gala2lean translates a GALA program, as Core JSON
// (internal/formal/coreexport), into ordinary Lean 4 definitions: a
// deterministic, mechanically derived Lean model of the program that can be
// run and reasoned about (docs/FORMAL_SEMANTICS.MD, "gala2lean").
//
// It is a third account of what a program means, next to galaspec (the
// hand-written reference semantics) and the real transpiler and Go. The
// differential harness (internal/formal/difftest) compares all three.
//
// The translation is a shallow embedding. Every rule is mechanical:
//
//   - int and int64 are Int64 (Go's wrapping int64), string is String, bool
//     is Bool; a tuple is the prelude's T2, T3 or T4; a struct is a Lean
//     structure and a sealed type an inductive, both deriving BEq.
//   - Every function is a Lean def in the monad Gala.Gen.Prog St: the state
//     St holds the printed output and the package-level val/var, a GALA panic
//     is an exception that keeps the state, and divergence is Option's none.
//     A recursive function (or a group of mutually recursive ones) is defined
//     with partial_fixpoint, so it has equations to prove things with; no
//     definition is `partial`.
//   - Expressions become monadic code whose `(← e)` lifts run in GALA's
//     strict left-to-right order; `&&`, `||`, `if` and match arms keep their
//     laziness by running in nested `do` blocks.
//   - A local var is a `let mut`; assigning a struct field rebuilds the
//     struct.
//   - `match` tests its arms in order. Literal and stable-identifier
//     patterns are equality tests after Lean's own pattern match; a guard
//     runs only after its pattern matched; no matching arm is the match
//     panic.
//   - Package-level val/var are initialized in Go's dependency order before
//     main runs.
//
// A construct outside the translated subset is reported, with the
// construct's name and position (UnsupportedError); nothing is ever left out
// of the translation silently.
//
// The package is test support: like coreexport, no part of the gala CLI
// links it. The Lean it writes imports the trusted prelude,
// formal/Gala/Gala2Lean/Prelude.lean.
package gala2lean

import (
	"fmt"
	"sort"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// PreludeModule is the Lean module every translated program imports.
const PreludeModule = "Gala.Gala2Lean.Prelude"

// Options configure a translation.
type Options struct {
	// Namespace is the Lean namespace the program's definitions go in, for
	// example "Gala.Gala2Lean.Examples.Match". Required.
	Namespace string
	// Main appends a root `def main : IO UInt32` that runs the program and
	// prints its output and outcome (Gala.Gen.runMain), so the module can be
	// run with `lean --run`.
	Main bool
	// Library translates a package without a main: the package
	// initialization then runs no main. For measuring how much of a library
	// translates.
	Library bool
}

// Unsupported is one construct the translation does not cover.
type Unsupported struct {
	// Decl is the top-level declaration it is in ("" for the program as a
	// whole, such as a missing main).
	Decl string
	// Construct names what is not translated: a Core JSON unsupported
	// name (import, method, ...) or one of gala2lean's own (lambda,
	// return-inside-expression, ...).
	Construct string
	// Pos is where it is, when known.
	Pos coreexport.Pos
}

func (u Unsupported) String() string {
	where := ""
	if u.Pos.Line > 0 {
		where = fmt.Sprintf(" at %d:%d", u.Pos.Line, u.Pos.Col)
	}
	if u.Decl != "" {
		return fmt.Sprintf("%s (in %s%s)", u.Construct, u.Decl, where)
	}
	return u.Construct + where
}

// UnsupportedError lists every construct a program uses outside the
// translated subset: the first one of each top-level declaration, in source
// order.
type UnsupportedError struct {
	Items []Unsupported
}

func (e *UnsupportedError) Error() string {
	parts := make([]string, len(e.Items))
	for i, it := range e.Items {
		parts[i] = it.String()
	}
	return "gala2lean: not translated: " + strings.Join(parts, "; ")
}

// Construct is the name of the first construct not translated, the one a
// report names.
func (e *UnsupportedError) Construct() string { return e.Items[0].Construct }

// Decl is one top-level Lean definition of the output and the lines it spans
// (from 1), so that an error Lean reports can be put down to the GALA
// declaration it came from.
type Decl struct {
	// Name is the GALA name: a function, or "<init>" for the package
	// initialization and main.
	Name string
	// Recursive is true for a definition by partial_fixpoint.
	Recursive  bool
	First, End int
}

// Result is a translated program.
type Result struct {
	// Lean is the module's source text.
	Lean string
	// Decls are the function definitions in it, in output order.
	Decls []Decl
}

// DeclAt is the declaration that holds line (from 1), or nil.
func (r *Result) DeclAt(line int) *Decl {
	for i := range r.Decls {
		if d := &r.Decls[i]; d.First <= line && line <= d.End {
			return d
		}
	}
	return nil
}

// Translate translates a Core JSON program. The output depends only on p and
// opts: the same input gives byte-identical Lean.
func Translate(p *coreexport.Program, opts Options) (*Result, error) {
	if opts.Namespace == "" {
		return nil, fmt.Errorf("gala2lean: no namespace")
	}
	if p.Core != coreexport.Version {
		return nil, fmt.Errorf("gala2lean: Core JSON version %d, want %d", p.Core, coreexport.Version)
	}
	g := newGen()
	g.library = opts.Library
	g.collect(p)
	// Every declaration is translated even after one failed, so that every
	// construct outside the subset is reported.
	g.emitProgram(opts)
	if len(g.unsup) > 0 {
		return nil, &UnsupportedError{Items: g.unsup}
	}
	return &Result{Lean: g.out.String(), Decls: g.decls}, nil
}

// unsupportedPanic carries an unsupported construct out of the recursive
// translation of one declaration.
type unsupportedPanic struct {
	construct string
	pos       coreexport.Pos
}

func unsup(pos coreexport.Pos, format string, a ...any) {
	panic(unsupportedPanic{fmt.Sprintf(format, a...), pos})
}

// guard runs f, the translation of the declaration named decl, and records
// the construct it stopped at, if any. It reports whether f finished.
func (g *gen) guard(decl string, f func()) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			u, isUnsup := r.(unsupportedPanic)
			if !isUnsup {
				panic(r)
			}
			g.unsup = append(g.unsup, Unsupported{Decl: decl, Construct: u.construct, Pos: u.pos})
			ok = false
		}
	}()
	f()
	return true
}

// sortedKeys is m's keys in order, for deterministic output.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
