# GALA-E0023 — Undefined symbol

**When it fires.** An identifier used in *value position* — a variable
read, a call target, a bare function reference — resolves to nothing.
The analyzer walks each file with a scope chain and checks every such
identifier against the complete symbol table it built for that file:
enclosing bindings, the current package's declarations (including
sibling files'), every imported package's exports, the implicitly
dot-imported `std` prelude, package qualifiers, and the language
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
including inside an interpolated string. It does **not** close them for
a name in **type position**, where the same erasure still reaches the
generated Go and the author still gets the Go compiler's message rather
than a framed one — see the first entry under *Not covered*.

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

The two import-related codes divide as follows. E0023 asks whether the
compilation knows the name **at all**: a symbol whose package reached
the compilation — including via a sibling file's import — satisfies it.
[GALA-E0025](GALA-E0025.md) then asks the stricter question of whether
**this** file declared the import, for the signature types it covers.
So a bare `ArrayTabulate` in a file whose package never imports
`collection_immutable` is E0023 (the name is nowhere in the symbol
table); the same call in a file whose *sibling* imports it passes E0023
and is caught by E0025 on the signature that mentions `Array`.

Not covered. Each of these is a deliberate trade of a missed detection
for a guaranteed absence of false positives:

- **Type positions.** `func f(x Foo)`, `val v Foo = ...` and `Foo{}`
  are skipped, because the analyzer's type resolution is lossy enough
  (Go generics, constraints, `map[K]V`, func types) that flagging here
  would produce false positives. [GALA-E0025](GALA-E0025.md) covers a
  signature type whose package reached the compilation but whose
  import this file omitted; it does **not** cover a type name nothing
  in the compilation declares, because it works from the resolved
  metadata such a name never produces. So `func total(xs Array[int])`
  in a package that imports `collection_immutable` nowhere is caught
  by neither code: it transpiles, erases the body's lambda to
  `func(acc any, x any) any`, and fails at `go build` with
  `undefined: Array`. Closing that needs a check that can distinguish
  an unresolvable type name from a merely lossy one; widening this one
  would trade away the zero-false-positive property.
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
