---
layout: default
title: "GALA-E0005 — Extractor Must Define an Unapply Method"
description: "GALA-E0005 fires when a pattern uses a name as an extractor but that type has no Unapply method. See the triggering code, the compiler message with its did-you-mean hint, and how to write an Unapply."
keywords: "gala-e0005, must define an unapply method, gala extractor, gala unapply, scala style extractor go, gala pattern matching error"
permalink: /docs/errors/gala-e0005/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0005</p>

# GALA-E0005 — Extractor must define an `Unapply` method

**What it means.** A pattern invokes something as an extractor (`case Foo(x) =>`) but the referenced type has no `Unapply` method. GALA patterns use Scala-style extractor semantics: any type used as a pattern must define `Unapply`, returning either `bool` for a guard pattern or `Option[T]` / `Option[TupleN[…]]` for field extraction.

---

## Code that triggers it

```gala
package main

import . "martianoff/gala/std"

func main() {
    // Typo: Som instead of Some
    val r = Some(42) match {
        case Som(x) => x
        case _      => 0
    }
    Println(r)
}
```

Or a type that exists but is not an extractor:

```gala
type Bucket struct { items Array[int] }

val r = bucket match {
    case Bucket(xs) => xs
    case _          => ArrayOf[int]()
}
```

---

## Compiler message

```
error[GALA-E0005]: extractor 'Som' must define an Unapply method. For generic extractors use: func (e Extractor[T]) Unapply(v ContainerType[T]) Option[T]. For guard patterns use: func (e Extractor) Unapply(v ConcreteType) bool
  --> e0005.gala:7:14
  |
7 |         case Som(x) => x
  |              ^^^ did you mean 'Some'?
  |
  = hint: did you mean 'Some'?
```

The transpiler suggests near matches from the companion-object registry, so typos usually come back with a "did you mean?" hint.

---

## How to fix it

**Typo** — use the name the hint suggests (`Some`, not `Som`).

**Missing extractor** — define one. A guard extractor on a concrete type:

```gala
type Even struct {}
func (e Even) Unapply(i int) bool = i % 2 == 0
```

A generic extractor that pulls a value out of a container follows this signature shape (the compiler message spells it out too):

```gala
func (e Extractor[T]) Unapply(v ContainerType[T]) Option[T]
```

Return `Some(value)` when the pattern matches and `None[T]()` when it does not.

**Struct matching** — every public (uppercase) field of a struct auto-generates an `Unapply`, so a failure here is usually a name mismatch or a private field.

---

## Why the rule exists

Without the `Unapply` contract, any capitalized identifier in a pattern would be silently treated as a variable binding: `case Som(x) =>` would compile as "bind the matched value to `Som`" and quietly discard the `(x)`. The explicit error plus a "did you mean?" suggestion catches the typo, and the required `Unapply` shape keeps extractors self-documenting.

---

## Related

- [Pattern Matching](/features/pattern-matching/) — extractors, guards, and destructuring
- [Sealed Types](/features/sealed-types/) — variants get `Unapply` generated for you
- [All GALA error codes](/docs/errors/)
