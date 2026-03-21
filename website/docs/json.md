---
layout: default
title: "Json in GALA — Type-Safe JSON Serialization and Pattern Matching"
description: "GALA's Json module provides type-safe JSON serialization and deserialization with Try-based error handling and pattern matching extractors. Safer than Go's encoding/json."
keywords: "gala json, golang json alternative, go type safe json, gala json parse, gala json pattern matching, go json serialization"
permalink: /docs/json/
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Json</p>

# Json — Type-Safe JSON with Try

GALA's `Json` module wraps Go's `encoding/json` with type safety. All operations return `Try[T]` — no unchecked errors, no forgotten `if err != nil`. Combined with pattern matching, you get a clean, composable JSON pipeline.

```gala
import . "martianoff/gala/std"
```

---

## Serialization

```gala
type Person struct {
    var Name string
    var Age  int
}

val person = Person{Name: "Alice", Age: 30}

// Compact JSON
val jsonStr = JsonStringify(person).Get()
// => {"Name":"Alice","Age":30}

// Pretty-printed JSON
val pretty = JsonStringifyPretty(person).Get()
// => {
//   "Name": "Alice",
//   "Age": 30
// }
```

---

## Deserialization

```gala
val parsed = JsonParse[Person](jsonStr)
// parsed: Try[Person]

// Safe access via Map
val name = parsed.Map((p) => p.Name).GetOrElse("unknown")

// Side effect on success
parsed.ForEach((p) => {
    Println(s"Parsed: ${p.Name}, age ${p.Age}")
})
```

Invalid JSON returns `Failure` instead of panicking:

```gala
val bad = JsonParse[Person]("invalid json")
Println(bad.IsFailure())  // true
```

---

## Pattern Matching with Json[T]

The `Json[T]` extractor parses JSON strings directly inside `match` expressions. If parsing fails, the case does not match — no exception, no panic:

```gala
val result = jsonStr match {
    case Json[Person](p) => s"Found: ${p.Name}, age ${p.Age}"
    case _ => "invalid JSON"
}
```

This is especially useful when handling input from external sources:

```gala
func handleMessage(raw string) string = raw match {
    case Json[Command](cmd) => processCommand(cmd)
    case Json[Event](evt)   => processEvent(evt)
    case _                  => "unknown message format"
}
```

The `Json[T]` extractor calls `Unapply` under the hood, returning `Some[T]` on success and `None` on failure — the same pattern used by [sealed types](/features/sealed-types/) and [regex extractors](/docs/regex/).

---

## Chaining with Try

Because `JsonParse` returns `Try[T]`, you can chain operations without intermediate error checks:

```gala
val greeting = JsonParse[Person](input)
    .Map((p) => p.Name)
    .Map((name) => s"Hello, $name!")
    .GetOrElse("Hello, stranger!")
```

Convert to other monadic types:

```gala
val opt = JsonParse[Person](input).ToOption()     // Option[Person]
val either = JsonParse[Person](input).ToEither()  // Either[error, Person]
```

---

## API Reference

| Function | Signature | Description |
|----------|-----------|-------------|
| <code>JsonParse[T](data)</code> | <code>string → Try[T]</code> | Deserialize JSON string to T |
| <code>JsonStringify[T](value)</code> | <code>T → Try[string]</code> | Serialize value to JSON string |
| <code>JsonStringifyPretty[T](value)</code> | <code>T → Try[string]</code> | Serialize to pretty-printed JSON |
| <code>Json[T].Unapply(s)</code> | <code>string → Option[T]</code> | Pattern matching extractor |

---

## Further Reading

- [Error Handling](/features/error-handling/) — Option, Either, and Try monads
- [Pattern Matching](/features/pattern-matching/) — extractors, guards, and exhaustive matching
- [Regex](/docs/regex/) — regular expressions with pattern matching extractors
