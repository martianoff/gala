# GALA-E0142 — Range over a type parameter with no core type

**When it fires.** A `for ... := range x` loop ranges over a value whose type
is a type parameter `T`, and `T`'s constraint has no core type. Go ranges over
a value of a type parameter only when every type its constraint allows has
the same underlying type (its core type), and ranges as that type. These
constraints have none:

- `any` and `comparable`;
- an interface with methods only, declared in GALA or Go;
- a Go interface whose types have different underlying types, such as
  `interface{ ~int | ~int64 }`, or `constraints.Integer`, or a union with
  `any`.

A Go constraint with a core type ranges as that type: `interface{ ~int64 }`
binds the loop variable to `T`, and `interface{ ~[]string }` ranges as a
slice. A Go constraint that is not known to lack a core type is left to Go:
one whose type information is not available (no Go SDK), one naming a type
that could not be resolved, and any set holding a channel (Go merges channels
that differ only in direction into one core type).

**Minimal repro.** A hand-written Go file of the package declares the
constraint:

```go
package main

type Number interface{ ~int | ~int64 }
```

<!-- doc-check: fragment -->
```gala
package main

func steps[T Number](n T) {
    for i := range n {
        Println(i)
    }
}

func main() {
    steps(3)
}
```

**Error output.**

```
error[GALA-E0142]: cannot range over `n`: its type T is constrained by Number, which has no core type: the types it allows do not share one underlying type
  --> main.gala:4:20
  |
4 |     for i := range n {
  |                    ^ range over a value of a concrete type, or constrain the type…
  |
  = hint: range over a value of a concrete type, or constrain the type parameter by an interface declared in a .go file of the package whose types share one underlying type (`interface{ ~int64 }`)
```

**Fix.** Range over a value of a concrete type, such as an `int` count. Or
constrain `T` by an interface whose types share one underlying type. GALA
interfaces hold methods only, so declare it in a `.go` file of the package:

```go
type Number interface{ ~int64 }
```

Then `for i := range n` binds `i` to `T`. The call must pass an `int64`-based
value too, since an untyped `3` is an `int`: `steps(int64(3))`, or
`steps(time.Nanosecond * 3)`.

**Rationale.** Go rejects a range over such a value with "cannot range over n
(variable of type T constrained by Number): int and int64 have different
underlying types". The loop used to pass every GALA check, with the loop
variable typed `int`, and fail only in `go build`. The error now comes from
GALA, at the range expression.
