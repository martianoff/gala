---
layout: default
title: "GALA-E0099 — Pointer Used Where the Value Is Expected"
description: "GALA-E0099 fires when a Ptr[T], such as a mutable collection from ArrayOf, fills a slot of type T. Declare the slot as the Ptr, or dereference the pointer."
keywords: "gala-e0099, gala ptr, gala pointer, gala dereference, gala mutable collection, gala arrayof"
permalink: /docs/errors/gala-e0099/
last_modified_at: 2026-11-11
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0099</p>

# GALA-E0099 — Pointer used where the value is expected

**When it fires.** A pointer to a type fills a slot of that type itself: an
argument, a `val` or `var` declaration or assignment, a return value or a
constructor field. The common case is a mutable collection: its constructors
(`ArrayOf`, `EmptyArray`, `ListOf`, ...) return a `Ptr`, so
`collection_mutable.ArrayOf(1, 2)` is a `Ptr[Array[int]]`, not an
`Array[int]`.

**Minimal repro.**

```gala
package main

import "martianoff/gala/collection_mutable"

func total(xs collection_mutable.Array[int]) int = xs.Length()

func main() {
    Println(total(collection_mutable.ArrayOf(1, 2)))
}
```

**Error output.**

```
error[GALA-E0099]: cannot use collection_mutable.ArrayOf(1, 2) (Ptr[collection_mutable.Array[int]]) as collection_mutable.Array[int]: it is a pointer to the value, not the value
  --> main.gala:8:19
  |
8 |     Println(total(collection_mutable.ArrayOf(1, 2)))
  |                   ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ declare the parameter, field or variable as Ptr[collection_m…
  |
  = hint: declare the parameter, field or variable as Ptr[collection_mutable.Array[int]], as mutable collections are passed, or dereference the pointer: `*collection_mutable.ArrayOf(1, 2)`
```

**Fix.** Mutable collections are passed and held through a `Ptr`, so that every
holder sees the same collection. Declare the parameter, field or variable as
the `Ptr`:

```gala
package main

import "martianoff/gala/collection_mutable"

func total(xs Ptr[collection_mutable.Array[int]]) int = xs.Length()

func main() {
    Println(total(collection_mutable.ArrayOf(1, 2)))
}
```

For a type that is meant to be used by value, dereference the pointer instead:
`show(*p)` passes the value `p` points to.

**Rationale.** GALA, like Go, never dereferences a pointer implicitly. The
program used to pass GALA's checks and fail in `go build`, which reported it
against the generated Go: `cannot use collection_mutable.ArrayOf(1, 2) (value
of type *collection_mutable.Array[int]) as collection_mutable.Array[int]
value`.

---

## Related

- [Mutable collections](/docs/mutable-collections/)
- [Pointers](/docs/language-reference/#non-null-pointers-ptrt-and-optionptrt)
- [All GALA error codes](/docs/errors/)
