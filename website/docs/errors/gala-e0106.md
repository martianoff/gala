---
layout: default
title: "GALA-E0106 — Type Named Like a Go Predeclared Type"
description: "GALA-E0106 fires when a type is declared with the name of a Go predeclared type such as error, string or int. Rename the type."
keywords: "gala-e0106, gala predeclared type name, gala type named error, gala type named string"
permalink: /docs/errors/gala-e0106/
last_modified_at: 2026-11-12
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0106</p>

# GALA-E0106 — Type named like a Go predeclared type

**When it fires.** A type is declared with the name of one of Go's predeclared
types: `bool`, `byte`, `rune`, `string`, `error`, `any`, `comparable`, `int`,
`int8`, `int16`, `int32`, `int64`, `uint`, `uint8`, `uint16`, `uint32`,
`uint64`, `uintptr`, `float32`, `float64`, `complex64` or `complex128`. The
rule covers every kind of type declaration: a shorthand or block struct, an
interface, an alias, an opaque type, a sealed type and each of its cases, and a
type parameter, at the top level or in a body.

**Minimal repro.**

```gala
package main

struct error(Code int, Msg string)

func lookup(code int) error = error(code, "not found")

func main() {
    Println(lookup(404).Msg)
}
```

**Error output.**

```
error[GALA-E0106]: type error is named like a Go predeclared type
  --> main.gala:3:8
  |
3 | struct error(Code int, Msg string)
  |        ^^^^^ rename the type
  |
  = hint: rename the type; GALA reads error as Go's own type wherever it is written
```

**Fix.** Rename the type:

```gala
package main

struct NotFound(Code int, Msg string)

func lookup(code int) NotFound = NotFound(code, "not found")

func main() {
    Println(lookup(404).Msg)
}
```

**Rationale.** GALA reads a predeclared type name as Go's type in every type
position, so a type declared with that name could not be written as a type. In the
repro, the result type `error` is Go's `error` interface, not the struct, and
the program used to fail on an unrelated-looking message (`error does not
implement error`). A value, a field or a function may still take such a name:
there the name means the binding, and in a type position Go's type.

---

## Related

- [Language reference: a type may not take the name of a Go predeclared type](/docs/language-reference/)
- [All GALA error codes](/docs/errors/)
