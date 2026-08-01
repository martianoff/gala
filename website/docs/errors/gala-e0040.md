---
layout: default
title: "GALA-E0040 — Go Slice or Map Type in an Expression"
description: "\"Go slice type []byte is not allowed in an expression\" — GALA-E0040 restricts Go slice and map types to type positions. See the full allowed-position table, the real compiler output, and the Array/HashMap fix."
keywords: "gala-e0040, gala go slice type, gala map type expression, gala type argument, gala array hashmap, gala go interop types, gala tobytes"
permalink: /docs/errors/gala-e0040/
last_modified_at: 2026-07-31
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0040</p>

# GALA-E0040 — Go slice or map type in an expression

**What it means.** A Go slice (`[]T`) or map (`map[K]V`) type was written somewhere GALA expects an **expression** rather than a type. The usual site is an explicit type argument on a call:

```gala
val m = collection_immutable.EmptyHashMap[string, []byte]()
```

The same code covers the other expression site where Go would allow the syntax and GALA does not — a conversion, `[]byte(s)`.

---

## The rule

A Go slice or map type may be written **only where a type is expected**:

| Position | Example | Allowed |
|----------|---------|---------|
| Function / method parameter | `func f(xs []int)` | yes |
| Function / method return type | `func f() map[string]int` | yes |
| Struct field | `type B struct { data []byte }` | yes |
| Interface method result | `type R interface { Read() []byte }` | yes |
| `val` / `var` annotation | `val b []byte = …` | yes |
| Lambda parameter annotation | `(xs []int) => …` | yes |
| Type alias | `type Bytes []byte` | yes |
| Inside a `func` type | `func([]byte) string` | yes |
| Type argument of a generic **type** in any position above | `m HashMap[string, []byte]` | yes |
| Explicit type argument on a **call** | `EmptyHashMap[string, []byte]()` | **no — GALA-E0040** |
| Conversion | `[]byte(s)` | **no — GALA-E0040** |
| Composite literal | `[]int{1, 2, 3}` | no — see [E0007](/docs/errors/gala-e0007/) / [E0008](/docs/errors/gala-e0008/) |

The distinction is **type context versus expression context**, not "signatures and struct fields". `HashMap[string, []byte]` is fine as a parameter type, a struct field type, a `val` annotation or a type alias, because all of those are type positions. The identical text after `EmptyHashMap` in an expression is not, because bracketed arguments following an expression are parsed as an expression list — and `[]byte` is not an expression.

---

## Minimal repro

```gala
package main

import "martianoff/gala/collection_immutable"

func main() {
    val m = collection_immutable.EmptyHashMap[string, []byte]()
    Println(m.Size())
}
```

## Error output

```
error[GALA-E0040]: Go slice type []byte is not allowed in an expression
  --> typearg.gala:6:55
  |
6 |     val m = collection_immutable.EmptyHashMap[string, []byte]()
  |                                                       ^^^^^^ use Array[byte], or string for text, instead
  |
  = hint: use Array[byte], or string for text, instead; Go slice and map types are an interop surface, allowed only where a type is expected: a function signature, a struct field, a val/var annotation, or a type alias
```

A map type reports the same code and names the `HashMap` spelling:

```
error[GALA-E0040]: Go map type map[string]int is not allowed in an expression
  --> maparg.gala:6:55
  |
6 |     val m = collection_immutable.EmptyHashMap[string, map[string]int]()
  |                                                       ^^^^^^^^^^^^^^ use HashMap[string, int] instead
```

---

## How to fix it

Name a GALA type. `[]T` becomes `Array[T]`, `map[K]V` becomes `HashMap[K, V]`:

```gala
val m = collection_immutable.EmptyHashMap[string, collection_immutable.Array[byte]]()
```

For `[]byte` specifically, ask what the bytes are. Text is `string` in GALA, and that is almost always the type wanted:

```gala
val m = collection_immutable.EmptyHashMap[string, string]()
```

If the value genuinely has to be a Go `[]byte` — because it crosses into a Go API — keep the Go type out of the expression and put it in a type position instead. A type alias is the shortest route:

```gala
type Bytes []byte

val m = collection_immutable.EmptyHashMap[string, Bytes]()
```

For a conversion, use the `go_interop` helpers rather than `[]byte(s)`:

```gala
import . "martianoff/gala/go_interop"

val b = ToBytes("hi")      // string -> []byte
val s = ToString(b)        // []byte -> string
```

---

## Why the restriction exists

Go slices and maps are an **interop surface** in GALA, not first-class types. They exist so a GALA program can name the shape a Go API demands; they are deliberately not something a GALA program builds with. That is why the literal forms are rejected outright ([E0007](/docs/errors/gala-e0007/), [E0008](/docs/errors/gala-e0008/)) and `make` is not part of the surface ([E0035](/docs/errors/gala-e0035/)).

Restricting them to type positions is what keeps that boundary narrow: a Go type can be *named* wherever a Go value has to be described, and nowhere else.

The code itself exists because the restriction used to be enforced without being explained. The parser, unable to read `[]byte` as an expression, fell back to its composite-literal alternative — whose type *does* admit `[]T` — and then demanded the brace that alternative requires, reporting ANTLR's raw recovery text:

```
error: missing '{' at '('
```

That named no code, no file, no line and no type, and it pointed at the token *after* the offending one — sending readers to hunt for an unbalanced brace instead of the Go type they had written.

---

## See also

- [Immutable Collections](/docs/immutable-collections/) — `Array`, `List`, `HashMap` and the rest of GALA's own collections
- [GALA-E0007](/docs/errors/gala-e0007/) — slice literal `[]T{…}` is not a GALA construct
- [GALA-E0008](/docs/errors/gala-e0008/) — map literal `map[K]V{…}` is not a GALA construct
- [GALA-E0035](/docs/errors/gala-e0035/) — bare Go builtins like `make` and `len`
