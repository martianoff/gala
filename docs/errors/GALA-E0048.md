# GALA-E0048 — method on an alias to a non-local type

**When it fires.** A method is declared on a type alias whose target this
package did not declare — a built-in, or a type from another package:

```gala
type Millis int64

func (d Millis) Value() int64 = int64(d)
```

**Minimal repro.**

```gala
package main

type DateTime int64

func (d DateTime) Millis() int64 = int64(d)

func main() {
    Println(DateTime(5).Millis())
}
```

**Error output.**

```
error[GALA-E0048]: cannot declare a method on "DateTime": "DateTime" aliases the built-in type "int64"
  --> main.gala:5:6
  |
5 | func (d DateTime) Millis() int64 = int64(d)
  |      ^ a type alias is the same type as its target, so it takes no…
  |
  = hint: a type alias is the same type as its target, so it takes no methods of its own — declare a struct that wraps the value, or write the method as a plain function
```

**Fix.** Wrap the value in a struct, which gives it an identity of its own and
somewhere for the methods to live:

```gala
struct DateTime(Value int64)

func (d DateTime) Millis() int64 = d.Value
```

A plain function works too when no method is needed:

```gala
type DateTime int64

func millisOf(d DateTime) int64 = int64(d)
```

**Rationale.** `type X Y` is an *alias*, not a new type: it lowers to Go's
`type X = Y`, so `X` and `Y` are one type and the method's receiver base type
is `Y`. Go permits a method only on a type its own package declares, so an
alias to `int64` or to `time.Duration` is not a legal receiver.

The declaration used to be emitted unchecked, which meant the rejection arrived
from `go build` against generated code:

```
main.gala:5: cannot define new methods on non-local type DateTime
```

That names a Go rule, and a locality property of generated code, for a type the
author declared in GALA — and it appeared only at build time, after a clean
transpile.

**Where it stands down.** An alias whose target is declared in **this** package
is a legal receiver, because the base type is then local. This keeps working:

```gala
struct Point(X int, Y int)

type Coord Point

func (c Coord) Sum() int = c.X + c.Y
```

Everything else an alias does is unaffected — annotating a type, converting
(`Millis(v)`), and constructing through an alias to a struct (`Coord(1, 2)`).

**Scope.** This code covers methods on aliases. The alias rules as a whole are
in [Type Aliases](../GALA.MD#type-aliases); GALA has no newtype declaration, so
a distinct type with its own methods is a single-field struct.
