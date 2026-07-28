---
layout: default
title: "GALA-E0007 — Slice Literals Are Not a First-Class GALA Construct"
description: "GALA-E0007 rejects Go-style slice literals like []int{1,2,3}. See the triggering code, the compiler message, and the two supported replacements: collection_immutable.ArrayOf and go_interop.SliceOf."
keywords: "gala-e0007, slice literals are not a first-class gala construct, gala slice literal, gala arrayof, go_interop sliceof, gala collections error"
permalink: /docs/errors/gala-e0007/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0007</p>

# GALA-E0007 — Slice literal not supported

**What it means.** The source uses a Go-style slice literal (`[]T{…}`) as an expression. Slices are not a first-class GALA construct — they exist only behind the Go-interop boundary.

---

## Code that triggers it

```gala
package main

func main() {
    val xs = []int{1, 2, 3}
    Println(xs)
}
```

---

## Compiler message

```
error[GALA-E0007]: slice literals are not a first-class GALA construct
  --> e0007.gala:4:14
  |
4 |     val xs = []int{1, 2, 3}
  |              ^ use collection_immutable.Array or collection_mutable.Array f…
  |
  = hint: use collection_immutable.Array or collection_mutable.Array for type-safe collections, or go_interop.SliceOf()/SliceEmpty() for Go interop
```

---

## How to fix it

Pick the replacement that matches your intent:

```gala
import . "martianoff/gala/collection_immutable"
import "martianoff/gala/go_interop"

val a = ArrayOf(1, 2, 3)              // immutable, type-safe GALA collection
val s = go_interop.SliceOf(1, 2, 3)   // raw Go []int, for passing to a Go API
```

Reach for `go_interop.SliceOf` only when a Go function actually demands a slice. Everything else — mapping, filtering, folding, sharing across goroutines — is better served by `Array` or `List`.

---

## Why the rule exists

GALA's type system reasons about immutable collections and explicit Go-interop shapes. A bare `[]int{…}` sits outside that model and escapes silently into Go, discarding the compile-time guarantees that make GALA collections predictable (defensive copies, structural sharing, and [shareability across goroutines](/docs/errors/gala-e0037/)). The error names the two canonical replacements so the choice stays explicit.

---

## Related

- [Collections](/features/collections/) — `Array`, `List`, `HashMap`, and their mutable variants
- [Go Interop](/features/go-interop/) — when and how to cross into Go types
- [GALA-E0008](/docs/errors/gala-e0008/) — the same rule for map literals
- [All GALA error codes](/docs/errors/)
