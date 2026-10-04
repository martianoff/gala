# GALA-E0065 — Opaque-type pattern with the wrong number of sub-patterns

**When it fires.** A pattern names an opaque type with other than exactly one
sub-pattern: `case Point(x, y)` or `case UserID()`. An opaque type wraps one
value, its underlying value, so its pattern takes one sub-pattern for it.

**Minimal repro.**

```gala
package main

opaque type Point int64

func describe(p Point) string = p match {
    case Point(x, y) => s"$x,$y"
    case _ => "?"
}

func main() {
    Println(describe(Point(3)))
}
```

**Error output.**

```
error[GALA-E0065]: the opaque-type pattern Point(...) takes exactly one sub-pattern, got 2
  --> main.gala:6:10
  |
6 |     case Point(x, y) => s"$x,$y"
  |          ^^^^^ Point unwraps to its one underlying value: write Point(v) to…
  |
  = hint: Point unwraps to its one underlying value: write Point(v) to bind it, Point(_) to ignore it
```

**Fix.** Bind the underlying value with one name, match it against a literal,
or ignore it with `_`:

```gala
package main

opaque type Point int64

func describe(p Point) string = p match {
    case Point(0) => "origin"
    case Point(v) => s"point $v"
    case _ => "?"
}

func main() {
    Println(describe(Point(3)))
}
```

If the value really has two parts, it is a struct, not an opaque type:
`struct Point(X int64, Y int64)`, matched with `case Point(x, y)`.

**Rationale.** `opaque type Point int64` lowers to the Go defined type
`type Point int64`; the pattern `case Point(v)` lowers to the conversion
`v := int64(p)`. There is no generated `Unapply` with a field list for the
sub-patterns to line up with, so any count but one has nothing to match.

**Related.** [Opaque Types](../GALA.MD#opaque-types).
