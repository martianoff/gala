---
layout: default
title: "Json in GALA — Zero-Reflection JSON Codec with Builder Pattern"
description: "GALA's json package provides zero-reflection, compile-time JSON serialization with builder pattern configuration, naming strategies, and pattern matching support."
keywords: "gala json, golang json alternative, go type safe json, gala json codec, gala json pattern matching, go json serialization, zero reflection json"
permalink: /docs/json/
last_modified_at: 2026-10-01
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Json</p>

# Json — Zero-Reflection JSON Codec

GALA's `json` package provides compile-time JSON serialization powered by `StructMeta[T]`. No reflection, no struct tags, fully typed. All operations return `Try[T]` — no unchecked errors. Combined with builder pattern configuration and pattern matching, you get a clean, composable JSON pipeline.

```gala
import . "martianoff/gala/json"
```

---

## Quick Start

```gala
struct Person(FirstName string, LastName string, Age int)

val codec = Codec[Person](SnakeCase())

val person = Person("Alice", "Smith", 30)
val jsonStr = codec.Encode(person).Get()
// => {"first_name":"Alice","last_name":"Smith","age":30}

val decoded = codec.Decode(jsonStr)
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

`.OmitEmpty(field)` leaves the field out of the encoded output whenever its value is empty: `""` for a string kind, `0` for a numeric kind (`rune` included, and a float `-0.0`, which decodes back as `0`), `false` for `bool`, `None` for an `Option`, and no elements for an `Array`, `List` or `HashMap`. A nested struct is never empty: as in Go's `encoding/json`, an all-zero struct is still written. The test runs per value at encode time, so the elements of a root array each keep or drop the field on their own. `Omit` wins over `OmitEmpty`. Decoding a document without the field gives the field that empty value, so an omitted field round-trips. Like `Omit` and `Rename`, `OmitEmpty` names a field of `T` itself; the fields of a nested struct are always written.

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

// Compact JSON
val jsonStr = codec.Encode(person).Get()
// => {"first_name":"Alice","last_name":"Smith","age":30}

// Pretty-printed JSON
val pretty = codec.EncodePretty(person).Get()
// => {
//   "first_name": "Alice",
//   "last_name": "Smith",
//   "age": 30
// }
```

---

## Deserialization

<!-- doc-check: fragment -->
```gala
val decoded = codec.Decode(jsonStr)
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
val result = jsonStr match {
    case codec(p) => s"Found: ${p.FirstName}, age ${p.Age}"
    case _ => "invalid JSON"
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

val jsonStr = codec.Encode(user).Get()
// => {"name":"alice","tags":[{"key":"urgent","color":"red"},{"key":"draft","color":"yellow"}]}

val decoded = codec.Decode(jsonStr).Get()
Println(s"first tag: ${decoded.Tags.Get(0).Key}/${decoded.Tags.Get(0).Color}")
```

The same applies to `HashMap[string, Tag]`, `List[Tag]`, and `Array[Array[Tag]]`. No additional builder calls or type annotations are required — declare the struct shape, ask for `Codec[T](naming)`, and the codec handles the rest.

### Field Types

A field can be any scalar kind — `string`, `bool`, `rune`, `int`, `int8`…`int64`, `uint`, `uint8`…`uint64`, `uintptr`, `byte`, `float32`, `float64` — an alias or Go named type over one (`type Millis int64`, `time.Duration`), a struct, an alias of any of these, or an `Option`, `Array`, `List` or `HashMap[K, V]` of any of these (`K` a string or an alias of `string`), nested to any depth. A field of any other type — a function, a pointer, a Go slice or map, a sealed type, a generic struct, a struct with no fields, `Option[Option[T]]` — is a compile error, [GALA-E0050](/docs/errors/gala-e0050/); a field is never silently written as `null`.

Decoding checks ranges: `300` into an `int8`, `-1` into a `uint`, or `1e39` into a `float32` makes `Decode` return a `Failure`. JSON cannot represent NaN or ±Infinity, so encoding one makes `Encode` return a `Failure`.

---

## Root Arrays and Other Root Values

`Codec[T]` reads and writes a document whose root is a `T` object. Plenty of payloads are not shaped like that — a `GET /users` returns `[{"id":1},{"id":2}]`, a counter endpoint returns `42`. Two entry points cover them.

**A root array of structs.** Configure the element codec as usual, then call `.Array()` (or `.List()`). `Naming`, `Rename` and `Omit` apply to every element:

```gala
import (
    . "martianoff/gala/collection_immutable"
    . "martianoff/gala/json"
)

struct User(UserId int, FullName string, Password string)

func main() {
    val users = Codec[User](SnakeCase()).Omit("Password").Rename("UserId", "id").Array()

    val body = users.Encode(ArrayOf(User(1, "Ann", "x"), User(2, "Bo", "y"))).Get()
    Println(body)
    // => [{"id":1,"full_name":"Ann"},{"id":2,"full_name":"Bo"}]

    users.Decode("[{\"id\":3,\"full_name\":\"Cy\"}]").ForEach((us) => Println(us.Get(0).FullName))
    // => Cy
}
```

