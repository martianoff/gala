---
layout: default
title: "GALA-E0008 — Map Literals Are Not a First-Class GALA Construct"
description: "GALA-E0008 rejects Go-style map literals like map[string]int{...}. See the triggering code, the compiler message, and how to use collection_immutable.HashMap or go_interop map helpers instead."
keywords: "gala-e0008, map literals are not a first-class gala construct, gala map literal, gala hashmap, go_interop mapempty, gala collections error"
permalink: /docs/errors/gala-e0008/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0008</p>

# GALA-E0008 — Map literal not supported

**What it means.** The source uses a Go-style map literal (`map[K]V{…}`) as an expression. Maps, like slices, are not a first-class GALA construct.

---

## Code that triggers it

```gala
package main

func main() {
    val m = map[string]int{"a": 1, "b": 2}
    Println(m)
}
```

---

## Compiler message

```
error[GALA-E0008]: map literals are not a first-class GALA construct
  --> e0008.gala:4:13
  |
4 |     val m = map[string]int{"a": 1, "b": 2}
  |             ^^^ use collection_immutable.HashMap or collection_mutable.HashM…
  |
  = hint: use collection_immutable.HashMap or collection_mutable.HashMap for type-safe maps, or go_interop.MapEmpty()/MapPut() for Go interop
```

---

## How to fix it

```gala
import . "martianoff/gala/collection_immutable"
import "martianoff/gala/go_interop"

val m = HashMapOf(("a", 1), ("b", 2))                            // immutable GALA map
val g = go_interop.MapPut(go_interop.MapEmpty[string, int](), "a", 1)   // raw Go map
```

---

## Why the rule exists

Same reasoning as [GALA-E0007](/docs/errors/gala-e0007/): GALA reasons about its own map types. Raw Go map literals erase the GALA-side invariants — an immutable `HashMap` can be shared without defensive copies, a Go map cannot. Forcing the caller to name one of the two supported paths keeps that distinction visible at the call site.

---

## Related

- [Collections](/features/collections/) — `HashMap`, `TreeMap`, `HashSet`, `TreeSet`
- [Go Interop](/features/go-interop/)
- [GALA-E0007](/docs/errors/gala-e0007/) — the same rule for slice literals
- [All GALA error codes](/docs/errors/)
