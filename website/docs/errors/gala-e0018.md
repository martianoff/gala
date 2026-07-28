---
layout: default
title: "GALA-E0018 — Cannot Infer Type Parameter for a Sealed Variant Constructor"
description: "GALA-E0018 fires when a zero-argument sealed-variant constructor appears where its parent's type parameter cannot be pinned. See the triggering code, the compiler message, and both annotation fixes."
keywords: "gala-e0018, cannot infer type parameter, gala sealed variant constructor, gala generic sealed type, gala type inference error"
permalink: /docs/errors/gala-e0018/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0018</p>

# GALA-E0018 — Sealed variant type parameter cannot be inferred

**What it means.** A zero-argument constructor of a *generic* sealed type is used somewhere the parent type's parameter cannot be pinned. The transpiler checks three signals before giving up:

1. The enclosing `match` subject's type (`cmd match { case NoCmd() => … }`).
2. The enclosing function's declared return type.
3. A `val` / `var` type annotation supplying an expected type.

If none of those resolve the parameter, generated Go would contain a bare `Variant{}` literal whose type argument Go cannot deduce — producing an obscure `cannot infer T` far from the GALA source.

---

## Code that triggers it

```gala
package main

sealed type Box[T any] {
    case Empty()
}

func main() {
    val x = Empty()   // no annotation, no enclosing type — T is unbound
    Println(x)
}
```

---

## Compiler message

```
error[GALA-E0018]: cannot infer type parameter for sealed variant constructor "Empty()"
  --> e0018.gala:8:18
  |
8 |     val x = Empty()
  |                  ^ annotate the binding
  |
  = hint: annotate the binding (e.g. `val x: ParentType[Int] = Empty()`) or pass type args explicitly (`Empty[Int]()`)
```

---

## How to fix it

Give the parameter a home. Annotate the binding:

```gala
val x Box[int] = Empty()
```

…or instantiate the constructor explicitly:

```gala
val x = Empty[int]()
```

When the constructor is an arm of a `match` against a value of the parent type, or the body of a function with a declared return type, the signal is already present and no annotation is needed:

```gala
func empty[T any]() Box[T] = Empty()   // return type pins T
```

---

## Why the rule exists

GALA's downward inference relies on a single expected-type signal flowing from the enclosing context. When that signal is absent, the transpiler would otherwise fall through to an untyped composite literal and let Go deduce — which fails confusingly and far from the source. Failing fast, with a hint naming the three resolving signals, points at the cheapest fix.

**Scope.** Only zero-argument constructors of *generic* sealed types. Variants of non-generic sealed types, and constructors whose arguments contribute to inference, are unaffected.

---

## Related

- [Sealed Types](/features/sealed-types/) — generic sealed hierarchies
- [Type Inference](/features/type-inference/) — how expected types flow
- [GALA-E0021](/docs/errors/gala-e0021/) — general unification failures
- [All GALA error codes](/docs/errors/)
