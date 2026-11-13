---
layout: default
title: "GALA-E0108 — A Type's Own Equal Passes Its Type to a Comparable Type Parameter"
description: "GALA-E0108 fires when a type's own Equal calls a generic function with that type as a comparable type argument: == on the type parameter would call Equal again, forever."
keywords: "gala-e0108, gala equal recursion, gala comparable, gala equality, gala stack overflow equal"
permalink: /docs/errors/gala-e0108/
last_modified_at: 2026-11-13
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0108</p>

# GALA-E0108 — A type's own Equal passes its type to a comparable type parameter

**When it fires.** A type's own `Equal` method calls a generic function or
method, and the call's type argument for a type parameter constrained by
`comparable` (or by a Go interface that embeds it) is that same type, or a
type that holds it by value: `Option[Point]`, a tuple, a `List[Point]`. `==`
on such a type parameter calls the value's `Equal`, so `Equal` and the helper
would call each other until the stack runs out.

`Equal` reaches such a call through a method too: a method of a generic
type is compiled with the type's `comparable` type parameter, so
`Pair(p, o).Same()` for `struct Pair[T comparable]` is the call `same(p, o)`
is. And it reaches one through the functions and methods of the same file
that it calls: `eq(p, o)` for `func eq(a Point, b Point) bool = same(a, b)`.
The error then points at the call in `Equal` and names the path to the
generic call.

**Minimal repro.**

```gala
package main

func same[T comparable](a T, b T) bool = a == b

struct Point(x int, label string)

func (p Point) Equal(o Point) bool = same(p.label, o.label) && same(p, o)

func main() {
    Println(Point(1, "a") == Point(2, "a"))
}
```

**Error output.**

```
error[GALA-E0108]: Point's own Equal calls same with T = Point, a comparable type parameter: == on T there calls Point's Equal, so the two call each other forever
  --> main.gala:7:69
  |
7 | func (p Point) Equal(o Point) bool = same(p.label, o.label) && same(p, o)
  |                                                                     ^^^^ compare Point's fields instead
  |
  = hint: compare Point's fields instead
```

**Fix.** Compare the fields, with the helper or with `==`:

```gala
package main

func same[T comparable](a T, b T) bool = a == b

struct Point(x int, label string)

func (p Point) Equal(o Point) bool = same(p.label, o.label) && same(p.x, o.x)

func main() {
    Println(Point(1, "a") == Point(2, "a"))
}
```

`==` written in `Equal` itself on the type's own values is Go's `==`, so
`p == o` there does not recurse either.

**Rationale.** A generic function is compiled once for all its callers, so
`==` in it cannot be Go's `==` for one caller and `Equal` for the others.
The check follows the functions and methods of the file that `Equal` calls.
It does not see a call made through a function value, or one made by a
function or method declared in another file or package; such a call recurses
the same way.

---

## Related

- [Equality](/docs/language-reference/#equality--and-)
- [All GALA error codes](/docs/errors/)
