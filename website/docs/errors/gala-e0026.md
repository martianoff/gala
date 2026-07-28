---
layout: default
title: "GALA-E0026 — Ambiguous Sealed-Variant Reference Across Dot Imports"
description: "\"ambiguous sealed-variant reference\" — GALA-E0026 guards against a sealed case name declared by two dot-imported packages. In practice GALA-E0032 fires first; here is why, and how to qualify the call site."
keywords: "gala-e0026, ambiguous sealed-variant reference, gala dot import variant, gala sealed case collision, gala qualify constructor, gala-e0032"
permalink: /docs/errors/gala-e0026/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0026</p>

# GALA-E0026 — Ambiguous sealed-variant reference

**What it means.** An *unqualified* sealed-variant constructor is called with **named arguments** (`Box(W = 1)`), and that case name is declared by a sealed type in two or more dot-imported packages. Variant resolution refuses to pick one and asks you to qualify the call site.

**No valid source reaches this code today.** Its precondition is a strict subset of [GALA-E0032](/docs/errors/gala-e0032/)'s, and E0032 is checked earlier, so every attempt to construct it reports E0032 instead. The code is retained as a backstop. **If a real compiler message sent you here, you want [GALA-E0032](/docs/errors/gala-e0032/).**

---

## Why it is unreachable

Two guards run before the ambiguity scan:

- **Local declarations win.** The first resolution pass is scoped to the explicitly named package, or to the current package when there is no qualifier. A same-named variant in your own package always shadows every dot import.
- **An explicit qualifier is authoritative.** When the call site writes `shapes.Box(...)`, resolution stops after the scoped pass and the ambiguity scan never runs.

That leaves only the two-dot-import case — and every sealed case registers a companion type under its own name, so the dot-import collision check sees the clash first. It runs early in transformation, before any expression is transformed.

---

## What you actually get

`shapes/shapes.gala`:

```gala
package shapes

sealed type Shape {
    case Box(W int)
    case Dot()
}
```

`widgets/widgets.gala`:

```gala
package widgets

sealed type Widget {
    case Box(H int)
    case Blank()
}
```

`main.gala`:

```gala
package main

import (
    . "example.com/demo/shapes"
    . "example.com/demo/widgets"
)

func main() {
    val b = Box(W = 1)
    Println(b)
}
```

Compiling that program reports GALA-E0032, naming `Box` and both packages:

```
error[GALA-E0032]: dot-import symbol collision(s) detected:
  - symbol "Box" is exported by multiple dot-imported packages: shapes, widgets
Use a qualified or aliased import for one of the packages to resolve the conflict.
(Some stdlib packages intentionally re-export names from another package as a convenience facade — e.g. `concurrent` re-exports `go_interop`'s execution-context helpers — so dot-importing both is never meaningful. Pick the facade you want.)
  --> main.gala:3:1
  |
3 | import (
  | ^^^^^^ qualify or alias one of the dot-imports to disambiguate
  |
  = hint: qualify or alias one of the dot-imports to disambiguate
```

Narrowing the setup does not help either: with a *single* dot import the ambiguity scan has only one contributing package, and the package-scoped first pass resolves the name before the scan runs.

GALA-E0026's own header — `ambiguous sealed-variant reference: case "Box" is declared in multiple dot-imported packages (…)`, with a hint to qualify the call site — is quoted here from the emit site rather than from a compiler run, because no source produces it.

---

## How to fix it

Whichever of the two codes you see, the remedy is the same: stop dot-importing both packages, or qualify the reference.

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

---

## Why the code exists

Before it, variant lookup walked the dot-import list and returned the first match. Because the walk crossed a Go map, "first" was not stable — the same source could resolve to a different variant between builds, producing silently divergent output. Collecting every match and rejecting more than one makes the failure deterministic and names both candidate packages.

The code is kept because the two guards in front of it are independent. If the dot-import collision check were ever narrowed — to allow deliberate re-export facades, say — this error becomes the backstop that keeps variant resolution from picking by import order.

**Scope.** Only unqualified, named-argument constructor calls resolved through the dot-import fallback. Positional calls resolve through the companion `Apply` path and never enter this helper; qualified calls and local declarations short-circuit before it.

---

## Related

- [GALA-E0032](/docs/errors/gala-e0032/) — the dot-import collision that fires instead
- [Sealed Types](/features/sealed-types/) — variants, companions, and generated constructors
- [Dependency Management](/docs/dependency-management/) — import forms and aliasing
- [All GALA error codes](/docs/errors/)
