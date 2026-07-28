---
layout: default
title: "Golang String Interpolation — Template Strings and F-Strings for Go"
description: "GALA adds string interpolation to Go. Write s\"Hello $name\" and f\"${pi}%.2f\" instead of fmt.Sprintf — no import needed, with auto-inferred format verbs. The string formatting Go is missing."
keywords: "golang string interpolation, go string interpolation, go template string, golang sprintf alternative, go f string, golang string format, go string template literal, gala string interpolation"
permalink: /features/string-interpolation/
last_modified_at: 2026-04-19
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/">Features</a> / String Interpolation</p>

# String Interpolation — Template Strings for Go

GALA replaces `fmt.Sprintf` with string interpolation. Write `s"Hello $name"` instead of `fmt.Sprintf("Hello %s", name)`. Format verbs are inferred automatically from the expression type. No `import "fmt"` needed — the transpiler handles it.

Two prefixes are available:
- **`s"..."`** — auto-inferred format verbs based on the expression type
- **`f"..."`** — explicit format specifiers for precise control (padding, precision, hex)

```gala
val name = "Alice"
val age = 30
Println(s"$name is $age years old")  // Alice is 30 years old
Println(f"$age%04d")                 // 0030
```

---

## s-strings: Auto-Inferred Format Verbs

Use `$variable` to embed a variable and `${expression}` to embed any expression. The compiler picks the correct format verb from the expression's type:

```gala
val name = "Alice"
val age = 30
val pi = 3.14159

Println(s"Hello $name!")                    // Hello Alice!
Println(s"$name is $age years old")         // Alice is 30 years old
Println(s"Pi ≈ $pi")                        // Pi ≈ 3.14159
Println(s"Next year: ${age + 1}")           // Next year: 31
Println(s"Price: $$99")                     // Price: $99 (use $$ for literal $)
```

### Auto-Inferred Format Verb Table

The compiler selects the format verb based on the expression type:

| Type | Format Verb | Example |
|------|-------------|---------|
| `string` | `%s` | `s"Hello $name"` |
| `int`, `int64`, etc. | `%d` | `s"Count: $n"` |
| `float64`, `float32` | `%g` | `s"Pi: $pi"` |
| `bool` | `%t` | `s"Active: $flag"` |
| `rune` | `%c` | `s"Char: $ch"` |
| everything else | `%v` | `s"Data: $obj"` |

---

## Expression Interpolation with `${...}`

Any expression can be embedded inside `${...}`, including function calls, method chains, arithmetic, and even expressions containing string literals:

```gala
val x = 10
val y = 20

Println(s"Sum: ${x + y}")                          // Sum: 30
Println(s"Upper: ${strings.ToUpper("hello")}")      // Upper: HELLO
```

### Nested Strings Inside `${...}`

The lexer correctly handles string literals inside interpolation expressions — you can pass string arguments to functions without escaping:

```gala
import "strings"
import . "martianoff/gala/collection_immutable"

// Function call with string argument
Println(s"result: ${strings.Repeat("ab", 3)}")         // result: ababab

// Multiple string arguments
Println(s"${strings.Replace("hello world", "world", "gala", 1)}")  // hello gala

// Collection methods with string separators
val nums = ArrayOf(1, 2, 3)
Println(s"nums: ${nums.MkString(", ")}")                // nums: 1, 2, 3
```

---

## f-strings: Explicit Format Specifiers

When you need precise formatting — padding, decimal places, hex output — use `f"..."` with a Go printf format specifier after the variable or expression:

```gala
val count = 7
val price = 19.99

Println(f"$count%04d items")                // 0007 items
Println(f"Total: $$$price%.2f")             // Total: $19.99
Println(f"Hex: $count%x")                   // Hex: 7
Println(f"${count * 100}%010d")             // 0000000700
```

If you omit the `%spec` after a variable in an `f"..."` string, the format verb is auto-inferred (same behavior as `s"..."`).

### Common Format Specifiers

| Specifier | Meaning | Example |
|-----------|---------|---------|
| `%d` | Decimal integer | `f"$n%d"` |
| `%04d` | Zero-padded to 4 digits | `f"$n%04d"` |
| `%x` | Hexadecimal | `f"$n%x"` |
| `%f` | Floating point | `f"$x%f"` |
| `%.2f` | 2 decimal places | `f"$price%.2f"` |
| `%010d` | Zero-padded to 10 digits | `f"$n%010d"` |
| `%s` | String | `f"$name%s"` |

---

## Println and Print Without Imports

`Println` and `Print` are available globally in GALA — no `import "fmt"` required. They map directly to `fmt.Println` and `fmt.Print` in the generated Go code:

```gala
// No import needed
Println("Hello, World!")
Println(s"Result: $x")
Print("no newline")
```

You only need to `import "fmt"` for specialized functions like `fmt.Errorf`, `fmt.Fprintf`, or `fmt.Stringer`.

---

## Comparison: GALA Interpolation vs Go fmt.Sprintf

<div class="comparison">
<div>
<p>GALA</p>

```gala
val name = "Alice"
val age = 30
val balance = 1234.5

Println(s"Hello $name!")
Println(s"$name is $age years old")
Println(f"Balance: $$$balance%.2f")
Println(s"Next year: ${age + 1}")
```

</div>
<div>
<p>Go</p>

```go
name := "Alice"
age := 30
balance := 1234.5

fmt.Printf("Hello %s!\n", name)
fmt.Printf("%s is %d years old\n", name, age)
fmt.Printf("Balance: $%.2f\n", balance)
fmt.Printf("Next year: %d\n", age+1)
```

</div>
</div>

Key differences:
- **No import** — GALA auto-imports `fmt` when interpolation is used
- **No format verb matching** — `s"..."` picks the right verb from the type, so you cannot accidentally use `%d` for a string
- **Expressions inline** — `${age + 1}` embeds the computation directly in the string, instead of passing it as a trailing argument
- **Literal dollar sign** — use `$$` in GALA vs `$` in Go (since `$` starts an interpolation)

---

## When to Use s-strings vs f-strings

| Need | Use |
|------|-----|
| Simple variable embedding | `s"Hello $name"` |
| Expression embedding | `s"Total: ${x + y}"` |
| Specific decimal places | `f"$price%.2f"` |
| Zero padding | `f"$id%06d"` |
| Hex / octal output | `f"$n%x"` |
| No format control needed | `s"..."` (simpler, auto-inferred) |

Use `s"..."` by default. Switch to `f"..."` only when you need explicit format control.

---

## Further Reading

- [Getting Started with GALA]({{ '/' | relative_url }}) — overview of the language
- [Functional Collections]({{ '/features/collections/' | relative_url }}) — use `MkString` inside interpolation for formatted output
- [Type Inference]({{ '/features/type-inference/' | relative_url }}) — how format verbs are chosen from inferred types
