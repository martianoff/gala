---
layout: default
title: "GALA vs Borgo — A Maintained, Licensed Language That Compiles to Go"
description: "GALA and Borgo both bring sum types and pattern matching to Go. The practical differences: GALA is actively maintained, Apache-2.0 licensed, and ships dependency management, a standard library, and IDE tooling."
keywords: "gala vs borgo, borgo alternative, borgo language, maintained borgo alternative, compile to go language, golang sum types, golang pattern matching, borgo abandoned, scala on go, rust on go"
permalink: /vs-borgo/
---

<div class="breadcrumb">
  <a href="{{ '/' | relative_url }}">Home</a> &raquo; GALA vs Borgo
</div>

*Two languages that compile to Go. The difference is what's around the language.*

# GALA vs Borgo

[Borgo](https://github.com/borgo-lang/borgo) was one of the first projects to make a compelling case that Go could have sum types and pattern matching. Its "Rust on Go" framing was instantly legible, its landing page shipped an in-browser playground, and it earned thousands of stars for a good reason: it showed real demand for a more expressive language on top of the Go runtime.

GALA shares that core thesis — algebraic data types and exhaustive pattern matching, compiled to plain Go. If you found Borgo and wished it were still moving, this page is the honest comparison of where the two differ in practice.

We want to be fair to Borgo: it was an influential proof of concept, and the demand it surfaced is real. The differences below are about what surrounds the language — maintenance, licensing, and the tooling a language needs to be used day to day — not about who had the better idea.

---

## At a glance

| | GALA | Borgo |
|---|---|---|
| Core idea | Sum types + pattern matching, compiled to Go | Sum types + pattern matching, compiled to Go |
| Syntax family | Scala-style | Rust-style |
| Active development | Yes — frequent releases | Last commit October 2024 |
| License | Apache 2.0 | None published |
| Dependency management | `gala mod` (Go + GALA deps, incl. third-party Go) | None |
| Standard library | `Option`/`Either`/`Try`/`Future`/`IO`, immutable collections, JSON/YAML codecs, regex, crypto, fs | Basics |
| Editor tooling | GoLand/IntelliJ plugin + LSP (VS Code, Neovim) | None |
| Go interop | Full — any Go module, types inferred from the Go SDK | Limited |
| Monadic do-notation | `bind` / `also` | No |
| Self-hosting path | Transpiler written in Go | Compiler written in Rust |

---

## What this means in practice

### 1. It's still moving

The single biggest difference is momentum. Borgo's last commit was in October 2024; GALA ships releases regularly, and every reported transpiler bug becomes a permanent regression test before it's fixed. For a language — where you're betting your codebase on the compiler keeping up with your needs — an actively maintained project is a different kind of commitment than an archived one.

### 2. There's a license

Borgo does not publish a license, which leaves its legal status ambiguous for anyone who wants to build something real on it. GALA is Apache 2.0. That's a prerequisite for adoption inside most companies, not a nice-to-have.

### 3. Dependencies and the Go ecosystem

GALA treats the Go ecosystem as first-class. `gala mod add` pulls both GALA and Go dependencies, including third-party Go modules, and the transpiler reads the Go SDK to infer return types — `(T, error)` results are wrapped into `Try[T]` at the call site:

```gala
import . "martianoff/gala/std"

// A Go function returning (T, error) becomes a Try[T] you can compose
val user = fetchUser(id)          // Try[User]
    .Map((u) => u.Name)
    .GetOrElse("anonymous")
```

### 4. A standard library that reaches the edges

GALA's standard library goes well past the basics: `Option`/`Either`/`Try`/`Future`/`IO`, immutable `List`/`Array`/`HashMap`/`HashSet`/`TreeSet`/`TreeMap`, zero-reflection JSON and YAML codecs, regex extractors, and `crypto`/`fs`/`path`/`strings` packages whose fallible operations return `Try` instead of panicking.

### 5. Tooling you'll actually feel

GALA ships a GoLand/IntelliJ plugin and an LSP server (`gala lsp`) for VS Code and Neovim: real-time diagnostics including match-exhaustiveness, hover types, go-to-definition across files, and inlay hints. A language without an editor story is a hard sell for daily work.

---

## If you came from Borgo

The concepts transfer directly — you already think in sum types and pattern matching. The syntax is Scala-flavored rather than Rust-flavored, and the surrounding pieces (deps, stdlib, IDE) are there. The fastest way to see whether it fits:

**[Try GALA in your browser](https://gala-playground.fly.dev)** — no install — or read the [GALA vs Go]({{ '/vs-go/' | relative_url }}) comparison for side-by-side code.
