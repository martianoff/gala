---
layout: default
title: "GALA-E0086 — Type Argument a Method Needs Comparable"
description: "GALA-E0086 fires when a method that needs one of its type's parameters comparable, such as HashMap.ToGoMap, is called on a receiver whose type argument Go cannot compare, such as an Array key."
keywords: "gala-e0086, gala does not satisfy comparable, gala togomap array key, gala hashmap tomap, gala comparable type argument, gala method comparable"
permalink: /docs/errors/gala-e0086/
last_modified_at: 2026-10-28
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0086</p>

# GALA-E0086 — Type argument a method needs comparable

**When it fires.** A method of a generic type uses one of the type's
parameters where Go requires a comparable type, while the type declares the
parameter `any`. `HashMap.ToGoMap` builds a Go map keyed by `K`, and a
`HashMap`'s keys are compared with `Equal`, so `K` may be any hashable type.
GALA finds such a use on its own (a map key type, an operand of `==` or `!=`,
the argument of a `comparable` type parameter, or a call of another such
method) and compiles the method to a Go function whose copy of the parameter
is constrained `comparable`. The method keeps its GALA form.

A call of the method, or a method value of it, whose receiver has a type
argument for that parameter Go cannot compare is this error: an `Array`, a
function, a Go slice or map, a type parameter constrained by `any`, or a
struct, tuple or collection that holds one.

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val m = HashMapOf((ArrayOf(1), 2))
    Println(m.ToGoMap())
}
```

**Error output.**

```
error[GALA-E0086]: HashMap.ToGoMap needs a comparable K: Array[int] holds a Go slice
  --> main.gala:7:15
  |
7 |     Println(m.ToGoMap())
  |               ^^^^^^^ give K a type Go can compare with ==: a primitive, or a stru…
  |
  = hint: give K a type Go can compare with ==: a primitive, or a struct, tuple or Option of such types
```

When the type argument is a type parameter of the calling function, the
message reads `K is a type parameter constrained by any`, and the hint says
to declare it `comparable`.

**Fix.** Call the method on a receiver whose type argument Go can compare, or
use what the type offers for any element type. A `HashMap` with `Array` keys
is still a `HashMap`: iterate it with `ForEachKV`, or convert it with
`ToArray()` or `ToList()` instead of `ToGoMap()`:

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val m = HashMapOf((ArrayOf(1), 2))
    Println(m.ToArray())
}
```

In a generic function, declare the type parameter `comparable`:
`func toGo[K comparable, V any](m HashMap[K, V]) map[K]V = m.ToGoMap()`.

**Rationale.** Go cannot constrain a receiver's type parameter in a method,
so a method that needs it comparable is a function in Go, named
`Type_Method` (`HashMap_ToGoMap`). The requirement belongs to that one
method, not the type: every other method of a `HashMap` takes any hashable
key. Left to Go, the call failed against the function with `does not satisfy
comparable`, naming generated code.
