---
layout: default
title: "GALA Documentation — Sum Types, Pattern Matching, Collections, and Concurrency for Go"
description: "Complete GALA documentation. Language reference, sum types (sealed types), pattern matching, Option/Either/Try, immutable collections, Futures, streams, and dependency management — all transpiling to Go."
keywords: "gala documentation, gala docs, golang sum types docs, golang pattern matching docs, gala language reference, gala api docs, golang option type documentation"
permalink: /docs/
---

# GALA Documentation

Welcome to the GALA documentation hub. Here you will find everything you need to learn and use GALA effectively.

## Core Language

| Topic | Description |
|-------|-------------|
| [Sealed Types]({{ '/features/sealed-types/' | relative_url }}) | Define closed type hierarchies with exhaustive compile-time checking |
| [Pattern Matching]({{ '/features/pattern-matching/' | relative_url }}) | Destructure values, apply guards, and produce expression results |
| [Immutability]({{ '/features/immutability/' | relative_url }}) | `val` bindings, immutable struct fields, `Copy()`, and `ConstPtr[T]` |
| [Error Handling]({{ '/features/error-handling/' | relative_url }}) | Composable `Option[T]`, `Either[A,B]`, and `Try[T]` monads |
| [GALA vs Go]({{ '/vs-go/' | relative_url }}) | Side-by-side comparison with idiomatic Go code |

## Standard Library

| Topic | Description |
|-------|-------------|
| [Collections]({{ '/features/collections/' | relative_url }}) | Immutable `List`, `Array`, `HashMap`, `HashSet`, `TreeSet`, `TreeMap` and their mutable variants |
| [Concurrency]({{ '/features/concurrency/' | relative_url }}) | `Future[T]`, `Promise[T]`, `ExecutionContext`, `Await`, and `Zip` |
| [Go Interop]({{ '/features/go-interop/' | relative_url }}) | Import and use any Go package, type, or function directly from GALA |
| [Json]({{ '/docs/json/' | relative_url }}) | Type-safe JSON serialization, deserialization, and pattern matching extractors |
| [Regex]({{ '/docs/regex/' | relative_url }}) | Regular expressions with pattern matching and Array destructuring |
| [IO Effect]({{ '/docs/io/' | relative_url }}) | Lazy composable side effects with `IO[T]` |

## Guides

| Topic | Description |
|-------|-------------|
| [Getting Started]({{ '/getting-started/' | relative_url }}) | Installation, project setup, and writing your first GALA program |
| [Playground](https://gala-playground.fly.dev) | Try GALA in your browser -- no installation required |
| [GitHub Repository](https://github.com/martianoff/gala) | Source code, issue tracker, and release downloads |

## Showcase Projects

| Project | Description |
|---------|-------------|
| [GALA Playground](https://github.com/martianoff/gala-playground) | Web-based playground -- [try it live](https://gala-playground.fly.dev) |
| [State Machine Example](https://github.com/martianoff/gala-state-machine-example) | State machines with sealed types and pattern matching |
| [Log Analyzer](https://github.com/martianoff/gala-log-analyzer) | Structured log parsing with Go stdlib interop and functional pipelines |
| [GALA Server](https://github.com/martianoff/gala-server) | Immutable HTTP server library with builder-pattern configuration |
