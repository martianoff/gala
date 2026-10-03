---
layout: default
title: "GALA-E0048 — Method on an Alias to a Non-Local Type"
description: "\"cannot declare a method\" on a type alias — GALA-E0048 fires when an alias resolves to a built-in, another package's type, or a composite, which Go cannot give methods. Wrap the value in a struct instead."
keywords: "gala-e0048, gala type alias method, gala cannot define new methods on non-local type, gala newtype, gala alias receiver, golang type alias methods"
permalink: /docs/errors/gala-e0048/
last_modified_at: 2026-10-03
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0048</p>

# GALA-E0048 — method on an alias to a non-local type

**What it means.** A method is declared on a type alias that does not resolve to a plain type this package declares — a built-in, a type from another package, an unnamed composite (slice, map, func) or an instantiated type — or on any generic alias (`type Box[T any] Cell[T]`), which Go gives no methods whatever it names. `type X Y` is an *alias*, not a new type: `X` and `Y` are one type, so the method would belong to `Y`, and Go permits methods only on types its own package declares.

---

## Code that triggers it

```gala
package main

type DateTime int64

func (d DateTime) Millis() int64 = int64(d)

func main() {
    Println(DateTime(5).Millis())
}
```

---

## Compiler message

```
error[GALA-E0048]: cannot declare a method on "DateTime": it resolves to the built-in type int64
  --> main.gala:5:6
  |
5 | func (d DateTime) Millis() int64 = int64(d)
  |      ^ a type alias is the same type as its target, so it takes no…
  |
  = hint: a type alias is the same type as its target, so it takes no methods of its own — declare `opaque type DateTime int64` for a distinct type with methods, or write the method as a plain function
```

---

## How to fix it

Declare an opaque type, which gives the value an identity of its own and somewhere for the methods to live, and keeps its operators and its encoding:

```gala
opaque type DateTime int64

func (d DateTime) Millis() int64 = int64(d)
```

A JSON or YAML codec writes an opaque type as the bare value, exactly as it wrote the alias, so documents already written still decode. The opaque type is distinct, though: an `int64` no longer passes for a `DateTime` without `DateTime(n)` ([GALA-E0064](/docs/errors/gala-e0064/)) — see [Opaque Types](/docs/language-reference/#opaque-types). A plain function keeps the alias:

```gala
type DateTime int64

func millisOf(d DateTime) int64 = int64(d)
```

---

## Where it stands down

A non-generic alias whose chain passes through no generic alias and ends at a plain type declared in **this** package is a legal receiver, because the base type is then local:

```gala
struct Point(X int, Y int)

type Coord Point

func (c Coord) Sum() int = c.X + c.Y
```

Everything else an alias does is unaffected — annotating a type, converting (`Millis(v)`), and constructing through an alias to a struct (`Coord(1, 2)`). Declaration order does not matter: a method written above its own alias is caught too.

---

## Why the rule exists

The declaration used to be emitted unchecked, so the rejection arrived from `go build` against generated code — `cannot define new methods on non-local type DateTime` — naming a Go rule for a type the author declared in GALA, and only at build time, after a clean transpile.

---

## Related

- [Language Reference: Type Aliases](/docs/language-reference/#type-aliases) — what an alias can and cannot do, and [alias, opaque type or private-field struct](/docs/language-reference/#alias-opaque-type-or-private-field-struct)
- [All GALA error codes](/docs/errors/)
