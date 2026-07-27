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

**Scope.** Analyzer post-pass, run once per top-level file after all
metadata for the file, its siblings and its imports has been collected
(`internal/transpiler/analyzer/undefined_symbol.go`). It is an
*existence* check only: whether the resolved symbol is used at a
sensible type is not its concern.

The two import-related codes divide as follows. E0023 asks whether the
compilation knows the name **at all**: a symbol whose package reached
the compilation — including via a sibling file's import — satisfies it.
[GALA-E0025](GALA-E0025.md) then asks the stricter question of whether
**this** file declared the import, for the signature types it covers.
So a bare `ArrayTabulate` in a file whose package never imports
`collection_immutable` is E0023 (the name is nowhere in the symbol
table); the same call in a file whose *sibling* imports it passes E0023
and is caught by E0025 on the signature that mentions `Array`.

Not covered, by design:

- **Type positions.** `func f(x Foo)`, `val v Foo = ...` and `Foo{}`
  are skipped; unresolved signature types belong to GALA-E0025.
- **Selectors.** In `x.foo().bar`, only `x` is checked — field and
  method names require the receiver's type.
- **`match` / `case` patterns.** Every identifier in a pattern is
  treated as a binding for that arm, because separating capture names
  from constructor references needs the scrutinee's type. This can miss
  a typo inside a pattern; it can never invent one.
- **Interpolated strings.** `s"...$x..."` bodies are a single lexer
  token and never reach the parse tree the walk sees.
- **Files with an import the analyzer could not load.** If any import
  failed to resolve, the check stands down entirely for that file:
  none of that package's exports are in the symbol table, and blaming
  the author for names the analyzer never saw would be worse than
  missing a real one.
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

The inference engine (`internal/transpiler/infer`) also raises this
code for a name with no binding in its type environment, but its
diagnostics are advisory — the bridge that feeds it approximates
selectors, methods and generics, so callers deliberately discard its
errors. The analyzer pass above is the authoritative source.
