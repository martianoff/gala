# GALA-E0011 — Type redefined

**When it fires.** A `type` / `struct-shorthand` / `sealed type` with the
same name is declared more than once in the same package (across any
combination of files).

**Minimal repro.**

```gala
// a.gala
package main
type User struct { Name string }

// b.gala
package main
type User struct { Email string }   // redefines User
```

**Error output.**

```
[SemanticError GALA-E0011] b.gala:2:5 type "User" in package "main" redefined (first defined in a.gala) (hint: remove the duplicate declaration or rename one of the types)
```

**Fix.** Delete one of the declarations, merge their fields, or rename one
of the types so both can coexist.

**Rationale.** The transpiler builds a single canonical metadata entry per
type; a second declaration would silently overwrite the first, producing
surprising method-dispatch behavior and generated Go that references fields
that no longer exist. Catching the duplicate at the analyzer layer makes
the collision obvious.
