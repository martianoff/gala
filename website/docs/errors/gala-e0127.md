---
layout: default
title: "GALA-E0127 — Go Type-Set Constraint"
description: "GALA-E0127 fires on a Go type-set constraint, a union int | float64 or an approximation ~int. Use a constraint such as cmp.Ordered, or declare one in a .go file."
keywords: "gala-e0127, gala union constraint, gala ~int, gala tilde, gala type set, gala cmp.Ordered, gala generics constraint"
permalink: /docs/errors/gala-e0127/
last_modified_at: 2026-11-15
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0127</p>

# GALA-E0127 — Go type-set constraint

**When it fires.** A constraint is written as a Go type set: a union,
`int | float64`, or an approximation, `~int`, in an interface
(`type Number interface { ~int | ~float64 }`) or in a type parameter's
constraint (`func sum[T int | float64](...)`, `[T ~string]`). GALA has no
union or `~` constraints.

**Minimal repro.**

```gala
package main

type Number interface {
    ~int | ~float64
}

func main() {
    Println("numbers")
}
```

**Error output.**

```
error[GALA-E0127]: Go type-set constraint `~int | ~float64`: GALA has no union or `~` constraints
  --> main.gala:4:5
  |
4 |     ~int | ~float64
  |     ^^^^^^^^^^^^^^^ use a constraint from Go, such as `cmp.Ordered`
  |
  = hint: use a constraint from Go, such as `cmp.Ordered`; a type-set interface of your own is declared in a `.go` file of the package
```

**Fix.** Use a constraint Go already declares, such as `cmp.Ordered` for the
types with `<`:

```gala
package main

import "cmp"

func largest[T cmp.Ordered](a T, b T) T = if (a > b) a else b

func main() {
    Println(largest(3, 7), largest(2.5, 1.5))
}
```

A type-set interface of your own is declared in a `.go` file of the same
package, and GALA code uses it by name like any other constraint.

**Rationale.** The lexer has no `~` token, so an approximation used to start
with `token recognition error at: '~'`, and a union with `mismatched input
'|' expecting {'(', '['}`, as if the line were a method. It is now this one
error at the constraint.

**Scope.** An embedded interface in an interface is
[GALA-E0126](/docs/errors/gala-e0126/).

---

## Related

- [Porting from Go](/docs/porting-from-go/)
- [All GALA error codes](/docs/errors/)
