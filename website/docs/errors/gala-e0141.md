---
layout: default
title: "GALA-E0141 — Unreachable Case After a Catch-All"
description: "GALA-E0141 fires when a match or partial function has a case after an unguarded case that matches every value, such as case _ or case x. Cases run in written order, so the later case can never run. Move the catch-all last."
keywords: "gala-e0141, gala unreachable case, gala case order, gala match catch-all, gala case _ before other cases"
permalink: /docs/errors/gala-e0141/
last_modified_at: 2026-11-20
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0141</p>

# GALA-E0141 — Unreachable case

**When it fires.** A `match` or a partial-function literal `{ case ... }` has a
case written after an unguarded case that matches every value. Cases are tried
in the order they are written, so the later case can never run. These cases
match every value:

- `case _`;
- a plain binding, `case x`;
- a typed pattern of the subject's own type, `case n: int` over an `int`, when
  that type is not an interface or a type parameter (a typed pattern over
  `any` or an interface does not match `nil`);
- a tuple pattern of the subject's arity whose elements are each one of these,
  `case (a, _)` over a `Tuple[int, string]`.

A case with a guard, `case x if x > 0`, does not match every value, so the
cases after it stay reachable. A second `case _` (or second plain binding) is
[GALA-E0006](/docs/errors/gala-e0006/) instead.

---

## Code that triggers it

```gala
package main

func name(n int) string = n match {
    case 0 => "zero"
    case _ => "other"
    case 1 => "one"
}

func main() {
    Println(name(1))
}
```

---

## Compiler message

```
error[GALA-E0141]: unreachable case: `case _` on line 5 matches every value, so no case after it can run
  --> main.gala:6:5
  |
6 |     case 1 => "one"
  |     ^^^^ move this case above `case _`, or delete it
  |
  = hint: move this case above `case _`, or delete it
```

---

## How to fix it

Move the catch-all to the end, so the specific cases are tried first:

```gala
func name(n int) string = n match {
    case 0 => "zero"
    case 1 => "one"
    case _ => "other"
}
```

If the later case really is dead, delete it.

---

## Why the rule exists

GALA follows Scala's first-match semantics: the first case that matches wins. A
`match` used to move its catch-all to the end, so the cases written after it
ran first and won, and `name(1)` above returned `"one"` without a diagnostic. A
partial-function literal kept the written order, so the same cases gave
different answers in the two forms. Rejecting the case makes the order of the
cases the order they run in, in both forms.

---

## Related

- [GALA-E0006](/docs/errors/gala-e0006/) — more than one default case
- [Pattern Matching](/features/pattern-matching/) — guards and case ordering
- [All GALA error codes](/docs/errors/)
