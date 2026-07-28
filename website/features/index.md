---
layout: default
title: "GALA Features — Sum Types, Pattern Matching, and Monads for Go"
description: "Every GALA language feature in one place: sealed sum types, exhaustive pattern matching, Option/Either/Try, bind do-notation, immutability by default, immutable collections, Futures, compile-time data-race safety, and full Go interop."
keywords: "gala features, golang sum types, golang pattern matching, golang option type, go immutable collections, golang do notation, go data race compile error, golang type inference, go interop language, scala on go features"
permalink: /features/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / Features</p>

# GALA Features

GALA is a statically typed, functional-first language that transpiles to Go. Every feature below is a *language* feature — checked by the compiler, compiled to plain Go, and usable inside an existing Go module without bindings, code generation, or a runtime.

The list is organised the way the language is: what you can express in the type system, how values and effects flow through a program, and what the toolchain does for you.

---

## Start Here

If you are evaluating GALA, three pages cover most of the story:

- **[Sealed Types](/features/sealed-types/)** — the sum types the [Go Developer Survey](https://go.dev/blog/survey2024-h1-results) ranks as Go's most-wanted missing feature.
- **[Pattern Matching](/features/pattern-matching/)** — destructuring plus compile-time exhaustiveness, so an unhandled variant is a build failure rather than a production panic.
- **[Error Handling](/features/error-handling/)** — `Option`, `Either`, and `Try` replacing `nil` and the `if err != nil` cascade.

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
}

func area(s Shape) float64 = s match {
    case Circle(r)       => 3.14159 * r * r
    case Rectangle(w, h) => w * h
}   // drop a case and the build fails
```

---

## Type System

| Feature | What it gives you |
|---------|-------------------|
| [Sealed Types](/features/sealed-types/) | Closed type hierarchies — algebraic data types with auto-generated constructors and extractors, so a value is exactly one of a known set of variants. |
| [Pattern Matching](/features/pattern-matching/) | `match` expressions with struct destructuring, guards, nested and sequence patterns, custom extractors, and compiler-enforced exhaustiveness. |
| [Type Inference](/features/type-inference/) | Lambda parameters, generic type arguments, and fold accumulators inferred from context — always resolving to a concrete Go type, never `any`. |
| [Immutability](/features/immutability/) | `val` bindings, immutable struct fields, generated `Copy()` with named arguments, structural `Equal`, and read-only `ConstPtr[T]`. |

## Expressions and Effects

| Feature | What it gives you |
|---------|-------------------|
| [Error Handling](/features/error-handling/) | `Option[T]`, `Either[A, B]`, and `Try[T]` with `Map`, `FlatMap`, `Recover`, and pattern matching — no `nil`, no naked `(T, error)` pairs. |
| [Monadic Binding](/features/monadic-binding/) | `bind` / `also` do-notation that flattens `FlatMap` chains, accumulates errors with `Validated`, and runs independent `Future` steps concurrently. |
| [String Interpolation](/features/string-interpolation/) | `s"Hello $name"` and `f"${value}%.2f"` literals with inferred format verbs — no `fmt` import, no `Sprintf` call. |
| [Collections](/features/collections/) | Immutable `List`, `Array`, `HashMap`, `HashSet`, `TreeSet`, and `TreeMap` with `Map`, `Filter`, `FoldLeft`, `Collect`, and `SortBy`. |

## Concurrency

| Feature | What it gives you |
|---------|-------------------|
| [Concurrency](/features/concurrency/) | `Future[T]` over goroutines — composable with `Map`, `FlatMap`, `Zip`, and `Recover`, plus cancellation, timeouts, and `Race`. |
| [Concurrency Safety](/features/concurrency-safety/) | Data races as a *compile* error: only deeply immutable values may cross a goroutine boundary, proved statically at `Sendable` boundaries. |

## Toolchain and Interop

| Feature | What it gives you |
|---------|-------------------|
| [Go Interop](/features/go-interop/) | Import any Go package and call it directly. Return types are read from the Go SDK, so there is nothing to declare or generate. |
| [Compiler DX](/features/compiler-dx/) | Framed diagnostics with a caret and a hint, runtime panics that point at `.gala` source positions, and guaranteed tail-call optimisation. |
| [IDE Support](/features/ide-support/) | IntelliJ/GoLand plugin and a `gala lsp` server for VS Code and Neovim — completion, inlay hints, go-to-definition, and live diagnostics. |

---

## How the Features Fit Together

The pieces are designed to compose rather than to be adopted one at a time.

A sealed type is only as useful as the matching that consumes it, so **[sealed types](/features/sealed-types/)** and **[pattern matching](/features/pattern-matching/)** share an exhaustiveness check. `Option`, `Either`, and `Try` are themselves sealed types, which is why **[error handling](/features/error-handling/)** works with the same `match` syntax and why **[`bind`](/features/monadic-binding/)** works over any of them without higher-kinded types.

Immutability runs underneath all of it: because `val` bindings and struct fields are **[immutable by default](/features/immutability/)**, the **[data-race check](/features/concurrency-safety/)** is silent in ordinary code and fires only where genuinely mutable state would escape to another goroutine.

And none of it is a walled garden — every feature compiles to ordinary Go and coexists with the **[Go ecosystem](/features/go-interop/)** in the same module.

---

## Next Steps

- [Getting Started](/getting-started/) — install GALA and build your first program.
- [Playground](/playground/) — run GALA in the browser, nothing to install.
- [GALA vs Go](/vs-go/) — the same programs side by side.
- [Documentation](/docs/) — the language reference and standard library.
- [Error Codes](/docs/errors/) — every `GALA-Exxxx` diagnostic, and how to clear it.
