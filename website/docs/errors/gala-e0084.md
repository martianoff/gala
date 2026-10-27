---
layout: default
title: "GALA-E0084 — Values That Cannot Be Compared with =="
description: "GALA-E0084 fires when == or != (or a stable-identifier pattern) compares values Go cannot compare: a function, a Go map, a type parameter constrained by any, or a struct, sealed type, tuple or Array holding one or a Go slice. Compare them with Equal."
keywords: "gala-e0084, gala cannot be compared, gala struct containing std.Immutable cannot be compared, gala incomparable types in type set, gala equal, gala == array, gala comparable"
permalink: /docs/errors/gala-e0084/
last_modified_at: 2026-10-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0084</p>

# GALA-E0084 — Values that cannot be compared with ==

**When it fires.** `==` and `!=` are Go's operators, and Go compares only
values of a comparable type. The comparison has an operand whose type is not
one:

- a function value;
- a Go map;
- a type parameter constrained by `any` (`func same[T any](a T, b T)`);
- a struct, sealed type or tuple with a field of such a type, a Go slice
  field, or a field of a type that holds one: `Array` holds a Go slice, so a
  struct with an `Array` field cannot be compared, and neither can an `Array`
  itself;
- a Go type that holds one, such as `bytes.Buffer`.

A [stable-identifier pattern](/docs/language-reference/#stable-identifiers-constants-in-patterns)
(`case Answer`) compares the matched value with `==` too, and fires the same
way.

A comparison with `nil` is not this error. A type GALA does not know is left
to the Go compiler, and so are two Go slices compared directly: Go reports
`slice can only be compared to nil`.

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

struct Post(title string, tags Array[string])

func main() {
    val a = Post("hello", ArrayOf("intro"))
    val b = Post("hello", ArrayOf("intro"))
    Println(a == b)
}
```

**Error output.**

```
error[GALA-E0084]: == cannot compare Post values: field tags has type Array[string], which holds a Go slice
  --> main.gala:10:13
   |
10 |     Println(a == b)
   |             ^^^^^^ compare with Equal: `a.Equal(b)`
   |
   = hint: compare with Equal: `a.Equal(b)`
```

The message names the field that keeps the type from being comparable, and
`!=` hints `!a.Equal(b)`. In a generic function, `a == b` on a `T any` reads
`T is a type parameter constrained by any`, and a stable identifier reports
`case Answer compares the matched value with Answer by ==, which cannot
compare ...`.

**Fix.** Compare the values with `Equal`. Every GALA struct, sealed type and
tuple has an `Equal` method that compares field by field, and `Array` compares
its elements:

```gala
package main

import . "martianoff/gala/collection_immutable"

struct Post(title string, tags Array[string])

func main() {
    val a = Post("hello", ArrayOf("intro"))
    val b = Post("hello", ArrayOf("intro"))
    Println(a.Equal(b))  // true
    Println(!a.Equal(b)) // false
}
```

In a generic function, constrain the type parameter with `comparable` when
`==` is what you mean (`func same[T comparable](a T, b T) bool = a == b`), or
compare with `Equal(a, b)`, which takes any type. A function value cannot be
compared at all: compare the values it returns. A stable identifier that holds
such a value is matched with a guard instead, `case v if v.Equal(Answer) =>`.

**Rationale.** Without this check, the comparison was emitted as written and
`go build` rejected it against the generated code — `struct containing
std.Immutable[collection_immutable.Array[string]] cannot be compared` — naming
the wrapper a `val` field is stored in rather than the field. `==` keeps Go's
meaning: a comparison Go compiles is unchanged.
