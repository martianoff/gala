---
layout: default
title: "GALA-E0083 — Unexported Method of Another Package"
description: "GALA-E0083 fires when code calls a lower-case method of another package's GALA type, such as a sealed type's isCircle() predicate. Match on the variant instead, or call an exported method."
keywords: "gala-e0083, gala unexported method, gala sealed type isvariant, gala cannot refer to unexported method, gala match variant, gala visibility"
permalink: /docs/errors/gala-e0083/
last_modified_at: 2026-10-24
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0083</p>

# GALA-E0083 — Unexported method of another package

**When it fires.** A method of a GALA type declared in another package is
called, but the method is not exported. Visibility follows Go: a method whose
name starts with a lower-case letter belongs to its package, and only that
package can call it.

The method most often reached for this way is a sealed type's `is<Variant>`
predicate. Every sealed type gets one per case — `isCircle()`, `isDot()` —
for its own package's use; they are not part of the type's public API.

**Minimal repro.**

`shapes/shapes.gala`:

```gala
package shapes

sealed type Shape {
    case Circle(R int)
    case Dot()
}
```

`main.gala`:

```gala
package main

import "example.com/demo/shapes"

func round(s shapes.Shape) bool = s.isCircle()

func main() {
    Println(round(shapes.Dot()))
}
```

**Error output.**

```
error[GALA-E0083]: isCircle is an unexported method of shapes.Shape, so only package shapes can call it
  --> main.gala:5:37
  |
5 | func round(s shapes.Shape) bool = s.isCircle()
  |                                     ^^^^^^^^ match the variant instead
  |
  = hint: match the variant instead (is<Variant> predicates are private to the sealed type's package): `case shapes.Circle(_) => true`, `case _ => false`
```

For a lower-case method the type declares itself, the hint reads
`call an exported method (only the capitalized methods of Box are visible
outside package shapes)`.

**Fix.** Test the variant with a match, naming the case through the import
(this program needs the `shapes` package above beside it):

<!-- doc-check: fragment -->
```gala
package main

import "example.com/demo/shapes"

func round(s shapes.Shape) bool = s match {
    case shapes.Circle(_) => true
    case _                => false
}

func main() {
    Println(round(shapes.Dot()))
}
```

For a method the other package declares, call an exported method instead, or
— if you own that package — export the method by capitalizing its name.

**Rationale.** The call never compiled; Go rejected the generated code with
`cannot refer to unexported method`, pointing at the generated Go rather than
your source. A match is also the better test: it is checked for exhaustiveness
when it covers every case, and it can bind the variant's fields in the same
step. See [Sealed Types](/features/sealed-types/).
