---
layout: default
title: "Json in GALA — Zero-Reflection JSON Codec with Builder Pattern"
description: "GALA's json package provides zero-reflection, compile-time JSON serialization with builder pattern configuration, naming strategies, and pattern matching support."
keywords: "gala json, golang json alternative, go type safe json, gala json codec, gala json pattern matching, go json serialization, zero reflection json"
permalink: /docs/json/
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

```gala
val codec = Codec[Person](SnakeCase())
    .Omit("Password")
    .Rename("Email", "email_address")
    .OmitEmpty("Bio")
```

Each builder method returns a new immutable codec instance — safe to share across goroutines.

### Naming Strategies

| Strategy | Input | Output |
|----------|-------|--------|
| `AsIs()` | `FirstName` | `FirstName` |
| `CamelCase()` | `FirstName` | `firstName` |
| `SnakeCase()` | `FirstName` | `first_name` |
| `KebabCase()` | `FirstName` | `first-name` |

---

## Serialization

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

```gala
val result = jsonStr match {
    case codec(p) => s"Found: ${p.FirstName}, age ${p.Age}"
    case _ => "invalid JSON"
}
```

This is especially useful when handling input from external sources:

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

## Qualified Import

For projects that prefer explicit package prefixes:

```gala
import "martianoff/gala/json"

val codec = json.Codec[Person](json.SnakeCase())
codec.Encode(person)
```

---

## How It Works

`Codec[T]` is powered by `StructMeta[T]` — a compiler intrinsic that generates type-safe field access at compile time. When you write:

```gala
val codec = Codec[Person](SnakeCase())
```

The transpiler:
1. Detects that `Codec[T].Apply` expects `StructMetaOps` as first parameter
2. Auto-generates `_StructMeta_Person` with typed `WriteTo`/`ReadFrom` methods
3. Injects it as the first argument: `Codec[Person]{}.Apply(_StructMeta_Person{}, SnakeCase())`

No reflection at runtime. Field names, types, and access patterns are all resolved at compile time.

---

## API Reference

| Method | Signature | Description |
|--------|-----------|-------------|
| `Codec[T](naming)` | `Naming → JsonEncoder[T]` | Create codec (StructMeta auto-injected) |
| `.Naming(n)` | `Naming → JsonEncoder[T]` | Set naming strategy |
| `.Omit(field)` | `string → JsonEncoder[T]` | Exclude field from serialization |
| `.Rename(field, key)` | `string, string → JsonEncoder[T]` | Map field to custom JSON key |
| `.OmitEmpty(field)` | `string → JsonEncoder[T]` | Skip field when value is zero |
| `.Encode(v)` | `T → Try[string]` | Serialize to compact JSON |
| `.EncodePretty(v)` | `T → Try[string]` | Serialize to pretty-printed JSON |
| `.Decode(s)` | `string → Try[T]` | Deserialize from JSON string |
| `.Unapply(s)` | `string → Option[T]` | Pattern matching extractor |

---

## Further Reading

- [Error Handling](/features/error-handling/) — Option, Either, and Try monads
- [Pattern Matching](/features/pattern-matching/) — extractors, guards, and exhaustive matching
- [Regex](/docs/regex/) — regular expressions with pattern matching extractors
