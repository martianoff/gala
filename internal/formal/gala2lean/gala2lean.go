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
//   - A variadic parameter is a Lean List of its arguments, which may only be
//     spread or sized (variadic.go).
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
	"slices"
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
	// Deps are the packages of the program's module that it imports,
	// directly or through each other (packages.go).
	Deps []Package
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
	// Generic are the Lean names of the program's generic types and
	// functions, whose type arguments Lean infers.
	Generic []string
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
	for _, d := range opts.Deps {
		if d.Program.Core != coreexport.Version {
			return nil, fmt.Errorf("gala2lean: Core JSON version %d of %s, want %d", d.Program.Core, d.Path, coreexport.Version)
		}
	}
	p, depUnsup, depNS := mergePackages(p, opts.Deps)
	p = hideLibNames(p)
	p = qualifyGoImports(p)
	p = ptrReceivers(p)
	p, placeholders := lowerPlaceholders(p)
	g := newGen()
	g.depNS = depNS
	g.ownPtrs = ownPtr(p)
	g.placeholders = placeholders
	g.library = opts.Library
	g.ns = opts.Namespace
	g.unsup = depUnsup
	g.collect(p)
	g.findBoxes(p)
	// Every declaration is translated even after one failed, so that every
	// construct outside the subset is reported.
	g.emitProgram(opts)
	g.attributeReach(p)
	// After the constructs in declarations, which name what stops a program
	// more precisely than its imports do.
	g.unsup = append(g.unsup, importsOutside(p, opts.Deps)...)
	if len(g.unsup) > 0 {
		return nil, &UnsupportedError{Items: g.unsup}
	}
	return &Result{Lean: g.out.String(), Decls: g.decls, Generic: g.genericNames()}, nil
}

// noOutputInitImports are the packages whose import alone stops nothing: Go
// standard-library packages, and GALA's std and collection_immutable, which
// the Go of any program using Option or Array imports whether or not the
// source does, so both models already run such programs without their
// initializers. Assumption, not modelled: none of them prints while it
// initializes, so leaving their initializers out prints what Go prints
// (for the two GALA packages, TestQuietInitPackages checks it). A name of
// one is still reported where the program uses it outside what is modelled
// (import). Exact paths only: a module package's path carries its module.
// galaspec makes the same assumption for the same list
// (Json.noOutputInitImports in formal/Gala/Json.lean;
// TestNoOutputInitImportsInStep keeps them equal).
var noOutputInitImports = []string{
	"bytes", "errors", "fmt", "math", "os", "sort", "strconv", "strings", "sync", "time",
	"unicode", "unicode/utf8", "martianoff/gala/std", "martianoff/gala/collection_immutable",
}

// importsOutside reports each import of the merged program p, a dependency
// file's included, of a package the translation is not given (not among
// deps): a blank import (blank-import), and a named one unless its package
// is one of noOutputInitImports (import). Go initializes every imported
// package, before the importer, whether or not a declaration the program
// reaches uses the import; without the package the translation cannot run
// its initializers, and would print without their output. An import of a
// package in deps is translated: its initialization is ordered like any
// other import's (importsOf).
func importsOutside(p *coreexport.Program, deps []Package) []Unsupported {
	known := map[string]bool{}
	for _, d := range deps {
		known[d.Path] = true
	}
	var out []Unsupported
	for _, f := range p.Files {
		for _, d := range f.Decls {
			imp, ok := d.(*coreexport.Import)
			switch {
			case !ok || known[imp.Path]:
			case imp.Alias == "_":
				out = append(out, Unsupported{Construct: "blank-import", Pos: imp.Pos})
			case !slices.Contains(noOutputInitImports, imp.Path):
				out = append(out, Unsupported{Construct: "import", Pos: imp.Pos})
			}
		}
	}
	return out
}

// unsupportedPanic carries an unsupported construct out of the recursive
// translation of one declaration.
type unsupportedPanic struct {
	construct string
	pos       coreexport.Pos
}

func unsup(pos coreexport.Pos, format string, a ...any) {
	// A renamed library name (hideLibNames) is reported as written.
	panic(unsupportedPanic{unhide(fmt.Sprintf(format, a...)), pos})
}

// guard runs f, the translation of the declaration named decl, and records
// the construct it stopped at, if any. It reports whether f finished.
func (g *gen) guard(decl string, f func()) bool {
	// A translation stopped midway leaves no state behind for the next.
	g.qualify, g.inDefault, g.tparams, g.cmpParams = false, map[string]bool{}, nil, nil
	g.cur = decl
	_, u := attempt(func() struct{} { f(); return struct{}{} })
	if u != nil {
		g.unsup = append(g.unsup, Unsupported{Decl: decl, Construct: u.construct, Pos: u.pos})
	}
	return u == nil
}

// attempt runs f and returns its result, or the construct outside the
// subset it stopped at. Any other panic goes on.
func attempt[T any](f func() T) (v T, u *unsupportedPanic) {
	defer func() {
		if r := recover(); r != nil {
			p, ok := r.(unsupportedPanic)
			if !ok {
				panic(r)
			}
			u = &p
		}
	}()
	return f(), nil
}
