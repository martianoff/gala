# GALA-E0011 — Type redefined

**When it fires.** A `type` / `struct-shorthand` / `sealed type` with the
same name is declared more than once in the same package (across any
combination of files).

**Minimal repro — two files.**

```gala
// a.gala
package main

type User struct {
    Name string
}
```

```gala
// b.gala
package main

type User struct {
    Email string
}
```

**Error output.**

```
Error transpiling a.gala: [SemanticError GALA-E0011] a.gala:3:5 type "User" in package "main" redefined (also declared at b.gala:3) (hint: remove the duplicate declaration or rename one of the types)
Error transpiling b.gala: [SemanticError GALA-E0011] b.gala:3:5 type "User" in package "main" redefined (also declared at a.gala:3) (hint: remove the duplicate declaration or rename one of the types)
```

Every file of the package is compiled, so both declarations are reported,
each naming the *other* one. Batch analysis order is not source order, so
the message does not claim either declaration came first — it points at the
other declaration site and leaves the choice of which to delete to you.

**Minimal repro — one file.**

```gala
// a.gala
package main

type User struct {
    Name string
}

type User struct {
    Email string
}
```

**Error output.**

```
Error transpiling a.gala: [SemanticError GALA-E0011] a.gala:7:0 type "User" in package "main" redefined (also declared at line 3) (hint: remove the duplicate declaration or rename one of the types)
```

When the other declaration is in the file the error is reported against, the
message gives its line instead of repeating the file name.

**Fix.** Delete one of the declarations, merge their fields, or rename one
of the types so both can coexist.

**Rationale.** The transpiler builds a single canonical metadata entry per
type; a second declaration would silently overwrite the first, producing
surprising method-dispatch behavior and generated Go that references fields
that no longer exist. Catching the duplicate at the analyzer layer makes
the collision obvious.
