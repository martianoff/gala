# GALA-E0139 — Read of `_`

**When it fires.** A `_` is read as a value where its innermost binding is a
lambda parameter written `_`, a typed pattern `case _: T` or a `bind _ = m`
clause. Such a `_` binds the value without naming it, so there is nothing to
read: `(_) => _ + 1` and `case _: int => _` have no value for the second `_`.
It fires inside an interpolated string too (`(_) => s"v${_}"`). A placeholder
lambda's `_` is its parameter, so `_ + 1` passed where a function is expected
is not this error, even inside a lambda with a `_` parameter.

An increment, decrement or compound assignment of `_` (`_++`, `_--`,
`_ += x`) is the same error, with or without a `_` binding in scope: each
reads `_` before writing it. Only `=` writes to `_`, and `_ = x` discards x.

A `_` that nothing binds is the same error when it is read, as in `inc(_)`
where `inc` takes an `int`: no placeholder lambda takes that `_`.

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2)
    Println(xs.Map((_) => _ + 1))
}
```

**Error output.**

```
error[GALA-E0139]: `_` reads a `_` binding, which names no value
  --> main.gala:7:27
  |
7 |     Println(xs.Map((_) => _ + 1))
  |                           ^ bind a name instead of `_` and read that name
  |
  = hint: bind a name instead of `_` and read that name; for example `(x) => x + 1` or `case n: int => n`
```

**Fix.** Name the parameter and read the name, or drop the parameter list and
write a placeholder lambda:

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2)
    Println(xs.Map((x) => x + 1))
    Println(xs.Map(_ + 1))
}
```

For a typed pattern, bind a name: `case n: int => n` in place of
`case _: int => _`. A `_` parameter or `case _: T` that is never read is fine.
In place of `_ += x`, use a named variable.

**Rationale.** `_` in a parameter list or a pattern says the value is not
needed. Reading it contradicts that, and Go has no value for its blank
identifier, so the program used to fail `go build` with "cannot use _ as value
or type". GALA reports the read at the place it is written.
