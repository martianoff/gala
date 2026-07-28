---
layout: default
title: "GALA-E0002 — Non-Exhaustive Match on a Sealed Type"
description: "\"non-exhaustive match: missing cases\" — GALA-E0002 fires when a match on a sealed type omits variants and has no default branch. See the triggering code, the real compiler output, and both fixes."
keywords: "gala-e0002, non-exhaustive match, gala sealed type match, missing cases, golang exhaustive pattern matching, gala match error"
permalink: /docs/errors/gala-e0002/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0002</p>

# GALA-E0002 — Non-exhaustive match on a sealed type

**What it means.** A `match` expression on a sealed type omits one or more variants and has no default (`case _ =>`) fallback. The compiler lists exactly which variants are missing.

---

## Code that triggers it

```gala
package main

sealed type Color {
    case Red()
    case Green()
    case Blue()
}

func name(c Color) string = c match {
    case Red()   => "red"
    case Green() => "green"
    // missing: Blue
}

func main() {
    Println(name(Red()))
}
```

---

## Compiler message

```
error[GALA-E0002]: non-exhaustive match: missing cases: Blue
  --> e0002.gala:10:5
   |
10 |     case Red()   => "red"
   |     ^^^^ add the missing variant cases, or add a `case _ => ...` defa…
   |
   = hint: add the missing variant cases, or add a `case _ => ...` default to cover them
```

The caret marks the start of the match arms; the header names the variants you left out.

---

## How to fix it

Cover every variant explicitly:

```gala
func name(c Color) string = c match {
    case Red()   => "red"
    case Green() => "green"
    case Blue()  => "blue"
}
```

Or add a default when the remaining variants collapse to the same result:

```gala
func name(c Color) string = c match {
    case Red() => "red"
    case _     => "other"
}
```

---

## Why the rule exists

Sealed types exist so the compiler can verify you have thought about every variant. A silent fall-through would carry the same risk as a Go `switch` with a forgotten case. GALA therefore requires either full coverage or an explicit acknowledgement (`case _ =>`) that some variants are grouped.

This is the payoff of sealing a hierarchy: add a variant later, and every non-exhaustive match in the codebase becomes a compile error instead of a runtime surprise.

---

## Related

- [Sealed Types](/features/sealed-types/) — closed hierarchies and auto-generated constructors
- [Pattern Matching](/features/pattern-matching/) — destructuring, guards, and expression results
- [GALA-E0003](/docs/errors/gala-e0003/) — the open-type counterpart (missing default case)
- [All GALA error codes](/docs/errors/)
