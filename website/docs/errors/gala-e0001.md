---
layout: default
title: "GALA-E0001 — Recursive Immutable Wrap Is Not Allowed"
description: "GALA-E0001 fires when a value is wrapped in two or more layers of Immutable[T]. Learn what triggers the error, the exact compiler message, and how to unwrap with .Get() so the val binding wraps exactly once."
keywords: "gala-e0001, gala recursive immutable, immutable wrapping is not allowed, gala immutable type, gala val binding error"
permalink: /docs/errors/gala-e0001/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0001</p>

# GALA-E0001 — Recursive `Immutable` wrap

**What it means.** A declaration or expression tries to wrap a value inside two or more layers of `Immutable[T]`. `Immutable[T]` is the compiler-managed wrapper around `val`-bound state — the transpiler inserts it exactly once, so nesting it is always a bug.

---

## Code that triggers it

```gala
package main

import . "martianoff/gala/std"

func getImm() Immutable[int] = NewImmutable(1)

func main() {
    val y = NewImmutable(getImm())   // Immutable[Immutable[int]]
    Println(y)
}
```

An explicit annotation does the same thing: `val x Immutable[Immutable[int]] = NewImmutable(NewImmutable(1))`.

---

## Compiler message

```
error[GALA-E0001]: recursive Immutable wrapping is not allowed
  --> e0001.gala:8:26
  |
8 |     val y = NewImmutable(getImm())
  |                          ^^^^^^
  |
```

The caret sits on the already-wrapped inner value. This code emits no hint, so the diagnostic ends at the frame.

---

## How to fix it

Assign the inner value directly and let the `val` binding do the wrapping:

```gala
val x = 1                // the compiler wraps this as Immutable[int]
val y = getImm().Get()   // unwrap once; the outer val wraps it again
```

If you must hand an already-wrapped value to a function that produces another wrapped value, unwrap it with `.Get()` first and let the caller re-wrap.

---

## Why the rule exists

`Immutable[T]` is an implementation detail of `val` bindings. Nesting it breaks the read-position auto-unwrap (`person.name` → `person.name.Get()`) and produces Go the downstream compiler cannot type-infer. Rather than silently flattening the wrap, GALA rejects it at the source so the redundant `NewImmutable` call is visible.

---

## Related

- [Immutability](/features/immutability/) — `val` bindings, immutable fields, and `Copy()`
- [All GALA error codes](/docs/errors/)
