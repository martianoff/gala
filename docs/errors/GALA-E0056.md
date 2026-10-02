# GALA-E0056 — Malformed tuple destructuring

**When it fires.** A tuple destructuring declaration, `val (a, b) = pair` or
`var (a, b) = pair`, is written with something the grammar allows in that slot
but a destructuring cannot use:

- a type annotation: `var (a, b) Tuple[int, int] = pair`
- no initializer: `var (a, b)`
- more than one expression on the right: `var (a, b) = x, y`
- a name count that differs from the tuple's: `var (a, b, c) = (1, 2)`

**Minimal repro.**

```gala
package main

func bounds() Tuple[int, int] = (3, 9)

func main() {
    var (lo, hi) Tuple[int, int] = bounds()
    lo = lo * 2
    Println(s"$lo..$hi")
}
```

**Error output.**

```
error[GALA-E0056]: a tuple destructuring `var (...)` takes no type annotation
  --> main.gala:6:18
  |
6 |     var (lo, hi) Tuple[int, int] = bounds()
  |                  ^^^^^^^^^^^^^^^ remove the type
  |
  = hint: remove the type; each name takes its type from the tuple
```

A `var` destructuring with nothing to destructure:

```gala
package main

func main() {
    var (lo, hi)
    lo = 3
    hi = 9
    Println(s"$lo..$hi")
}
```

```
error[GALA-E0056]: a tuple destructuring `var (...)` needs an initializer
  --> main.gala:4:5
  |
4 |     var (lo, hi)
  |     ^^^^^^^^^^^^ add the tuple to split: `= pair`
  |
  = hint: add the tuple to split: `= pair`; a variable with no value declares its own type instead, as in `var a int`
```

**Fix.** Write the tuple after `=` and let each name take its component's type:

```gala
package main

func bounds() Tuple[int, int] = (3, 9)

func main() {
    var (lo, hi) = bounds()
    lo = lo * 2
    Println(s"$lo..$hi")
}
```

To declare variables that start without a value, declare each one with its own
type, `var lo int`, rather than as a destructuring.

**Rationale.** `val` and `var` share their declaration grammar with the plain
`val a T = x` form, so the type and initializer slots parse after a tuple
pattern too. A destructuring, though, takes every name's type from the one
tuple it splits: an annotation would name the tuple's type, not any variable's,
and a destructuring with no tuple has nothing to take types or values from.
Neither had a meaning, and before this code a `var (a, b)` declaration stopped
the transpiler with an internal error (GALA-E0017).

The grammar ignores newlines between a declaration's parts, so in the second
repro the `lo` of the next line parses as a type. The declaration as written
still has no initializer, and that is what the error reports.

**Scope.** Destructuring in a `match` arm, `case (a, b) =>`, is a pattern, not
a declaration, and is not affected. See
[Tuple](../GALA.MD#tuple) for destructuring in declarations.
