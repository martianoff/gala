---
layout: default
title: "GALA-E0050 — Codec Field Type Has No Encoding"
description: "GALA-E0050 rejects a json or yaml Codec for a struct with a field the codec cannot serialize — a function, pointer, Go slice or map, sealed type or non-string map key — instead of silently writing null."
keywords: "gala-e0050, gala json codec, gala yaml codec, gala codec unsupported field, gala structmeta, gala serialization"
permalink: /docs/errors/gala-e0050/
last_modified_at: 2026-10-01
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0050</p>

# GALA-E0050 — codec field type has no encoding

**When it fires.** A codec is requested — `json.Codec[T]`, `yaml.Codec[T]` or
`StructMeta[T]()` — for a struct that has a field (directly, or in a struct it
nests) whose type the codec cannot serialize, or for a `T` that is itself one of
the shapes below:

- a function, a pointer, or a Go slice or map (`[]T`, `map[K]V`)
- a sealed type
- a generic struct, a struct with no fields, or any type that is neither a scalar
  nor a GALA struct
- a `HashMap` whose key is not a string (or an alias of `string`)
- an `Option` inside an `Option` (also through `Immutable`)
- for `json.Codec[T]` / `yaml.Codec[T]`, a `T` that is not a struct at all — a scalar
  (`Codec[int]`) or a collection (`Codec[Array[User]]`)

**Minimal repro.**

```gala
package main

import "martianoff/gala/json"

struct Job(Name string, Run func() int)

func main() {
    val codec = json.Codec[Job](json.AsIs())
    Println(codec.Encode(Job("build", () => 1)))
}
```

**Error output.**

```
error[GALA-E0050]: cannot generate a codec for Job: field Job.Run has type func() int: functions have no serialized form
  --> main.gala:8:33
  |
8 |     val codec = json.Codec[Job](json.AsIs())
  |                                 ^^^^ a codec field can be a string, bool, rune, int/uint/float ki…
  |
  = hint: a codec field can be a string, bool, rune, int/uint/float kind, an alias or named type over one, a struct, or an Option, Array, List or HashMap[string, _] of those
```

**Fix.** Keep only data in the struct you serialize. Store what the function
would compute, or a value that names it:

```gala
struct Job(Name string, Command string)
```

For the other shapes: store the value instead of a pointer; use `Array`,
`List` or `HashMap[string, V]` instead of a Go slice or map; key a map by the
string form of its key; and flatten `Option[Option[T]]` into a sealed status
you encode as a string field. For a document whose root is not a struct, use
`Codec[User](naming).Array()` for an array of structs and `Value[T]()` for any
other shape (`Value[int]()`, `Value[Array[string]]()`).

**What the codec does support.** Every scalar kind — `string`, `bool`,
`rune`, `int`, `int8`…`int64`, `uint`, `uint8`…`uint64`, `uintptr`, `byte`,
`float32`, `float64` — plus aliases and Go named types over them (`type
Millis int64`, `time.Duration`); structs; and `Option`, `Array`, `List` and
`HashMap[string, V]` of any of these, nested to any depth.

On decode, an integer that does not fit the field's kind (`300` into an
`int8`, `-1` into a `uint`) and a float that overflows `float32` are decode
errors, not wrapped values. JSON has no spelling for NaN or ±Infinity, so
encoding one fails; YAML writes them as `.nan`, `.inf` and `-.inf` and reads
them back.

**Rationale.** A field the codec had no encoding for used to be written as
`null` and skipped on decode, so `Encode` succeeded, `Decode` succeeded, and the
value came back as zero. Data was lost with no signal. Rejecting the shape
where the codec is requested turns that into an error you see while building.
