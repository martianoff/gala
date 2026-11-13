# GALA-E0105 — Positional argument after a named argument

**When it fires.** A call passes a positional argument after a named one, as in
`f(a = 1, 2)`. Once an argument is named, every argument after it must be named
too. The rule is the same for every call form: function and method calls,
struct and sealed case constructors, and `Copy()`. The message names the first
positional argument that follows a named one.

**Minimal repro.**

```gala
package main

func divide(dividend int, divisor int) int = dividend / divisor

func main() {
    Println(divide(dividend = 20, 4))
}
```

**Error output.**

```
error[GALA-E0105]: positional argument `4` follows named argument `dividend`
  --> main.gala:6:35
  |
6 |     Println(divide(dividend = 20, 4))
  |                                   ^ name it, or move it before the named arguments
  |
  = hint: name it, or move it before the named arguments
```

**Fix.** Name the argument too, or move it before the named arguments. A
`Copy()` takes named overrides only, so name the argument there:

```gala
package main

func divide(dividend int, divisor int) int = dividend / divisor

func main() {
    Println(divide(dividend = 20, divisor = 4))
    Println(divide(20, divisor = 4))
}
```

**Rationale.** Positional arguments fill the leading parameters (or fields) in
order. After a named argument there is no position left for a positional one to
take: in `f(b = 1, 2)`, the `2` could be meant for the first parameter or for
the one after `b`. With the positional arguments first, a call reads one way
only. Named arguments are still evaluated in the order they are written.
