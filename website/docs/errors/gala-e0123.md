---
layout: default
title: "GALA-E0123 — Go switch Statement"
description: "GALA-E0123 fires on a Go switch statement: an expression switch, a tagless switch or a type switch. GALA branches with match and if."
keywords: "gala-e0123, gala switch, gala switch case, gala type switch, gala match, gala no viable alternative case"
permalink: /docs/errors/gala-e0123/
last_modified_at: 2026-11-15
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0123</p>

# GALA-E0123 — Go `switch` statement

**When it fires.** A Go `switch` statement is written: an expression switch,
`switch x { case 1: ... }`, a tagless one, `switch { case x < 0: ... }`, or a
type switch, `switch v := x.(type) { ... }`. GALA branches with `match` and
`if`.

**Minimal repro.**

```gala
package main

func describe(n int) string {
    switch n {
    case 1, 2:
        return "small"
    default:
        return "large"
    }
}

func main() {
    Println(describe(2))
}
```

**Error output.**

```
error[GALA-E0123]: Go `switch` statement: GALA branches with `match` or `if`
  --> main.gala:4:5
  |
4 |     switch n {
  |     ^^^^^^ write it as a match
  |
  = hint: write it as a match; here, `n match { case ... => ... case _ => ... }`, where `case a, b:` is `case a | b =>` and `default:` is `case _ =>`
```

**Fix.** An expression switch is a `match` on the same value. `case a, b:`
is `case a | b =>`, `default:` is `case _ =>`, and the match is an
expression, so each arm gives its value:

```gala
package main

func describe(n int) string = n match {
    case 1 | 2 => "small"
    case _ => "large"
}

func main() {
    Println(describe(2))
}
```

A tagless switch is an `if` / `else if` chain, or a `match` whose arms have
guards. A type switch is a `match` on type patterns: `switch v := x.(type)`
with `case int:` is `x match { case v: int => ... case _ => ... }`. The hint
names the form for the switch written, with its own subject.

**Rationale.** `switch` is not a GALA keyword; it is reserved as a name
([GALA-E0055](/docs/errors/gala-e0055/)). A switch used to stop the parser at its first
case with `no viable alternative at input '{case1:'` and an `extraneous input
':'` error for every case after it. It is now this one error at the `switch`;
the code inside its cases is still checked.

**Scope.** `fallthrough`, `select` and `goto` are
[GALA-E0036](/docs/errors/gala-e0036/). A type assertion outside a switch, `x.(T)`, is
[GALA-E0124](/docs/errors/gala-e0124/).

---

## Related

- [Porting from Go](/docs/porting-from-go/)
- [All GALA error codes](/docs/errors/)
