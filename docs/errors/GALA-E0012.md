# GALA-E0012 — Method redefined

**When it fires.** A method with the same name is declared twice on the same
type (across any combination of files).

**Minimal repro.**

```gala
// a.gala
package main
type User struct { Name string }
func (u User) Greet() string = "hello"

// b.gala
package main
func (u User) Greet() string = "hola"   // duplicate
```

**Error output.**

```
[SemanticError GALA-E0012] b.gala:2:5 method "Greet" on type "User" in package "main" redefined (first defined in a.gala) (hint: remove the duplicate method or rename it)
```

**Fix.** Delete one of the definitions or rename the second method.

**Rationale.** Method resolution uses a single map keyed by method name; a
second declaration would silently replace the first, making the behavior
order-of-loading dependent and hard to debug. This also mirrors Go's own
rule — Go forbids duplicate methods on the same receiver.
