---
layout: default
title: "GALA-E0034 — Parameter Has No Declared Type (No Grouped Parameters)"
description: "\"parameter has no declared type\" — GALA-E0034 fires on Go-style grouped parameters like func add(a, b int). GALA requires one type per parameter; see the real compiler output and the corrected signatures."
keywords: "gala-e0034, parameter has no declared type, gala grouped parameters, func add(a b int), gala struct field type, gala signature error"
permalink: /docs/errors/gala-e0034/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0034</p>

# GALA-E0034 — Untyped function or struct parameter

**What it means.** A function parameter, method parameter, or struct-shorthand field is written without a type. Unlike [lambda parameters](/docs/errors/gala-e0033/), these declaration sites have no surrounding context to infer from, so each one must state its own type.

The usual trigger is Go-style *grouped* parameter syntax, where several names share a trailing type.

---

## Code that triggers it

```gala
func add(a, b int) int = a + b       // `a` has no declared type

struct Point(X, Y int)               // `X` has no declared type
```

GALA parses `(a, b int)` as a typeless `a` followed by a typed `b int` — it does **not** propagate the trailing type back over the earlier names. Grouped parameters are not a supported feature.

---

## Compiler message

```
error[GALA-E0034]: parameter "a" has no declared type
  --> e0034.gala:3:10
  |
3 | func add(a, b int) int = a + b
  |          ^ type every parameter individually
  |
  = hint: type every parameter individually (e.g. `a int`); GALA does not support Go-style grouped parameters like `(a, b int)`
```

---

## How to fix it

Give every parameter and field its own type:

```gala
func add(a int, b int) int = a + b

struct Point(X int, Y int)
```

---

## Why the rule exists

Emitting `any` for the untyped parameter would violate GALA's concrete-types invariant and only surface later as a confusing Go build failure (`mismatched types any and int`). Rejecting it at the declaration site names the exact parameter and points at the fix. Keeping one type per parameter also makes the rule uniform across function signatures, method signatures, and struct-shorthand fields.

---

## Related

- [GALA-E0033](/docs/errors/gala-e0033/) — untyped *lambda* parameters, which can often be inferred
- [Language Reference](/docs/language-reference/) — declaration syntax
- [All GALA error codes](/docs/errors/)
