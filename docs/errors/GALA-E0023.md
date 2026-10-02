# GALA-E0023 — Undefined symbol

**When it fires.** An identifier used in *value position* — a variable
read, a call target, a bare function reference — resolves to nothing.
The analyzer walks each file with a scope chain and checks every such
identifier against the complete symbol table it built for that file:
enclosing bindings, the current package's declarations (including
sibling files'), the exports of the packages this file dot-imports, the
implicitly dot-imported `std` prelude, package qualifiers (a package
imported by name is reached through its qualifier), and the language
builtins. A name that matches none of them is rejected here rather than
being deferred to the Go compiler.

Common causes:

- The name is misspelled.
- The import that introduces it is missing.
- The binding is out of scope at the reference site (declared in a
  narrower block, or in a sibling file's function body rather than at
  package level).

**Error output.** Two shapes, depending on whether the analyzer can
find a package that would supply the name.

The name is declared by exactly one package on the search paths:

```
[SemanticError GALA-E0023] line 4:12 undefined: SliceOf (hint: SliceOf is declared in the GALA package "martianoff/gala/go_interop", which this file does not import. Add `import . "martianoff/gala/go_interop"` to use it unqualified, or `import "martianoff/gala/go_interop"` and call it as `go_interop.SliceOf`.)
```

The name is declared by several packages, so the choice is the author's:

```
[SemanticError GALA-E0023] line 4:11 undefined: ArrayTabulate (hint: ArrayTabulate is declared in these GALA packages, none of which this file imports: "martianoff/gala/collection_immutable", "martianoff/gala/collection_mutable". Add `import . "<the one you want>"` to use it unqualified, or import it plainly and qualify the call.)
```

Nothing on the search paths declares the name:

```
[SemanticError GALA-E0023] line 4:12 undefined: x (hint: check the spelling, add the import that introduces this name, or declare it — every identifier must resolve to a binding, a declaration in this package, or an imported symbol)
```

**Fix.** Add the import the hint names, correct the spelling, or
declare the symbol.

**Rationale.** Before this check existed, an unresolved name produced
no metadata, so type inference fell back to an unconstrained variable
and the transformer emitted a bare Go identifier. Two bad outcomes
followed. A missing collection import silently erased a lambda
parameter to `any` — `func(i any) string` where `func(i int) string`
was meant — violating the concrete-types invariant with no diagnostic
at all. And an outright typo surfaced only as `undefined: x` from the
Go compiler, pointed at generated code rather than the `.gala` line the
author wrote.

This check closes both outcomes for a name in **value position**,
including inside an interpolated string, and for a name written as a
**type**: a parameter, result, struct field, type argument or `val`/`var`
annotation. See *Type names* below.

**Scope.** Analyzer post-pass, run once per top-level file after all
metadata for the file, its siblings and its imports has been collected
(`internal/transpiler/analyzer/undefined_symbol.go`). The traversal
itself is the shared lexical-scope walker in
`internal/transpiler/scopewalk`, which also backs the concurrency
capture analysis; this pass supplies the symbol table and decides what
an unbound reference means. It is an *existence* check only: whether
the resolved symbol is used at a sensible type is not its concern.

Identifiers inside interpolated strings (`s"…$x…"`, `f"${x + y}"`) **are**
checked. Such a literal is a single lexer token, so the walker
re-parses each embedded expression and walks it in the enclosing scope.

**A bare GALA name must be in this file's scope.** The symbol table
holds every package the import graph reached, not only this file's
imports: the GALA `strings` package imports the collection packages for
its own use, so a file importing only `strings` still loads them. A bare
name that only such packages declare is reported. It must come from
this file's own package (any file of it), a package this file
dot-imports, or the `std` prelude. The same rule applies in type
position, so `func total(xs Array[int])` in that file is reported at
`Array`. A package imported by name is reached through its qualifier,
and the hint says so:

```gala
package main

import "martianoff/gala/collection_immutable"

func main() {
    Println(ArrayOf(1, 2))
}
```

```
[SemanticError GALA-E0023] line 6:12 undefined: ArrayOf (hint: ArrayOf is declared in a package this file imports by name; call it as `collection_immutable.ArrayOf`, or dot-import that package to use it unqualified.)
```

A sibling file's dot import does not carry over. The one allowance is
the bare-name form of the one [GALA-E0025](GALA-E0025.md) makes: the
signature of a method whose receiver type is declared in another file
may use names that file dot-imports. The method body gets no allowance,
and neither does that file's named imports. Bare *Go* names are not held
to this rule, because the Go compiler rejects one that no dot import
provides.

[GALA-E0025](GALA-E0025.md) covers the remaining import question: a
signature type that resolved to a package this file never imported.

**Type names.** Type names are checked too: in a parameter, result,
struct field, type argument, lambda parameter or `val`/`var` annotation,
an alias target (`type Coord Point`), a type parameter's constraint
(`[T Number]`) and an unnamed parameter of a function type
(`func(Point) int`).

- A **qualified** type is checked at its qualifier: `var sb
  strings.Builder` in a file that never imports `strings` is reported
  at `strings`. The member (`Builder`) is not checked, because that
  would need the full type surface of every imported Go package.
