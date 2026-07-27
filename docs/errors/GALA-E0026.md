# GALA-E0026 — Ambiguous sealed-variant reference

> **No valid source reaches this code today.** Its precondition is a strict
> subset of [GALA-E0032](GALA-E0032.md)'s, and E0032 is checked earlier — so
> every construction attempted reports E0032 instead. It is retained as a
> backstop. If you are here from a real error message, you want
> [GALA-E0032](GALA-E0032.md).

**When it fires.** An *unqualified* sealed-variant constructor name is used with
**named arguments** (e.g. `Box(W = 1)`), and that case name is declared by a
sealed type in **two or more dot-imported packages**. The transpiler cannot pick
one without silently shadowing the others, so it refuses and asks for a
qualifier.

The check lives in the variant-resolution helpers
(`findSealedVariant` / `findSealedVariantFields` in
`internal/transpiler/transformer/calls.go`). Two guards run before it:

- **Local declarations win.** The first resolution pass is scoped to the
  explicitly named package, or to the current package when there is no
  qualifier. A same-named variant in your own package always shadows every
  dot-import, and never reaches the ambiguity check.
- **An explicit qualifier is authoritative.** When the call site writes
  `shapes.Box(...)`, resolution stops after the scoped pass — the ambiguity
  scan is skipped entirely.

**Minimal repro.** None. The precondition for GALA-E0026 — two dot-imported
packages that each declare a sealed case of the same name — is a strict subset
of the precondition for [GALA-E0032](GALA-E0032.md) (two dot-imported packages
exporting the same identifier). Every sealed case registers a companion type
under its own name, so the dot-import collision check sees the clash first, and
it runs before any expression is transformed:

```gala
package main

import (
    . "example.com/demo/shapes"   // sealed type Shape { case Box(W int) ... }
    . "example.com/demo/widgets"  // sealed type Widget { case Box(H int) ... }
)

func main() {
    val b = Box(W = 1)
    Println(b)
}
```

Compiling that program reports `error[GALA-E0032]` naming `Box` and both
packages — see [GALA-E0032](GALA-E0032.md) for the exact output.

Narrowing the setup does not help either. With a *single* dot import, the
ambiguity scan requires at least two dot-imported packages to contribute a
match, and the package-scoped first pass resolves the name before the scan runs.

The code is retained because the two guards in front of it are independent: if
the dot-import collision check were ever narrowed (for example to allow
deliberate re-export facades), this error would become the backstop that keeps
variant resolution from silently picking by import order.

**Error output.** The message the code would produce, taken from the emit site
(not observed from a compiler run):

```
[SemanticError GALA-E0026] file.gala:L:C ambiguous sealed-variant reference: case "Box" is declared in multiple dot-imported packages (shapes, widgets) (hint: qualify the call site with the package name, e.g. `shapes.Box(...)`)
```

**Fix.** Whichever of the two codes you actually see, the remedy is the same:
stop dot-importing both packages, or qualify the reference.

```gala
package main

import (
    . "example.com/demo/shapes"
    "example.com/demo/widgets"
)

func main() {
    val a = Box(W = 1)              // resolves to shapes.Box
    val b = widgets.Box(H = 2)      // explicitly qualified
    Println(s"$a $b")
}
```

**Rationale.** Before this code existed, variant lookup walked the dot-import
list and returned the first match it found. Because the walk crossed a Go map
(`typeMetas`), the "first" match was not stable — the same source could resolve
to a different variant between builds, producing silently divergent output.
Collecting every match and rejecting more than one makes the failure
deterministic and names both candidate packages.

**Scope.** Only unqualified, named-argument constructor calls resolved through
the dot-import fallback. Positional calls resolve through the companion
`Apply` path and never enter this helper; qualified calls and local
declarations short-circuit before it.
