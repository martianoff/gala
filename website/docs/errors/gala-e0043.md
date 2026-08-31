---
layout: default
title: "GALA-E0043 — Type Name Called as a Constructor"
description: "\"Array is a type, not a constructor\" — GALA-E0043 fires on Scala-style `Array(1, 2, 3)` and names the constructor function to use instead. See the real compiler output and why the old failure was worse than a bad message."
keywords: "gala-e0043, gala constructor, gala arrayof, gala listof, gala hashmapof, gala type not callable, gala collection construction, scala list vs gala"
permalink: /docs/errors/gala-e0043/
last_modified_at: 2026-08-30
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0043</p>

# GALA-E0043 — type name called as a constructor

**When it fires.** A type name was called as though it were a function:

```gala
val xs = Array(1, 2, 3)
```

GALA builds values through named constructor functions. The type name itself is
not callable.

| You wrote | Use |
|-----------|-----|
| `Array(1, 2, 3)` | `ArrayOf(1, 2, 3)` |
| `List(1, 2, 3)` | `ListOf(1, 2, 3)` |
| `HashMap(...)` | `HashMapOf(("a", 1), ("b", 2))` |
| `Array[int]()` | `EmptyArray[int]()` |

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = Array(1, 2, 3)
    Println(xs)
}
```

**Error output.**

```
error[GALA-E0043]: Array is a type, not a constructor
  --> main.gala:6:14
  |
6 |     val xs = Array(1, 2, 3)
  |              ^^^^^ use `ArrayOf(...)`
  |
  = hint: use `ArrayOf(...)`; GALA constructs values through named functions, so a type name is never callable
```

**Fix.** Call the constructor function:

```gala
val xs = ArrayOf(1, 2, 3)
```

**What still works.** This code fires only after every *constructive* reading of
the call has been tried and declined. All of these are unaffected:

```gala
struct Point(X int, Y int)
val p = Point(1, 2)                 // positional struct constructor

sealed type Shape { case Circle(R float64) }
val c = Circle(1.0)                 // sealed variant constructor

val f = Future(() => compute())     // companion Apply
```

A struct you declared yourself is still positionally constructible, including
from another package when its fields are exported.

**Rationale.** Scala spells collection construction `List(1, 2, 3)`, so reaching
for the type name is a common first attempt in GALA. What made it worth a
dedicated code is what used to happen instead of a clear error.

`Array` is a struct with four **private** fields — `root`, `length`, `depth`,
`prefix`. The positional struct constructor allows a caller to supply a *subset*
of a struct's fields, so `Array(1, 2, 3)` was claimed by that rule and compiled
to:

```go
Array{root: 1, length: 2, depth: 3}
```

It mapped three arguments onto the first three fields of a type the calling
package cannot name, assigning an `int` to `root`, which is a `*arrayNode[T]`.
Go then rejected the result in its own vocabulary — either as an undefined type
or as

```
cannot use generic type collection_immutable.Array[T any] without instantiation
```

naming Go generics and an "instantiation" step that have no GALA surface. The
rejection was correct and the line number was right; the sentence described the
generated code rather than the source, and it never mentioned `ArrayOf`.

The subset rule now additionally requires that the fields being set are
*nameable from the call site*: a type from another package whose fields are
private is no longer positionally constructible. Nothing that used to compile
stops compiling — Go rejected those literals anyway — but the rejection happens
in GALA now, and names the constructor to use.

**Scope.** This code covers a call whose callee resolves to a known GALA type
and to nothing callable. It does not cover:

- an identifier that resolves to nothing at all — [GALA-E0023](/docs/errors/gala-e0023/);
- a bare Go builtin such as `len` or `make` — [GALA-E0035](/docs/errors/gala-e0035/);
- a wrong-arity call to a function that does exist, which Go reports.
