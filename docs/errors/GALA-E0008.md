# GALA-E0008 — Map literal not supported

**When it fires.** A GALA source file uses a Go-style map literal
(`map[K]V{...}`) as an expression. Maps are not a first-class GALA construct.

**Minimal repro.**

```gala
package main

func main() {
    val m = map[string]int{"a": 1, "b": 2}
}
```

**Error output.**

```
[SemanticError GALA-E0008] main.gala:4:13 map literals are not a first-class GALA construct (hint: use collection_immutable.HashMap or collection_mutable.HashMap for type-safe maps, or go_interop.MapEmpty()/MapPut() for Go interop)
```

**Fix.** Use a GALA `HashMap` or Go-interop helpers:

```gala
import "collection_immutable"
import "go_interop"

val m  = collection_immutable.HashMapOf(("a", 1), ("b", 2))
val g  = go_interop.MapPut(go_interop.MapEmpty[string, int](), "a", 1)
```

**Rationale.** Same as GALA-E0007: GALA reasons about its own immutable map
types. Allowing raw Go map literals would erase the GALA-side invariants
(unordered iteration semantics aside, immutable maps can safely be shared
without defensive copies). Forcing the caller to name one of the two
supported paths keeps that distinction visible at the call site.
