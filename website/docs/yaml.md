---
layout: default
title: "Yaml in GALA — Zero-Reflection YAML Codec with Builder Pattern"
description: "GALA's yaml package provides zero-reflection, compile-time YAML serialization with builder pattern configuration, naming strategies, and pattern matching support."
keywords: "gala yaml, golang yaml alternative, go type safe yaml, gala yaml codec, gala yaml pattern matching, go yaml serialization, zero reflection yaml"
permalink: /docs/yaml/
last_modified_at: 2026-10-03
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Yaml</p>

# Yaml — Zero-Reflection YAML Codec

GALA's `yaml` package provides compile-time YAML serialization powered by `StructMeta[T]`. No reflection, no struct tags, fully typed. All operations return `Try[T]` — no unchecked errors. The API mirrors `json.Codec[T]` exactly; the only difference is the emitted format: block-style YAML.

```gala
import . "martianoff/gala/yaml"
```

---

## Quick Start

```gala
struct Person(FirstName string, LastName string, Age int)

val codec = Codec[Person](SnakeCase())

val person = Person("Alice", "Smith", 30)
val yamlStr = codec.Encode(person).Get()
// =>
// first_name: Alice
// last_name: Smith
// age: 30

val decoded = codec.Decode(yamlStr)
// decoded: Try[Person] — fully typed!
```

---

## Codec Builder Pattern

Create a codec with `Codec[T](naming)` and configure it with fluent builder methods:

<!-- doc-check: fragment -->
```gala
val codec = Codec[Person](SnakeCase())
    .Omit("Password")
    .Rename("Email", "email_address")
    .OmitEmpty("Bio")
```

Each builder method returns a new immutable codec instance — safe to share across goroutines.

`.OmitEmpty(field)` leaves the field out of the encoded output whenever its value is empty: `""` for a string kind, `0` for a numeric kind (`rune` included, and a float `-0.0`, which decodes back as `0`), `false` for `bool`, `None` for an `Option`, and no elements for an `Array`, `List` or `HashMap`. A nested struct is never empty: an all-zero struct is still written. The test runs per value at encode time, so the elements of a root sequence each keep or drop the field on their own. `Omit` wins over `OmitEmpty`. Decoding a document without the field gives the field that empty value, so an omitted field round-trips. Like `Omit` and `Rename`, `OmitEmpty` names a field of `T` itself; the fields of a nested struct are always written.

### Naming Strategies

| Strategy | Input | Output |
|----------|-------|--------|
| `AsIs()` | `FirstName` | `FirstName` |
| `CamelCase()` | `FirstName` | `firstName` |
| `SnakeCase()` | `FirstName` | `first_name` |
| `KebabCase()` | `FirstName` | `first-name` |

---

## Serialization

<!-- doc-check: fragment -->
```gala
val person = Person("Alice", "Smith", 30)
val yamlStr = codec.Encode(person).Get()
// =>
// first_name: Alice
// last_name: Smith
// age: 30
```

Block-style YAML is already human-readable, so there is no separate pretty-print method.

---

## Deserialization

<!-- doc-check: fragment -->
```gala
val decoded = codec.Decode(yamlStr)
// decoded: Try[Person]

// Safe access via Map
val name = decoded.Map((p) => p.FirstName).GetOrElse("unknown")

// Side effect on success
decoded.ForEach((p) => {
    Println(s"Decoded: ${p.FirstName}, age ${p.Age}")
})
```

---

## Pattern Matching

Codec instances work as pattern matching extractors via `Unapply`. If decoding fails, the case does not match — no exception, no panic:

<!-- doc-check: fragment -->
```gala
val result = yamlStr match {
    case codec(p) => s"Found: ${p.FirstName}, age ${p.Age}"
    case _ => "invalid YAML"
}
```

This is especially useful when handling input from external sources:

<!-- doc-check: fragment -->
```gala
val commandCodec = Codec[Command](SnakeCase())
val eventCodec = Codec[Event](SnakeCase())

func handleMessage(raw string) string = raw match {
    case commandCodec(cmd) => processCommand(cmd)
    case eventCodec(evt)   => processEvent(evt)
    case _                 => "unknown message format"
}
```

---

## Nested Structures and Collections

`Codec[T]` handles arbitrarily nested struct shapes — including struct fields that are themselves `Array[Struct]`, `List[Struct]`, `HashMap[string, Struct]`, or any combination of those (e.g. `Array[Array[Struct]]`). The user only writes `Codec[Top](naming)` — the transpiler discovers every reachable struct transitively, including across packages, and generates fully typed encode/decode dispatch for each.

