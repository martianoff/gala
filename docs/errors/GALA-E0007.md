# GALA-E0007 — Slice literal not supported

**When it fires.** A GALA source file uses a Go-style slice literal
(`[]T{...}`) as an expression. Slices are not a first-class GALA construct.

**Minimal repro.**

```gala
package main

func main() {
    val xs = []int{1, 2, 3}
}
```

**Error output.**

```
[SemanticError GALA-E0007] main.gala:4:14 slice literals are not a first-class GALA construct (hint: use collection_immutable.Array or collection_mutable.Array for type-safe collections, or go_interop.SliceOf()/SliceEmpty() for Go interop)
```

**Fix.** Use a GALA collection or a Go-interop helper depending on intent:

```gala
import "collection_immutable"
import "go_interop"

val a = collection_immutable.ArrayOf(1, 2, 3)   // immutable, type-safe
val s = go_interop.SliceOf(1, 2, 3)             // raw Go []int for interop
```

**Rationale.** GALA's type system reasons about immutable collections and
explicit Go-interop shapes. A bare `[]int{...}` literal would sit outside
that model and silently escape to Go, eliminating the compile-time
guarantees that make GALA collections predictable (e.g. defensive copies,
structural sharing). The error points the author at the two canonical
replacements so the choice is explicit.