**Any other root value.** `Value[T]()` takes any shape a codec field can have — a scalar, an alias or Go named type over one, an `Option` (`None` is `null`), an `Array` / `List`, a `HashMap[string, _]`, a struct — nested to any depth:

```gala
import (
    . "martianoff/gala/collection_immutable"
    . "martianoff/gala/json"
)

func main() {
    Println(Value[Array[int]]().Encode(ArrayOf(1, 2, 3)))   // Success([1,2,3])
    Println(Value[Array[string]]().Decode("[\"a\",\"b\"]"))  // Success(Array(a, b))
    Println(Value[float64]().Decode("1.5"))                 // Success(1.5)
    Println(Value[Option[int]]().Decode("null"))            // Success(None())
    Println(Value[uint8]().Decode("256"))                   // Failure: out of range for uint8
}
```

`Value[T]()` has a `.Naming(n)` builder for structs inside the value; it has no `Rename` / `Omit` — for those, use `Codec[T](naming).Array()`.

Decoding checks the root shape: an object where an array is expected, a string where a number is expected, or anything after the document (`[1]]`, `1 2`) is a `Failure`.

---

## Unknown Fields

The decoder silently drops any field in the input that is not declared on the target struct. This is the default and only behaviour today — there is no strict mode or unknown-field error.

```gala
struct Point(X int, Y int)
val codec = Codec[Point](SnakeCase())

// "z" is not declared on Point — it is skipped on decode.
val raw = "{\"x\":1,\"y\":2,\"z\":99}"
val decoded = codec.Decode(raw).Get()
Println(s"x=${decoded.X} y=${decoded.Y}")
// => x=1 y=2
```

Skipping handles all JSON value shapes — strings, numbers, booleans, `null`, and nested objects/arrays — so an unknown field can carry an arbitrarily complex payload without breaking the decode.

---

## Qualified Import

For projects that prefer explicit package prefixes:

<!-- doc-check: fragment -->
```gala
import "martianoff/gala/json"

val codec = json.Codec[Person](json.SnakeCase())
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

No reflection at runtime. Field names, types, and access patterns are all resolved at compile time.

A struct declared in a library package carries its own metadata: that package emits an exported `StructMeta_X` for each struct `X` it declares, and any package encoding an `X` — including one with unexported fields, which only its own package can read — uses it. Structs of the `main` package get `_StructMeta_X` generated where the codec is requested.

`Value[T]()` works the same way with `ValueMeta[T]`, the intrinsic for a whole value of any codec shape: the transpiler generates `_ValueMeta_X` with typed `EncodeValue` / `DecodeValue` methods and injects it, so `Value[int]()` takes no arguments of its own. Metadata generated on demand — a `_ValueMeta_X`, or a `main` package `_StructMeta_X` — gets a file-specific name, so two files of one package can use the same codec.

---

## API Reference

| Method | Signature | Description |
|--------|-----------|-------------|
| `Codec[T](naming)` | `Naming → JsonEncoder[T]` | Create codec (StructMeta auto-injected) |
| `.Naming(n)` | `Naming → JsonEncoder[T]` | Set naming strategy |
| `.Omit(field)` | `string → JsonEncoder[T]` | Exclude field from serialization |
| `.Rename(field, key)` | `string, string → JsonEncoder[T]` | Map field to custom JSON key |
| `.OmitEmpty(field)` | `string → JsonEncoder[T]` | Skip field when its value is empty (`""`, `0`, `false`, `None`, no elements) |
| `.Encode(v)` | `T → Try[string]` | Serialize to compact JSON |
| `.EncodePretty(v)` | `T → Try[string]` | Serialize to pretty-printed JSON |
| `.Decode(s)` | `string → Try[T]` | Deserialize from JSON string |
| `.Unapply(s)` | `string → Option[T]` | Pattern matching extractor |
| `.Array()` | `→ JsonArrayEncoder[T]` | Codec for a root array of `T`: `Encode(Array[T])`, `EncodePretty`, `Decode → Try[Array[T]]`, `Unapply` |
| `.List()` | `→ JsonListEncoder[T]` | The same over `List[T]` |
| `Value[T]()` | `→ JsonValueEncoder[T]` | Codec for a root value of any codec shape (ValueMeta auto-injected): `.Naming`, `Encode`, `EncodePretty`, `Decode`, `Unapply` |

---

## Further Reading

- [Yaml](/docs/yaml/) — same builder API, block-style YAML output
- [Error Handling](/features/error-handling/) — Option, Either, and Try monads
- [Pattern Matching](/features/pattern-matching/) — extractors, guards, and exhaustive matching
- [Regex](/docs/regex/) — regular expressions with pattern matching extractors
