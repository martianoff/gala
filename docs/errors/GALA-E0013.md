# GALA-E0013 — Non-defaulted parameter after defaulted parameter

**When it fires.** A function signature declares a parameter without a
default after a parameter that has one. Defaults must be contiguous and at
the tail of the parameter list.

**Minimal repro.**

```gala
package main

func create(a int = 5, b int) int = a + b
//                     ^^^^^^ no default, but follows a default
```

**Error output.**

```
[SemanticError GALA-E0013] main.gala:3:5 parameter "b" in create has no default but follows a parameter with a default (hint: move parameters with defaults to the end of the parameter list)
```

**Fix.** Reorder the parameters so all defaults come last, or give the
follower a default too:

```gala
func create(b int, a int = 5) int = a + b
// or
func create(a int = 5, b int = 0) int = a + b
```

**Rationale.** With mixed defaults the call-site rules become ambiguous:
what does `create(7)` mean — `a = 7, b = default?` or `a = default, b = 7`?
Requiring contiguous trailing defaults makes positional calls unambiguous
while still allowing named-argument calls to pick any subset.
