---
layout: default
title: "GALA-E0136 — Argument Given More Than Once"
description: "GALA-E0136 fires when a call gives one parameter or field two arguments: it names it twice, as in f(a = 1, a = 2), or names one a positional argument already fills. Remove the duplicate."
keywords: "gala-e0136, gala named arguments, gala duplicate argument, gala argument given twice, gala parameter given twice"
permalink: /docs/errors/gala-e0136/
last_modified_at: 2026-11-16
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0136</p>

# GALA-E0136 — Argument given more than once

**When it fires.** A call gives one parameter or field two arguments: two
named arguments name it, as in `f(a = 1, a = 2)`, or a named argument names one
that a positional argument already fills, as in `f(1, a = 2)` where `a` is the
first parameter. The rule is the same for every call form: GALA and Go
functions and methods, generic ones, struct and sealed case constructors,
interface literals and `Copy()`. The message names the argument and points at
its second occurrence.

**Minimal repro.**

```gala
package main

func area(width int, height int) int = width * height

func main() {
    Println(area(width = 3, height = 4, width = 5))
}
```

**Error output.**

```
error[GALA-E0136]: argument `width` is given more than once in call to `area`
  --> main.gala:6:41
  |
6 |     Println(area(width = 3, height = 4, width = 5))
  |                                         ^^^^^ remove the duplicate
  |
  = hint: remove the duplicate; each parameter, field or method takes one argument
```

A named argument for a parameter a positional argument already fills is the
same error:

```gala
package main

func area(width int, height int) int = width * height

func main() {
    Println(area(3, width = 5))
}
```

```
error[GALA-E0136]: argument `width` is given both by position and by name in call to `area`
  --> main.gala:6:21
  |
6 |     Println(area(3, width = 5))
  |                     ^^^^^ remove the duplicate
  |
  = hint: remove the duplicate; each parameter, field or method takes one argument
```

**Fix.** Remove the duplicate, keeping the value you mean:

```gala
package main

func area(width int, height int) int = width * height

func main() {
    Println(area(width = 5, height = 4))
    Println(area(5, height = 4))
}
```

**Rationale.** Named arguments are passed to the parameters they name, so a
parameter named twice has two values and only one can be passed. Keeping
either one silently hides a mistake: a value the call writes is never used.

---

## Related

- [Named arguments](/docs/language-reference/#named-arguments)
- [All GALA error codes](/docs/errors/)
