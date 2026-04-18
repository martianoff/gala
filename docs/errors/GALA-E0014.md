# GALA-E0014 — Default expression type mismatch

**When it fires.** A parameter's default-value literal is statically
incompatible with the parameter's declared type.

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

```gala
func create(a int    = 0)            int    = a
func create(a string = "not-an-int") string = a
```

**Rationale.** Default-argument injection happens at the call site, so a
mismatched default would either fail to compile in Go with a less
actionable error, or (worse) silently round-trip through an implicit
conversion. Catching the mismatch at analyzer time keeps the error near
the declaration where the fix is obvious.

Currently the check runs only for literal defaults — non-literal
expressions (like `f()` or `x + 1`) skip the check because their types
depend on resolution context. The literal subset catches the cases most
likely to be typos.
