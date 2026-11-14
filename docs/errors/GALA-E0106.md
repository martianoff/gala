# GALA-E0106 — Type named like a Go predeclared type, builtin function or std

**When it fires.** A type is declared with one of these names:

- a Go predeclared type: `bool`, `byte`, `rune`, `string`, `error`, `any`,
  `comparable`, `int`, `int8`, `int16`, `int32`, `int64`, `uint`, `uint8`,
  `uint16`, `uint32`, `uint64`, `uintptr`, `float32`, `float64`, `complex64`
  or `complex128`;
- a Go builtin function: `append`, `cap`, `clear`, `close`, `complex`,
  `copy`, `delete`, `imag`, `len`, `make`, `max`, `min`, `new`, `panic`,
  `print`, `println`, `real` or `recover`;
- `std`, the name of GALA's standard library package.

The rule covers every kind of type declaration: a shorthand or block struct, an
interface, an alias, an opaque type, a sealed type and each of its cases, and a
type parameter, at the top level or in a body.

**Minimal repro.**

```gala
package main

struct error(Code int, Msg string)

func lookup(code int) error = error(code, "not found")

func main() {
    Println(lookup(404).Msg)
}
```

**Error output.**

```
error[GALA-E0106]: type error is named like a Go predeclared type
  --> main.gala:3:8
  |
3 | struct error(Code int, Msg string)
  |        ^^^^^ rename the type
  |
  = hint: rename the type; GALA reads error as Go's own type wherever it is written
```

**Fix.** Rename the type:

```gala
package main

struct NotFound(Code int, Msg string)

func lookup(code int) NotFound = NotFound(code, "not found")

func main() {
    Println(lookup(404).Msg)
}
```

**Rationale.** GALA reads a predeclared type name as Go's type in every type
position, so a type declared with that name could not be written as a type. In the
repro, the result type `error` is Go's `error` interface, not the struct, and
the program used to fail on an unrelated-looking message (`error does not
implement error`). A value, a field or a function may still take such a name:
there the name means the binding, and in a type position Go's type.

A type named like a builtin function or `std` hides, in the Go GALA generates
for its package, the builtin or the package that Go calls, so the program
would fail in `go build` or depend on which helpers the transpiler happens to
use. The message and hint say which kind of name it is:

```gala
package main

struct len(Items int)

func main() {
    Println(len(3).Items)
}
```

```
error[GALA-E0106]: type len is named like a Go builtin function
  --> main.gala:3:8
  |
3 | struct len(Items int)
  |        ^^^ rename the type
  |
  = hint: rename the type; it would hide Go's builtin len, which the generated Go calls
```
