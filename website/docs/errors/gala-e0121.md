---
layout: default
title: "GALA-E0121 — Go const Declaration"
description: "GALA-E0121 fires on a Go const declaration, const N = 10 or a const ( ... ) block. GALA declares a constant with val."
keywords: "gala-e0121, gala const, gala constant, gala iota, gala val constant, gala mismatched input const"
permalink: /docs/errors/gala-e0121/
last_modified_at: 2026-11-15
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0121</p>

# GALA-E0121 — Go `const` declaration

**When it fires.** A constant is declared with Go's `const`: `const N = 10`,
inside a function or at the top level, or a `const ( ... )` block. GALA
declares a constant with `val`.

**Minimal repro.**

```gala
package main

const MaxRetries = 3

func main() {
    Println(MaxRetries)
}
```

**Error output.**

```
error[GALA-E0121]: Go `const` declaration: GALA declares a constant with `val`
  --> main.gala:3:1
  |
3 | const MaxRetries = 3
  | ^^^^^^^^^^^^^^^^^^^^ write it with `val`
  |
  = hint: write it with `val`; here, `val MaxRetries = 3`
```

**Fix.** Write `val` for `const`; the rest of the declaration stays:

```gala
package main

val MaxRetries = 3

func main() {
    Println(MaxRetries)
}
```

A top-level `val` is immutable, and a capitalized one compares in a `match`
as a Go constant does in a `switch` case (`case MaxRetries =>`). A `const`
block is one `val` per constant, and the hint spells the first. A block that
numbers its constants with `iota` is an enumeration: write it as a
`sealed type`, or, when the numbers themselves matter to a Go API, keep the
block in a `.go` file of the same package
([porting guide](/docs/porting-from-go/#const-exported-values-and-methods-on-scalar-types)).

**Rationale.** `const` is not a GALA keyword; it is reserved as a name
([GALA-E0055](/docs/errors/gala-e0055/)). A top-level `const` used to stop the parser
with `mismatched input 'const' expecting {<EOF>, 'val', ...}`, and one inside
a function was reported as a use of the name `const`. Both are now this one
error, at the `const`.

**Scope.** A name spelled `const`, such as `val const = 1`, is still
[GALA-E0055](/docs/errors/gala-e0055/).

---

## Related

- [Porting from Go](/docs/porting-from-go/)
- [All GALA error codes](/docs/errors/)
