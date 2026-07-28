---
layout: default
title: "GALA-E0006 — Multiple Default Cases in a Match Expression"
description: "GALA-E0006 fires when a match has more than one catch-all branch. See the triggering code, the compiler message, and how to restructure so a single default handles every remaining case."
keywords: "gala-e0006, multiple default cases, gala match default, unreachable match branch, gala pattern matching error"
permalink: /docs/errors/gala-e0006/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0006</p>

# GALA-E0006 — Multiple default cases in a match expression

**What it means.** A `match` expression has more than one default branch — either two `case _ =>` clauses, or a mix of `case _ =>` and an unguarded binding pattern (`case n =>`) that also catches everything.

---

## Code that triggers it

```gala
package main

func name(n int) string = n match {
    case 1 => "one"
    case _ => "other"
    case _ => "also-other"   // second default
}

func main() {
    Println(name(1))
}
```

---

## Compiler message

```
error[GALA-E0006]: multiple default cases in match expression
  --> e0006.gala:6:5
  |
6 |     case _ => "also-other"
  |     ^^^^ keep one default case
  |
  = hint: keep one default case; combine logic with guards or nested matches if you need sub-cases
```

The caret sits on the second default — the one that can never fire.

---

## How to fix it

Keep exactly one default. If you need to distinguish several "other" groups, use a guard or move the branching into the default body:

```gala
func name(n int) string = n match {
    case 1            => "one"
    case n if n > 100 => "big"
    case _            => "other"
}
```

---

## Why the rule exists

The second default is unreachable. GALA emits the default as the final `else` of the generated if-else chain, so anything after it is dead code. Making it a compile error means you notice the duplication instead of wondering why a branch never fires.

---

## Related

- [GALA-E0003](/docs/errors/gala-e0003/) — no default case at all
- [Pattern Matching](/features/pattern-matching/) — guards and case ordering
- [All GALA error codes](/docs/errors/)
