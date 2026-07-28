---
layout: default
title: "GALA-E0004 — Sealed Variant Pattern Binds the Wrong Number of Fields"
description: "GALA-E0004 fires when a sealed-variant pattern binds fewer or more fields than the variant declares. See the triggering code, the compiler message, and the `_` placeholder fix."
keywords: "gala-e0004, sealed variant arity mismatch, gala pattern binds fields, gala match arity, pattern destructuring error"
permalink: /docs/errors/gala-e0004/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0004</p>

# GALA-E0004 — Sealed variant pattern arity mismatch

**What it means.** A pattern in a sealed-type `match` binds a different number of fields than the variant declares. Using `_` for fields you do not need is fine; silently truncating the tail is not.

---

## Code that triggers it

```gala
package main

sealed type Shape {
    case Rect(W int, H int, Label string)
    case Circle(R int)
}

func area(s Shape) int = s match {
    case Rect(w, h) => w * h        // Rect has 3 fields, the pattern binds 2
    case Circle(r)  => r * r * 3
}

func main() {
    Println(area(Circle(2)))
}
```

---

## Compiler message

```
error[GALA-E0004]: sealed variant "Rect" pattern binds 2 field(s) but declares 3
  --> e0004.gala:8:26
  |
8 | func area(s Shape) int = s match {
  |                          ^ use `_` for unused fields
  |
  = hint: use `_` for unused fields
```

The caret sits on the match subject — the check runs over the whole match — and the header names the offending variant and both counts.

---

## How to fix it

Bind every field, using `_` for the ones you do not care about:

```gala
case Rect(w, h, _) => w * h
```

---

## Why the rule exists

`Rect(w, h)` looks like it matches a two-field variant, but `Rect` has a third `Label` field. Without the arity check the generated code would either misalign the bindings (assigning `Label` to `h`) or fail at runtime.

The `_` placeholder is the explicit "I know this field exists and I do not need it" acknowledgement. It reads differently from forgetting the field, which is exactly the distinction that matters when someone adds a field to the variant later.

---

## Related

- [Sealed Types](/features/sealed-types/)
- [Pattern Matching](/features/pattern-matching/) — destructuring rules
- [GALA-E0002](/docs/errors/gala-e0002/) — exhaustiveness checking on the same match
- [All GALA error codes](/docs/errors/)
