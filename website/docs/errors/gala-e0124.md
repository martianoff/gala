---
layout: default
title: "GALA-E0124 — Go Type Assertion"
description: "GALA-E0124 fires on a Go type assertion, x.(T). GALA tests a value's type with a type pattern in a match."
keywords: "gala-e0124, gala type assertion, gala x.(T), gala type pattern, gala match type, gala comma ok"
permalink: /docs/errors/gala-e0124/
last_modified_at: 2026-11-15
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0124</p>

# GALA-E0124 — Go type assertion

**When it fires.** A value's type is asserted with Go's `x.(T)`, or with the
two-value form `v, ok := x.(T)`. GALA tests a value's type with a type pattern
in a `match`.

**Minimal repro.**

```gala
package main

func show(v any) string {
    val n = v.(int)
    return s"int $n"
}

func main() {
    Println(show(42))
}
```

**Error output.**

```
error[GALA-E0124]: Go type assertion `v.(int)`: GALA tests a value's type with `match`
  --> main.gala:4:13
  |
4 |     val n = v.(int)
  |             ^^^^^^^ match on a type pattern
  |
  = hint: match on a type pattern; here, `v match { case n: int => ... case _ => ... }`
```

**Fix.** Match on a type pattern, `case n: int`, and say what happens for
every other type in a `case _` arm. The match cannot panic the way a failed
assertion does:

```gala
package main

func show(v any) string = v match {
    case n: int => s"int $n"
    case _ => "not an int"
}

func main() {
    Println(show(42))
}
```

The two-value form, `n, ok := v.(int)`, is the same match, with the `ok`
branch in the typed arm and the other in `case _`.

**Rationale.** The parser used to read `.(` as a selector missing its name,
`extraneous input '(' expecting {'default', IDENTIFIER}`, which never named
the match.

**Scope.** A whole type switch, `switch v := x.(type) { ... }`, is one
[GALA-E0123](/docs/errors/gala-e0123/).

---

## Related

- [Porting from Go](/docs/porting-from-go/)
- [All GALA error codes](/docs/errors/)