- An **unqualified** type name must exist. It resolves when it is a Go
  predeclared type (`int`, `error`, `any`, …) or a type parameter the
  file declares, including the names a method receiver binds
  (`func (b Box[T]) ...`). It also resolves when it is a type declared by
  any of these:
  - this package, in any of its `.gala` files or hand-written `.go`
    files, or inside a function body;
  - a package this file dot-imports, GALA or Go;
  - the `std` prelude.

  A function or value of the same name does not count; it is reported as
  `ArrayOf is not a type`. Neither does a Go type this file reaches only
  through a qualifier: under `import "time"`, `func wait(d Duration)` is
  reported, and the hint says to write `time.Duration`. The wildcard
  `_` (`case a: Array[_]`, `(x _) => x`) is left to the transpiler.
  A GALA type must also pass the scope rule above. The name is reported
  at its first use in the file, whether that use is a type or a value.
  When a GALA package on the search paths declares it, the hint names
  the import:

```gala
package main

func total(xs Array[int]) int = xs.FoldLeft(0, (acc, x) => acc + x)
```

```
[SemanticError GALA-E0023] line 3:14 undefined: Array (hint: Array is declared in these GALA packages, none of which this file imports: "martianoff/gala/collection_immutable", "martianoff/gala/collection_mutable". Add `import . "<the one you want>"` to use it unqualified, or import it plainly and qualify the call.)
```

Before this check, such a name transpiled and failed only at `go build`
with `undefined: Array` against the generated code. A collection type
was worse: the body's lambda had already been erased to
`func(acc any, x any) any`.

Type parameters and types declared in a function body are recognised
file-wide rather than per scope. That can only hide a report, never
create one.

Not covered. Each of these is a deliberate trade of a missed detection
for a guaranteed absence of false positives:

- **The member of a qualified type.** `strings.Builderr` is checked only
  at `strings`; see *Type names* above.
- **An unqualified type name in a file that dot-imports a Go package**
  is not checked for existence. Go type information comes from the
  host's build context, so it lacks the types only another platform's
  files declare (`Termios` under `import . "syscall"` on Windows), and
  the name may be one of those. The scope rule above still applies.
- **A Go function or value used as a type,** when it comes from the
  hand-written Go of this package or of a dot-imported GALA package: the
  scan of those files records names without telling types apart.
- **Selectors.** In `x.foo().bar`, only `x` is checked — field and
  method names require the receiver's type.
- **Constructor names in `match` / `case` patterns.** A pattern's
  binding names (`case Some(x)` → `x`) are bound; the constructor or
  extractor it names is neither bound nor checked, because deciding
  which is which in general needs the scrutinee's type. A typo in a
  pattern's constructor position is therefore not caught here. The
  arm's *body* is checked normally, against exactly the names the
  pattern introduces.
- **Any package that failed to load, anywhere in the graph.** If a GALA
  package could not be analyzed — missing from a search path, a
  transpile failure, whatever the reason — the check stands down for
  every file in the compilation, not just files that import it
  directly. It has to be that broad: the missing package is usually one
  a *dependency* imported. `std` dot-imports `go_builtins`, so a file
  that imports nothing at all still resolves bare `Panic` through
  std's closure; if `go_builtins` did not load, that name goes missing
  with nothing in the file to hint why, and reporting it would blame
  the author for an environmental failure. The same applies to a *dot* import of a Go
  package that contributed no symbols at all — dot-importing is what
  makes a Go package's exports reachable unqualified, so they must be
  enumerable. They normally are (via Go type info), and then the check
  stays fully live; they are not when no Go SDK is on PATH. A **named**
  Go import never disables the check.

  One residue of that rule is worth knowing when debugging why the
  check did not fire in your file. Go metadata is keyed by package
  *name*, which the analyzer takes to be the import path's last
  segment. A package whose declared name differs from its final path
  element — `gopkg.in/yaml.v3` declaring `package yaml` is the
  canonical case — therefore reads as "contributed nothing", and
  dot-importing it stands the check down **for the whole file**. This
  is the safe direction (it can only miss a detection, never invent
  one), but it means a single such dot import silently disables every
  check on this page for that file.
- **The language server.** The LSP runs the analyzer for best-effort
  metadata, and a hard error there drops the whole file's `RichAST` —
  taking completion, hover and go-to-definition with it, while the
  author is mid-keystroke and the name legitimately does not exist
  yet. The check is disabled for the LSP analyzer; surfacing it as a
  non-fatal editor diagnostic needs `Analyze` to return partial
  results alongside errors.
- **Names other codes own.** The Go builtins of
  `GALA-E0035` and the Go statement keywords of
  `GALA-E0036` keep their own, more specific
  diagnostics. So do the Go names GALA has not yet classified but that
  its own sources use — `break`, `continue`, `println`, `print` — which
  are accepted here rather than pre-empting that decision.
- **Generated method forms.** `Array_FoldLeft`, `Some_Apply` and the
  like exist only after transformation (Go forbids a method from
  introducing its own type parameters, so generic and synthesized
  methods are emitted as top-level functions). A name whose prefix
  before an underscore is a known type is accepted, so a misspelling
  *after* the underscore is left to the Go compiler.

**Previously documented gaps that are now closed.** The traversal was
originally written specifically for this check and had blind spots that
an earlier revision of this page listed. Moving it onto the shared
`scopewalk` walker closed four of them, each now covered by a test:

- interpolated-string bodies (`s"${missing(3)}"`), which the walker
  re-parses;
- lambda parameter defaults (`(x = missing) => x`), which are now
  walked like a function declaration's;
- the assigning form of `range` (`for i, v = range xs`), whose loop
  variables are references rather than fresh bindings;
- the file-wide stand-down on any Go dot import, now narrowed to a Go
  dot import that contributed no symbols at all.

The inference engine (`internal/transpiler/infer`) also raises this
code for a name with no binding in its type environment, but its
diagnostics are advisory — the bridge that feeds it approximates
selectors, methods and generics, so callers deliberately discard its
errors. The analyzer pass above is the authoritative source.
