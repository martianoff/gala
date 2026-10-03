---
layout: default
title: "GALA-E0061 — Sealed Variant Used as a Type"
description: "\"Circle is a variant of sealed type Shape, not a type\" — GALA-E0061 fires when a sealed variant such as Circle or Some[int] is named where a type is expected."
keywords: "gala-e0061, gala sealed variant type, gala variant is not a type, gala sealed type parameter, gala Some as a type, gala narrowed sealed type"
permalink: /docs/errors/gala-e0061/
last_modified_at: 2026-10-02
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0061</p>

# GALA-E0061 — Sealed variant used as a type

**When it fires.** A variant of a sealed type is named where a type is
expected. A `case` of a sealed type declares a constructor and an extractor,
not a type of its own: every value `Circle(...)` builds is a `Shape`. The
check covers every type position — a parameter, a result, a struct or variant
field, a `val` / `var` annotation, a type argument (in a type or in a call
such as `ArrayOf[Circle](...)`), a typed pattern (`case c: Circle`), a
receiver and an alias target — for a variant of a sealed type declared in the
same package, in an imported package (`shapes.Circle`), or in `std`
(`Some[int]`, `Left[string, int]`).

**Minimal repro.**

```gala
package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func radius(c Circle) float64 = c.R

func main() {
    Println(radius(Circle(2.0)))
}
```

**Error output.**

```
error[GALA-E0061]: Circle is a variant of sealed type Shape, not a type
  --> main.gala:8:15
  |
8 | func radius(c Circle) float64 = c.R
  |               ^^^^^^ use the sealed type Shape here
  |
  = hint: use the sealed type Shape here; Circle(...) builds one, and `case Circle(...)` in a match reaches the variant's fields
```

**Fix.** Take the sealed type, and match on the variant to reach its fields:

```gala
package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func radius(s Shape) float64 = s match {
    case Circle(r) => r
    case Square(_) => 0.0
}

func main() {
    Println(radius(Circle(2.0)))
}
```

When a function only makes sense for one variant, say so in its result
instead: return an `Option` and let the caller decide what the other variants
mean.

```gala
package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func radius(s Shape) Option[float64] = s match {
    case Circle(r) => Some(r)
    case _ => None[float64]()
}

func main() {
    Println(radius(Circle(2.0)).GetOrElse(0.0))
}
```

**A `std` variant.** `Some`, `None`, `Left`, `Right`, `Success` and `Failure`
are variants of `Option`, `Either` and `Try`, so the same rule applies to
them:

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val found Array[Some[int]] = ArrayOf(Some(1), Some(2))
    Println(found)
}
```

```
error[GALA-E0061]: Some[int] is a variant of sealed type Option[int], not a type
  --> main.gala:6:21
  |
6 |     val found Array[Some[int]] = ArrayOf(Some(1), Some(2))
  |                     ^^^^ use the sealed type Option[int] here
  |
  = hint: use the sealed type Option[int] here; Some(...) builds one, and `case Some(...)` in a match reaches the variant's fields
```

Write `Array[Option[int]]`.

**Rationale.** In the generated Go, a sealed type is one struct that holds the
fields of every variant, and each variant is an empty companion struct that
carries its `Apply` and `Unapply`. A variant named as a type compiled to that
empty struct, so the program failed in `go build` — `c.R undefined (type
Circle has no field or method R)` and `cannot use Circle{}.Apply(2.0) (value
of type Shape) as Circle value` — and a typed pattern `case c: Circle` asked
whether a `Shape` is the companion struct, which is never true, so the arm
silently never matched. GALA has no type for a single variant: narrowing to
one is what a `match` arm does.

**What still works.** The sealed type in any type position, a variant as a
constructor (`Circle(2.0)`) and as a pattern (`case Circle(r)`), and a type or
type parameter you declare under a variant's name, which shadows it.