Naming strategy propagates into nested types automatically: a `SnakeCase()` codec on the outermost type renames every nested struct's fields the same way.

```gala
struct Tag(Key string, Color string)
struct User(Name string, Tags Array[Tag])

val codec = Codec[User](SnakeCase())

val tags = EmptyArray[Tag]().Append(Tag("urgent", "red")).Append(Tag("draft", "yellow"))
val user = User("alice", tags)

val yamlStr = codec.Encode(user).Get()
// =>
// name: alice
// tags:
//   - key: urgent
//     color: red
//   - key: draft
//     color: yellow

val decoded = codec.Decode(yamlStr).Get()
Println(s"first tag: ${decoded.Tags.Get(0).Key}/${decoded.Tags.Get(0).Color}")
```

The same applies to `HashMap[string, Tag]`, `List[Tag]`, and `Array[Array[Tag]]`. No additional builder calls or type annotations are required — declare the struct shape, ask for `Codec[T](naming)`, and the codec handles the rest.

---

## Root Sequences and Other Root Values

As in the [Json](/docs/json/) codec, `.Array()` / `.List()` on a configured codec read and write a document whose root is a sequence of structs, with `Naming`, `Rename` and `Omit` applied to every element, and `Value[T]()` reads and writes a root of any shape a codec field can have — a scalar, an `Option` (`None` is `null`), an `Array` / `List`, a `HashMap[string, _]`, a struct:

```gala
import (
    . "martianoff/gala/collection_immutable"
    . "martianoff/gala/yaml"
)

struct Tag(Key string, TagColor string)

func main() {
    val tags = Codec[Tag](SnakeCase()).Array()
    Println(tags.Encode(ArrayOf(Tag("a", "red"), Tag("b", "blue"))).Get())
    // - key: a
    //   tag_color: red
    // - key: b
    //   tag_color: blue

    Println(Value[Array[int]]().Decode("- 1\n- 2"))   // Success(Array(1, 2))
    Println(Value[int]().Encode(42))                  // Success(42)
    Println(Value[string]().Encode("true"))           // Success("true") — quoted, so it reads back as a string
}
```

A root scalar is written on its own line, and an empty root sequence as `[]`. Decoding a mapping where a sequence is expected, or a sequence where a scalar is expected, is a `Failure`; sized integers are range-checked.

---

## Structs With Private Fields

As with the JSON codec, a struct with an unexported field is decodable only when it declares `func (x T) Validate() Try[T]`, and the decoded value is what `Validate` returns — a `Failure` carries your error:

```gala
package main

import (
    . "martianoff/gala/yaml"
    "errors"
)

struct Port(n int)

func (p Port) Validate() Try[Port] =
    if (p.n > 0 && p.n < 65536) Success(p) else Failure(errors.New(s"port out of range: ${p.n}"))

func main() {
    val codec = Codec[Port](AsIs())
    Println(codec.Decode("n: 8080").IsSuccess())   // true
    Println(codec.Decode("n: 0"))                  // Failure(port out of range: 0)
}
```

