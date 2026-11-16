# GALA-E0133 — Range over a GALA type

**When it fires.** A `for ... := range x` loop ranges over a value of a GALA
struct type, or a pointer to one. Every GALA collection is a struct: `Array`,
`List`, `HashMap`, `HashSet`, `TreeMap` and `TreeSet` from
`collection_immutable`, the `collection_mutable` ones, and `Option`, `Try` and
your own structs. `range` takes what Go ranges over: a Go slice or array, a
map, a string, an integer, a channel or a function.

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    for i, x := range xs {
        Println(i, x)
    }
}
```

**Error output.**

```
error[GALA-E0133]: cannot range over `xs`: it is a collection_immutable.Array[int], and `range` takes a Go slice, array, map, string, integer, channel or function
  --> main.gala:7:23
  |
7 |     for i, x := range xs {
  |                       ^^ iterate with a method
  |
  = hint: iterate with a method; here, `xs.ForEach((x) => ...)`, `xs.ZipWithIndex()` for the index, `xs.FoldLeft(init, (acc, x) => ...)` for an accumulator, or `range xs.ToGoSlice()` if a Go loop is really needed
```

**Fix.** Call the collection's method for what the loop does. `ForEach`
visits each element, `ZipWithIndex()` pairs each with its index, `FoldLeft`
carries an accumulator, and `Map`, `Filter` and the rest build a new
collection:

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2, 3)
    xs.ZipWithIndex().ForEach((p) => Println(p.V2, p.V1))
    Println(xs.FoldLeft(0, (acc, x) => acc + x))
}
```

A map's methods take the key and the value: `m.ForEachKV((k, v) => ...)` and
`m.FoldLeftKV(init, (acc, k, v) => ...)`. When a Go loop is really needed,
range over the Go slice or map the collection converts to, `xs.ToGoSlice()` or
`m.ToGoMap()`. The hint names the methods the ranged type has, spelled on the
expression written.

For a struct of your own, the hint names its fields that are a Go slice or map,
to range over one of them, or says to give the type a method that visits its
elements. An expression that is not a single operand, such as an
`if`-expression, is parenthesized in the hint.

**Rationale.** `range` is lowered to a Go range statement as written, and Go
cannot range over a struct. The loop used to pass every GALA check and fail
`go build` with `cannot range over xs (variable of struct type
collection_immutable.Array[int])`, against the generated Go. It is now this
error, at the ranged expression.

**Scope.** The check fires only when the ranged type is known to be a GALA
struct. A Go type, including a Go named slice or map type such as
`http.Header`, an interface, an `opaque` type and a value whose type is not
resolved are left to Go.
