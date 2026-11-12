# GALA-E0091 — Constant does not fit its type

**When it fires.** An untyped constant written with literals takes the type
of the position it fills, and that type cannot hold it. The position is the
other operand of a binary operator (`x * 1.5` for an `int` x) or a slot
whose type is known: an argument, a declaration, a field. The constant does
not fit when it is:

- a number with a fractional part where an integer type is expected (`1.5`
  for an `int`);
- a number out of the integer type's range (`300` for a `byte`);
- a number where a `string` or `bool` is expected, or a string where a number
  or `bool` is.

**Minimal repro.**

```gala
package main

func main() {
    val x = 3
    Println(x * 1.5)
}
```

**Error output.**

```
error[GALA-E0091]: the constant 1.5 cannot be an int, the type of the other operand of *: it is not a whole number
  --> main.gala:5:17
  |
5 |     Println(x * 1.5)
  |                 ^^^ convert the other operand to the type you mean
  |
  = hint: convert the other operand to the type you mean (e.g. `float64(x)`); GALA converts no number implicitly
```

**Fix.** Convert the other operand, so both have the type you mean:

```gala
package main

func main() {
    val x = 3
    Println(float64(x) * 1.5)
}
```

Or write a constant the type holds (`x * 2`), or declare the value with the
type you mean (`val x = 3.0`).

**Rationale.** GALA follows Go's rules for untyped constants and converts no
number implicitly: `x * 1.5` would silently change either the constant or the
type of `x`. Such code used to pass GALA and fail in `go build`, which
reported "1.5 (untyped float constant) truncated to int" against the
generated code.
