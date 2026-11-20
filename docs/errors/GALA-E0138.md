# GALA-E0138 — Spread of a value that is not a fitting Go slice

**When it fires.** A spread argument `xs...` fills a variadic parameter
`...T`, and `xs` is not a Go slice of `T`. Go spreads only a slice whose
elements are of the parameter's type. A GALA collection (`Array`, `List`,
`HashSet` and the rest, mutable or immutable) is a struct, not a slice; so are
`Option` and your own structs. A string, a number, a map or a function is not
a slice either. And a slice of other elements does not fit: a `[]string` is not
a `[]any`, so it cannot be spread into `fmt.Println`'s `...any`. The check
covers GALA and Go functions and methods, for a spread passed by position or
by name, and the `Apply` of a companion or of a value.

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

func count(xs ...int) int = xs.Size()

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(count(xs...))
}
```

**Error output.**

```
error[GALA-E0138]: cannot spread `xs` of type collection_immutable.Array[int]: only a Go slice can be spread into a variadic parameter
  --> main.gala:9:19
  |
9 |     Println(count(xs...))
  |                   ^^ spread the Go slice it converts to
  |
  = hint: spread the Go slice it converts to; here, `xs.ToGoSlice()...`
```

A slice whose elements are of another type:

```gala
package main

import (
    "fmt"

    "martianoff/gala/go_interop"
)

func main() {
    val names = go_interop.SliceOf("a", "b")
    fmt.Println(names...)
}
```

```
error[GALA-E0138]: cannot spread `names` of type []string into a variadic parameter of any: only a []any can be spread into it
  --> main.gala:11:17
   |
11 |     fmt.Println(names...)
   |                 ^^^^^ spread a []any, or pass the elements one by one
   |
   = hint: spread a []any, or pass the elements one by one
```

**Fix.** Spread the Go slice the collection converts to. `Array`, `List`,
`HashSet` and `TreeSet`, mutable and immutable, have `ToGoSlice()`:

```gala
package main

import . "martianoff/gala/collection_immutable"

func count(xs ...int) int = xs.Size()

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(count(xs.ToGoSlice()...))
}
```

The hint names the conversion the spread value's type has: `ToGoSlice()` for
a collection, and `go_interop.ToBytes(s)` or `go_interop.ToRunes(s)` for a
string spread into `...byte` or `...rune`. A value of the parameter's own type
is passed as one argument, without `...`. For a slice of other elements, build
a slice of the parameter's type, or pass the elements one by one:
`fmt.Println("a", "b")`.

**Rationale.** A spread is lowered to a Go spread as written, and Go has no
conversion for it. The call used to pass every GALA check and fail `go build`
with `cannot use xs (variable of struct type collection_immutable.Array[int])
as []int value in argument to count`, against the generated Go. It is now this
error, at the spread argument. GALA does not convert a collection to a slice
implicitly: Go slices are for Go interop, and the conversion is written where
it happens.

**Scope.** The check fires only when the spread value is known not to fit. A
GALA alias of a slice type (`type Names []string`) is checked as the slice it
names. A Go named slice type such as `sort.StringSlice`, an interface, an
`opaque` type, `nil` and a value whose type is not resolved are left to Go. So
is the element type when it names a type parameter that inference has not
bound yet, as in a generic callee's `...T`.
