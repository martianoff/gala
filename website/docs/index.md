---
layout: default
title: "GALA Documentation — Sum Types, Pattern Matching, Collections, and Concurrency for Go"
description: "Complete GALA documentation. Language reference, sum types (sealed types), pattern matching, Option/Either/Try, immutable collections, Futures, streams, and dependency management — all transpiling to Go."
keywords: "gala documentation, gala docs, golang sum types docs, golang pattern matching docs, gala language reference, gala api docs, golang option type documentation"
permalink: /docs/
last_modified_at: 2026-07-26
---

# GALA Documentation

Welcome to the GALA documentation hub. Here you will find everything you need to learn and use GALA effectively.

## Core Language

| Topic | Description |
|-------|-------------|
| [Language Reference]({{ '/docs/language-reference/' | relative_url }}) | Full syntax and semantics reference |
| [Sealed Types]({{ '/features/sealed-types/' | relative_url }}) | Define closed type hierarchies with exhaustive compile-time checking |
| [Pattern Matching]({{ '/features/pattern-matching/' | relative_url }}) | Destructure values, apply guards, and produce expression results |
| [Immutability]({{ '/features/immutability/' | relative_url }}) | `val` bindings, immutable struct fields, `Copy()`, and `ConstPtr[T]` |
| [Type Inference]({{ '/features/type-inference/' | relative_url }}) | What you can leave out — generics, lambda params, accumulators |
| [Error Handling]({{ '/features/error-handling/' | relative_url }}) | Composable `Option[T]`, `Either[A,B]`, and `Try[T]` monads |
| [Monadic Binding]({{ '/features/monadic-binding/' | relative_url }}) | `bind` / `also` do-notation, including accumulating `Validated` groups |
| [String Interpolation]({{ '/features/string-interpolation/' | relative_url }}) | `s"…"` and `f"…"` literals with embedded expressions and formatting |
| [Concurrency Safety]({{ '/features/concurrency-safety/' | relative_url }}) | Compile-time data-race safety — `Shareable`, `Sendable`, and GALA-E0037 |
| [Compiler DX]({{ '/features/compiler-dx/' | relative_url }}) | Framed diagnostics, GALA-source stack traces, guaranteed TCO, `use` |
| [GALA vs Go]({{ '/vs-go/' | relative_url }}) | Side-by-side comparison with idiomatic Go code |

## Standard Library

| Topic | Description |
|-------|-------------|
| [Collections]({{ '/features/collections/' | relative_url }}) | Immutable `List`, `Array`, `HashMap`, `HashSet`, `TreeSet`, `TreeMap` and their mutable variants |
| [Immutable Collections]({{ '/docs/immutable-collections/' | relative_url }}) | Full API reference for the persistent collection types |
| [Mutable Collections]({{ '/docs/mutable-collections/' | relative_url }}) | `collection_mutable` reference — in-place updates for hot paths |
| [Concurrency]({{ '/features/concurrency/' | relative_url }}) | `Future[T]`, `Promise[T]`, `ExecutionContext`, cancellation, timeouts, and `Race` |
| [Subprocess]({{ '/docs/subprocess/' | relative_url }}) | Spawn and drive external child processes with a goroutine-safe `Process` handle, including Future-returning async methods |
| [Streams]({{ '/docs/streams/' | relative_url }}) | Lazy, potentially infinite sequences with `Stream[T]` |
| [Strings]({{ '/docs/strings/' | relative_url }}) | `Str`, the string builder, and the free string functions |
| [Time Utilities]({{ '/docs/time-utils/' | relative_url }}) | `Duration`, `Sleep`, and time helpers |
| [Go Interop]({{ '/features/go-interop/' | relative_url }}) | Import and use any Go package, type, or function directly from GALA |
| [Json]({{ '/docs/json/' | relative_url }}) | Type-safe JSON serialization, deserialization, and pattern matching extractors |
| [Yaml]({{ '/docs/yaml/' | relative_url }}) | Type-safe YAML serialization, deserialization, and pattern matching extractors |
| [Regex]({{ '/docs/regex/' | relative_url }}) | Regular expressions with pattern matching and Array destructuring |
| [IO Effect]({{ '/docs/io/' | relative_url }}) | Lazy composable side effects with `IO[T]` |

`Validated[E, A]` — the applicative companion to `Either` that **accumulates** every error instead of short-circuiting, with `Zip2` through `Zip10` — is documented alongside the `also` groups that use it in [Monadic Binding]({{ '/features/monadic-binding/' | relative_url }}).

## Guides

| Topic | Description |
|-------|-------------|
| [Getting Started]({{ '/getting-started/' | relative_url }}) | Installation, project setup, and writing your first GALA program |
| [Why GALA?]({{ '/docs/why-gala/' | relative_url }}) | Feature-by-feature assessment, ideal use cases, and honest trade-offs |
| [Examples]({{ '/docs/examples/' | relative_url }}) | Complete, runnable programs covering the language surface |
| [Dependency Management]({{ '/docs/dependency-management/' | relative_url }}) | `gala.mod`, GALA and Go dependencies, and Bazel integration |
| [Error Codes]({{ '/docs/errors/' | relative_url }}) | Every `GALA-Exxxx` diagnostic — when it fires and how to fix it |
| [IDE Support]({{ '/features/ide-support/' | relative_url }}) | IntelliJ/GoLand plugin and the `gala lsp` server (VS Code, Neovim) |
| [Playground](https://gala-playground.fly.dev) | Try GALA in your browser -- no installation required |
| [GitHub Repository](https://github.com/martianoff/gala) | Source code, issue tracker, and release downloads |

## Showcase Projects

| Project | Description |
|---------|-------------|
| [GALA Playground](https://github.com/martianoff/gala-playground) | Web-based playground -- [try it live](https://gala-playground.fly.dev) |
| [State Machine Example](https://github.com/martianoff/gala-state-machine-example) | State machines with sealed types and pattern matching |
| [Log Analyzer](https://github.com/martianoff/gala-log-analyzer) | Structured log parsing with Go stdlib interop and functional pipelines |
| [GALA Server](https://github.com/martianoff/gala-server) | Immutable HTTP server library with builder-pattern configuration |
| [GALA TUI](https://github.com/martianoff/gala-tui) | Elm-architecture TUI framework -- immutable widgets, differential renderer, async runtime |
| [GALA Team](https://github.com/martianoff/gala-team) | Multi-agent Claude CLI orchestrator -- Team Lead delegates to Engineers and QAs, reviews work, hands you a PR |
