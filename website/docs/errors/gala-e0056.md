---
layout: default
title: "GALA-E0056 — Malformed Tuple Destructuring"
description: "\"a tuple destructuring `var (...)` needs an initializer\" — GALA-E0056 fires when val (a, b) / var (a, b) has a type annotation, no initializer, or more than one expression on the right."
keywords: "gala-e0056, gala tuple destructuring, gala var tuple, gala val (a, b), gala destructure tuple, gala tuple type annotation"
permalink: /docs/errors/gala-e0056/
last_modified_at: 2026-10-01
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0056</p>

# GALA-E0056 — Malformed tuple destructuring

**What it means.** A tuple destructuring declaration, `val (a, b) = pair` or `var (a, b) = pair`, has a type annotation, has no initializer, or has more than one expression on the right. A destructuring takes every name's type from the one tuple it splits, so none of these has a meaning.

---

## Code that triggers it

```gala
package main

func bounds() Tuple[int, int] = (3, 9)

func main() {
    var (lo, hi) Tuple[int, int] = bounds()
    lo = lo * 2
    Println(s"$lo..$hi")
}
```

---

## Compiler message

```
error[GALA-E0056]: a tuple destructuring `var (...)` takes no type annotation
  --> main.gala:6:18
  |
6 |     var (lo, hi) Tuple[int, int] = bounds()
  |                  ^^^^^^^^^^^^^^^ remove the type
  |
  = hint: remove the type; each name takes its type from the tuple
```

A destructuring with no tuple to split:

```
error[GALA-E0056]: a tuple destructuring `var (...)` needs an initializer
  --> main.gala:4:5
  |
4 |     var (lo, hi)
  |     ^^^^^^^^^^^^ add the tuple to split: `= pair`
  |
  = hint: add the tuple to split: `= pair`; a variable with no value declares its own type instead, as in `var a int`
```

---

## How to fix it

Write the tuple after `=` and let each name take its component's type:

```gala
package main

func bounds() Tuple[int, int] = (3, 9)

func main() {
    var (lo, hi) = bounds()
    lo = lo * 2
    Println(s"$lo..$hi")
}
```

To declare variables that start without a value, declare each one with its own type, `var lo int`, rather than as a destructuring.

---

## Why the rule exists

`val` and `var` share their grammar with the plain `val a T = x` form, so the type and initializer slots parse after a tuple pattern too. An annotation there would name the tuple's type rather than any variable's, and a destructuring with no tuple has nothing to take types or values from. Before this code, `var (a, b)` stopped the transpiler with an internal error.

The grammar ignores newlines between a declaration's parts, so `var (lo, hi)` followed by `lo = 3` on the next line parses `lo` as a type. The declaration as written still has no initializer, and that is what the error reports.

---

## Related

- [Tuple destructuring](/docs/language-reference/#9-standard-library-types)
- [All GALA error codes](/docs/errors/)
