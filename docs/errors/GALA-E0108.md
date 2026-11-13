# GALA-E0108 — A type's own Equal passes its type to a comparable type parameter

**When it fires.** A type's own `Equal` method calls a generic function or
method, and the call's type argument for a type parameter constrained by
`comparable` (or by a Go interface that embeds it) is that same type, or a
type that holds it by value: `Option[Point]`, a tuple, a `List[Point]`. `==`
on such a type parameter calls the value's `Equal`, so `Equal` and the helper
would call each other until the stack runs out.

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
The check covers the call `Equal` makes itself. A helper that `Equal` calls
and that passes the value on to another generic function is not checked, and
recurses the same way.
