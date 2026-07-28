---
layout: default
title: "GALA-E0003 — Match Expression Must Have a Default Case"
description: "\"match expression must have a default case (case _ => ...)\" — GALA-E0003 fires when a match over a non-sealed type has no catch-all branch. See the triggering code, the compiler output, and the fix."
keywords: "gala-e0003, match expression must have a default case, gala default case, gala match error, golang pattern matching default"
permalink: /docs/errors/gala-e0003/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0003</p>

# GALA-E0003 — Match expression must have a default case

**What it means.** A `match` expression over a non-sealed type — a primitive, a plain struct, anything with an unbounded instance set — has no default branch. The compiler cannot prove exhaustiveness for open types, so a default is mandatory.

---

## Code that triggers it

```gala
package main

func name(n int) string = n match {
    case 1 => "one"
    case 2 => "two"
    // missing: case _ => ...
}

func main() {
    Println(name(1))
}
```

---

## Compiler message

```
error[GALA-E0003]: match expression must have a default case (case _ => ...)
  --> e0003.gala:4:5
  |
4 |     case 1 => "one"
  |     ^^^^ add `case _ => ...`
  |
  = hint: add `case _ => ...`
```

---

## How to fix it

Add a default that produces a sensible fallback:

```gala
func name(n int) string = n match {
    case 1 => "one"
    case 2 => "two"
    case _ => "other"
}
```

If you genuinely want the program to fail on unknown values, say so explicitly:

```gala
case _ => panic("unexpected n")
```

---

## Why the rule exists

Non-sealed types have unbounded instance sets, so the transpiler cannot verify every value is covered. Without a default, the generated Go would need a synthesized one (usually a runtime panic), hiding the missing branch from the author. Requiring the default in source surfaces the decision where it is made.

If you want the compiler to prove exhaustiveness for you instead, model the value as a [sealed type](/features/sealed-types/) — then you get [GALA-E0002](/docs/errors/gala-e0002/) coverage checking and no default is needed.

---

## Related

- [GALA-E0002](/docs/errors/gala-e0002/) — the sealed-type counterpart
- [GALA-E0006](/docs/errors/gala-e0006/) — more than one default case
- [Pattern Matching](/features/pattern-matching/)
- [All GALA error codes](/docs/errors/)