Without `Validate`, any codec that reaches such a struct is [GALA-E0050](/docs/errors/gala-e0050/); a `Validate` with another signature is [GALA-E0057](/docs/errors/gala-e0057/). See [Structs With Private Fields](/docs/json/#structs-with-private-fields) in the JSON docs for the full rule.

---

## Unknown Fields

The decoder silently drops any field in the input that is not declared on the target struct. This is the default and only behaviour today — there is no strict mode or unknown-field error.

```gala
struct Point(X int, Y int)
val codec = Codec[Point](SnakeCase())

// "z" is not declared on Point — it is skipped on decode.
val raw = "x: 1\ny: 2\nz: 99\n"
val decoded = codec.Decode(raw).Get()
Println(s"x=${decoded.X} y=${decoded.Y}")
// => x=1 y=2
```

Skipping handles all YAML scalar and nested-mapping/sequence shapes, so an unknown field can carry an arbitrarily complex payload without breaking the decode.

---

## Qualified Import

For projects that prefer explicit package prefixes:

<!-- doc-check: fragment -->
```gala
import "martianoff/gala/yaml"

val codec = yaml.Codec[Person](yaml.SnakeCase())
codec.Encode(person)
```

---

## How It Works

`Codec[T]` is powered by `StructMeta[T]` — a compiler intrinsic that generates type-safe field access at compile time. When you write:

<!-- doc-check: fragment -->
```gala
val codec = Codec[Person](SnakeCase())
```

The transpiler:
1. Detects that `Codec[T].Apply` expects `StructMeta[T]` as first parameter
2. Auto-generates `_StructMeta_Person` with typed `EncodeFields` / `DecodeFields` / `FieldIsEmpty` methods
3. Injects it as the first argument: `Codec[Person]{}.Apply(_StructMeta_Person{}, SnakeCase())`

No reflection at runtime. Field names, types, and access patterns are all resolved at compile time. The same `StructMeta[T]` machinery powers both the JSON and YAML codecs — only the underlying `FieldEncoder` / `FieldDecoder` implementation differs.

A struct declared in a library package carries its own metadata: that package emits an exported `StructMeta_X` for each struct `X` it declares, and any package encoding an `X` — including one with unexported fields, which only its own package can read — uses it. Structs of the `main` package get `_StructMeta_X` generated where the codec is requested. The generated types are not part of GALA's surface: naming one in GALA code is [GALA-E0058](/docs/errors/gala-e0058/).

### Supported YAML Subset

The codec emits and parses a focused, predictable subset of YAML:

- block-style mappings
- block-style sequences
- scalars (`string`, `int`, `float`, `bool`, `null`), including `.nan`, `.inf` and `-.inf`
- literal block scalars (`|`)
- comments
- nested structures
- the flow-style empty containers `[]` and `{}`, which the encoder writes for an empty `Array`, `List` or `HashMap` so it reads back as empty rather than `null`
- a document that is a single scalar (`42`, `abc`, `null`) or an empty `[]` / `{}`

Struct fields follow the same rules as the JSON codec: every int, uint and float kind, aliases and Go named types over them, structs, and `Option` / `Array` / `List` / `HashMap[string, V]` of those; any other field type is a compile error ([GALA-E0050](/docs/errors/gala-e0050/)). Out-of-range numbers are decode errors.

An alias field and an [opaque type](/docs/language-reference/#opaque-types) field are written as their underlying value, while a single-field struct is a nested mapping. With `type UserID int64` and `struct AccountID(Value int64)`, a struct with one field of each encodes with `SnakeCase()` as:

```yaml
user_id: 42
account_id:
  value: 7
```

Switching a field from one form to the other changes the document shape: `user_id: 42` read into the struct form is `Failure(yaml: expected mapping, got scalar)`. See [Alias or single-field struct](/docs/language-reference/#alias-or-single-field-struct).

Out of scope: YAML anchors and aliases (`&a`, `*a`), other flow-style collections, custom tags. If your input requires these, preprocess it through a richer YAML library before handing it to `Codec[T]`.

---

## API Reference

| Method | Signature | Description |
|--------|-----------|-------------|
| `Codec[T](naming)` | `Naming → YamlEncoder[T]` | Create codec (StructMeta auto-injected) |
| `.Naming(n)` | `Naming → YamlEncoder[T]` | Set naming strategy |
| `.Omit(field)` | `string → YamlEncoder[T]` | Exclude field from serialization |
| `.Rename(field, key)` | `string, string → YamlEncoder[T]` | Map field to custom YAML key |
| `.OmitEmpty(field)` | `string → YamlEncoder[T]` | Skip field when its value is empty (`""`, `0`, `false`, `None`, no elements) |
| `.Encode(v)` | `T → Try[string]` | Serialize to block-style YAML |
| `.Decode(s)` | `string → Try[T]` | Deserialize from YAML string |
| `.Unapply(s)` | `string → Option[T]` | Pattern matching extractor |
| `.Array()` | `→ YamlArrayEncoder[T]` | Codec for a root sequence of `T`: `Encode(Array[T])`, `Decode → Try[Array[T]]`, `Unapply` |
| `.List()` | `→ YamlListEncoder[T]` | The same over `List[T]` |
| `Value[T]()` | `→ YamlValueEncoder[T]` | Codec for a root value of any codec shape (ValueMeta auto-injected): `.Naming`, `Encode`, `Decode`, `Unapply` |

---

## Further Reading

- [Json](/docs/json/) — same builder API, compact or pretty-printed JSON output
- [Error Handling](/features/error-handling/) — Option, Either, and Try monads
- [Pattern Matching](/features/pattern-matching/) — extractors, guards, and exhaustive matching
- [Regex](/docs/regex/) — regular expressions with pattern matching extractors
