# GALA-E0014 — Default expression type mismatch

**When it fires.** A parameter's default-value literal is statically
incompatible with the parameter's declared type, or the default is an earlier
parameter of another predeclared type (`z int64 = u` for a `u int`): the
default reads that parameter's value, which does not convert implicitly.

**Minimal repro.**

```gala
package main

func create(a int = "not-an-int") int = a
//                  ^^^^^^^^^^^^ string literal does not match int
```

**Error output.**

```
[SemanticError GALA-E0014] main.gala:3:5 default for parameter "a" has type string, expected int (hint: fix the default expression or change the parameter type)
```

**Fix.** Either adjust the default expression to match the parameter type
or change the parameter type to match the intended default:

<!-- doc-check: fragment -->
```gala
func create(a int    = 0)            int    = a
func create(a string = "not-an-int") string = a
```

**Rationale.** Default-argument injection happens at the call site, so a
mismatched default would either fail to compile in Go with a less
actionable error, or (worse) silently round-trip through an implicit
conversion. Catching the mismatch at analyzer time keeps the error near
the declaration where the fix is obvious.

The check runs for literal defaults and for a default that is exactly an
earlier parameter of a predeclared type. Other expressions (like `f()` or
`x + 1`) skip it because their types depend on resolution context; they are
checked where the default is used.
