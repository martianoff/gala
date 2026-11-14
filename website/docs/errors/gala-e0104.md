---
layout: default
title: "GALA-E0104 — Type Parameter Only a Constraint Determines Cannot Be Inferred"
description: "GALA-E0104 fires when a generic call leaves a type parameter a lambda needs unbound, because only another parameter's constraint mentions it and that type argument's methods do not determine it."
keywords: "gala-e0104, gala cannot infer type parameter, gala constraint inference, gala f-bounded generics, gala type arguments"
permalink: /docs/errors/gala-e0104/
last_modified_at: 2026-11-13
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0104</p>

# GALA-E0104 — Type parameter only a constraint determines cannot be inferred

**When it fires.** A call of a generic function or method leaves a type parameter
unbound that a lambda argument needs, and the parameter appears only in the
constraint of another type parameter (`T` in `[T any, C Labeled[T]]`). GALA
infers such a parameter, as Go does, from the methods of the other
parameter's type argument: for `C Seq[T, C]` and an argument of type `Nums`,
`Nums.Keep(p func(int) bool)` gives `T = int`. When those methods do not
mention it, nothing determines it, and the lambda would have no parameter
type.

**Minimal repro.**

```gala
package main

type Labeled[T any] interface {
    Label() string
}

struct Box(n int)

func (b Box) Label() string = s"box ${b.n}"

func describe[T any, C Labeled[T]](c C, show func(T) string) string = c.Label()

func main() {
    Println(describe(Box(1), (x) => s"$x"))
}
```

**Error output.**

```
error[GALA-E0104]: cannot infer T in this call of describe: T appears only in Labeled[T], the constraint of C, and the methods of C's type argument Box do not determine it
  --> main.gala:14:22
   |
14 |     Println(describe(Box(1), (x) => s"$x"))
   |                      ^^^^^^^^^^^^^^^^^^^^ write the type arguments: describe[<T>, Box](...)
   |
   = hint: write the type arguments: describe[<T>, Box](...)
```

**Fix.** Write the type arguments, the one inference cannot find included:

```gala
package main

type Labeled[T any] interface {
    Label() string
}

struct Box(n int)

func (b Box) Label() string = s"box ${b.n}"

func describe[T any, C Labeled[T]](c C, show func(T) string) string = c.Label()

func main() {
    Println(describe[int, Box](Box(1), (x) => s"$x"))
}
```

Or annotate the lambda's parameter (`(x int) => s"$x"`), which binds `T` from
the lambda.

**Rationale.** A type parameter that only a constraint mentions is determined
by the methods of the constrained parameter's type argument, if at all. When
they do not determine it, Go cannot infer it either; without this check, the
lambda was lowered against `any` and the call failed in `go build`.

---

## Related

- [Language reference](/docs/language-reference/)
- [All GALA error codes](/docs/errors/)
