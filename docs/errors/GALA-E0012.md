# GALA-E0012 — Method redefined

**When it fires.** A method with the same name is declared twice on the same
type (across any combination of files).

**Minimal repro — two files.** A method may be declared in a different file
from its receiver type, so the duplicate here is only visible once the whole
package is on the table.

```gala
// a.gala
package main

type User struct {
    Name string
}

func (u User) Greet() string = "hello"
```

```gala
// b.gala
package main

func (u User) Greet() string = "hola"   // duplicate
```

**Error output.**

```
Error transpiling a.gala: [SemanticError GALA-E0012] a.gala:7:14 method "Greet" on type "User" in package "main" redefined (also declared at b.gala:3) (hint: remove the duplicate method or rename it)
Error transpiling b.gala: [SemanticError GALA-E0012] b.gala:3:14 method "Greet" on type "User" in package "main" redefined (also declared at a.gala:7) (hint: remove the duplicate method or rename it)
```

Every file of the package is compiled, so both definitions are reported, each
naming the *other* one. Batch analysis order is not source order, so the
message does not claim either definition came first — it points at the other
definition site and leaves the choice of which to delete to you.

Declaring a method in a different file from its receiver type is otherwise
fine, and stays fine: only a second definition of the *same* method name on
the same type is rejected.

**Minimal repro — one file.**

```gala
// a.gala
package main

type User struct {
    Name string
}

func (u User) Greet() string = "hello"

func (u User) Greet() string = "hola"
```

**Error output.**

```
Error transpiling a.gala: [SemanticError GALA-E0012] a.gala:9:0 method "Greet" on type "User" in package "main" redefined (also declared at line 7) (hint: remove the duplicate method or rename it)
```

When the other definition is in the file the error is reported against, the
message gives its line instead of repeating the file name.

**Fix.** Delete one of the definitions or rename the second method.

**Rationale.** Method resolution uses a single map keyed by method name; a
second declaration would silently replace the first, making the behavior
order-of-loading dependent and hard to debug. This also mirrors Go's own
rule — Go forbids duplicate methods on the same receiver. Reporting it here
means the duplicate is named in GALA source terms rather than reaching the Go
compiler as `method User.Greet already declared` against generated code.
