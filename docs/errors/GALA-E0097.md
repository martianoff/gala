# GALA-E0097 — Identifier uses the reserved `_gala_` prefix

**When it fires.** An identifier starts with `_gala_`. The prefix is reserved
for names the transpiler generates, so no GALA name may use it: a local, a
parameter, a top-level function, val or var, a field, or a type.

**Minimal repro.**

```gala
package main

func main() {
    val _gala_total = 3
    Println(_gala_total)
}
```

**Error output.**

```
error[GALA-E0097]: identifier _gala_total starts with _gala_, a prefix reserved for names the transpiler generates
  --> main.gala:4:9
  |
4 |     val _gala_total = 3
  |         ^^^^^^^^^^^ rename it so it does not start with _gala_
  |
  = hint: rename it so it does not start with _gala_
```

**Fix.** Rename it:

```gala
package main

func main() {
    val total = 3
    Println(total)
}
```

**Rationale.** The generated Go uses `_gala_` names of its own. A local named
like a Go predeclared identifier or `std`, such as `len` or `string`, is
emitted as `_gala_len` or `_gala_string`, so code the transpiler generates
around it still reaches Go's `len` and `string`. Go's `fmt` may be imported as
`_gala_fmt`. A name of yours with the prefix could meet one of these and
quietly mean something else.
