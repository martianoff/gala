---
layout: default
title: "GALA - Sum Types, Pattern Matching, and Option Types for Go"
description: "GALA adds the features Go developers want most — sum types (sealed types), exhaustive pattern matching, Option/Either/Try monads, and immutable collections. Transpiles to native Go binaries with full library compatibility."
keywords: "golang sum types, golang pattern matching, golang option type, go algebraic data types, go sealed types, transpile to go, golang error handling alternative, golang immutable collections, gala language"
schema_type: "SoftwareApplication"
permalink: /
---

<div class="hero">
  <h1>GALA</h1>
  <p class="tagline">Sum types, pattern matching, and Option types that Go is missing — compiled to native Go binaries</p>
  <a href="https://gala-playground.fly.dev" class="cta">Try in Playground</a>
  <a href="https://github.com/martianoff/gala" class="cta cta-secondary">View on GitHub</a>
</div>

## Sum Types and Pattern Matching for Go — Without Leaving the Ecosystem

**GALA** (Go Alternative LAnguage) is a modern programming language that transpiles to Go. The [2024 Go Developer Survey](https://go.dev/blog/survey2024-h1-results) confirmed that **sum types and enums are the #1 most-requested missing feature** in Go. As of Go 1.25, they still don't exist. GALA delivers them today — along with exhaustive pattern matching, `Option[T]`/`Either[A,B]`/`Try[T]` monads, and immutable collections — all compiling to a single native binary through the standard Go toolchain.

Unlike libraries like samber/lo or IBM/fp-go that bolt functional patterns onto Go's syntax, GALA adds these features **at the language level** with clean, concise syntax. Every Go library works out of the box. Your existing Go modules, third-party packages, and tooling remain fully compatible. GALA extends Go with the type-safety features it deliberately omits, while preserving Go's performance, deployment simplicity, and ecosystem.

GALA's transpiler performs type inference, exhaustive match checking, and immutability enforcement at compile time. The generated Go code is clean and readable — you can always inspect what GALA produces. There is no runtime overhead beyond what the equivalent hand-written Go would have.

## Features

<div class="feature-grid">

<div class="feature-card">
<h3>Sealed Types</h3>
<p>Define closed type hierarchies. The compiler rejects incomplete matches — no forgotten cases at runtime.</p>
<pre><code>sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
}</code></pre>
<p><a href="{{ '/features/sealed-types/' | relative_url }}">Learn about sealed types</a></p>
</div>

<div class="feature-card">
<h3>Pattern Matching</h3>
<p>Exhaustive <strong>pattern matching</strong> with destructuring, guards, and expression results — far beyond Go's <code>switch</code>.</p>
<pre><code>val msg = shape match {
    case Circle(r)       =&gt; f"r=$r%.1f"
    case Rectangle(w, h) =&gt; f"${w * h}%.2f"
}</code></pre>
<p><a href="{{ '/features/pattern-matching/' | relative_url }}">Learn about pattern matching</a></p>
</div>

<div class="feature-card">
<h3>Immutability by Default</h3>
<p><code>val</code> bindings are immutable. Struct fields are immutable. Auto-generated <code>Copy()</code> for safe updates.</p>
<pre><code>struct Config(Host string, Port int)
val updated = config.Copy(Port = 8080)</code></pre>
<p><a href="{{ '/features/immutability/' | relative_url }}">Learn about immutability</a></p>
</div>

<div class="feature-card">
<h3>Monadic Error Handling</h3>
<p><code>Option[T]</code>, <code>Either[A,B]</code>, and <code>Try[T]</code> replace nil checks and <code>if err != nil</code> with composable pipelines.</p>
<pre><code>val result = divide(10, 2)
    .Map((x) =&gt; x * 2)
    .FlatMap((x) =&gt; divide(x, 3))
    .Recover((e) =&gt; 0)</code></pre>
<p><a href="{{ '/features/error-handling/' | relative_url }}">Learn about error handling</a></p>
</div>

<div class="feature-card">
<h3>Functional Collections</h3>
<p>Immutable <code>List</code>, <code>Array</code>, <code>HashMap</code>, <code>HashSet</code>, <code>TreeSet</code>, <code>TreeMap</code> with <code>Map</code>, <code>Filter</code>, <code>FoldLeft</code>, <code>Collect</code>, and more.</p>
<pre><code>val nums = ArrayOf(1, 2, 3, 4, 5)
val evens = nums.Filter((x) =&gt; x % 2 == 0)
val sum = nums.FoldLeft(0, (acc, x) =&gt; acc + x)</code></pre>
<p><a href="{{ '/features/collections/' | relative_url }}">Learn about collections</a></p>
</div>

<div class="feature-card">
<h3>Full Go Interop</h3>
<p>Use any Go library. Go imports, Go types, and Go functions work directly in GALA code. One ecosystem, zero friction.</p>
<pre><code>import "strings"

val name = user.Name
    .Map((n) =&gt; strings.ToUpper(n))
    .GetOrElse("ANONYMOUS")</code></pre>
<p><a href="{{ '/features/go-interop/' | relative_url }}">Learn about Go interop</a></p>
</div>

</div>

## GALA vs Go — A Quick Look

Pattern matching is one of the most visible differences between GALA and Go. Where Go requires a manual type switch with field accessors, GALA destructures values directly and ensures every case is handled at compile time.

<div class="comparison">
<div>
<p><strong>GALA</strong></p>
<pre><code>val msg = shape match {
    case Circle(r)       =&gt; f"r=$r%.1f"
    case Rectangle(w, h) =&gt; f"$w%.0fx$h%.0f"
    case Point()         =&gt; "point"
}</code></pre>
</div>
<div>
<p><strong>Go</strong></p>
<pre><code>var msg string
switch shape._variant {
case Shape_Circle:
    msg = fmt.Sprintf("r=%.1f",
        shape.Radius.Get())
case Shape_Rectangle:
    msg = fmt.Sprintf("%fx%f",
        shape.Width.Get(),
        shape.Height.Get())
case Shape_Point:
    msg = "point"
}</code></pre>
</div>
</div>

GALA's version is shorter, handles destructuring automatically, and produces a compile-time error if you forget a case. See the [full GALA vs Go comparison]({{ '/vs-go/' | relative_url }}) for more examples including Option handling, immutable structs, and error handling.

## Get Started in 3 Steps

### 1. Install

Download a pre-built binary from [Releases](https://github.com/martianoff/gala/releases) for Linux, macOS, or Windows. Or build from source:

```bash
git clone https://github.com/martianoff/gala.git && cd gala
bazel build //cmd/gala:gala
```

### 2. Write

Create `main.gala`:

```gala
package main

struct Person(Name string, Age int)

func greet(p Person) string = p match {
    case Person(name, age) if age < 18 => s"Hey, $name!"
    case Person(name, _)               => s"Hello, $name"
}

func main() {
    Println(greet(Person("Alice", 25)))
}
```

### 3. Run

```bash
gala run main.gala
```

Or with Bazel for larger projects:

```bash
bazel run //myapp:myapp
```

See the full [Getting Started guide]({{ '/getting-started/' | relative_url }}) for project setup, Bazel integration, and dependency management.

## Standard Library

GALA ships with a standard library of type-safe data structures and monads, all built on sealed types and functional programming patterns.

### Types

| Type | Description | Documentation |
|------|-------------|---------------|
| `Option[T]` | Optional values — `Some(value)` / `None()` | [Monadic types]({{ '/features/error-handling/' | relative_url }}) |
| `Either[A, B]` | Disjoint union — `Left(a)` / `Right(b)` | [Monadic types]({{ '/features/error-handling/' | relative_url }}) |
| `Try[T]` | Failable computation — `Success(value)` / `Failure(err)` | [Monadic types]({{ '/features/error-handling/' | relative_url }}) |
| `Future[T]` | Async computation with `Map`, `FlatMap`, `Zip`, `Await` | [Concurrency]({{ '/features/concurrency/' | relative_url }}) |
| `Tuple[A, B]` | Pairs and triples with `(a, b)` syntax | [Language spec]({{ '/features/pattern-matching/' | relative_url }}) |
| `ConstPtr[T]` | Read-only pointer with compile-time enforcement | [Immutability]({{ '/features/immutability/' | relative_url }}) |
| `Json[T]` | JSON extractor — pattern match on JSON strings | [Monadic types]({{ '/features/error-handling/' | relative_url }}) |
| `Regex` | Regular expressions with `Unapply` for pattern matching | [Pattern matching]({{ '/features/pattern-matching/' | relative_url }}) |
| `IO[T]` | Lazy composable effects — `Of`, `Suspend`, `Map`, `FlatMap` | [Language spec]({{ '/docs/language-reference/' | relative_url }}) |

### Collections

| Type | Kind | Key Operations | Best for |
|------|------|----------------|----------|
| `List[T]` | Immutable | O(1) prepend, O(n) index | Recursive processing |
| `Array[T]` | Immutable | O(1) random access | General-purpose sequences |
| `HashMap[K,V]` | Immutable | O(1) lookup | Key-value storage |
| `HashSet[T]` | Immutable | O(1) membership | Unique element collections |
| `TreeSet[T]` | Immutable | O(log n) sorted ops | Ordered unique elements |
| `TreeMap[K,V]` | Immutable | O(log n) sorted ops | Sorted key-value storage |

All collections support `Map`, `Filter`, `FoldLeft`, `ForEach`, `Exists`, `Find`, `Collect`, `MkString`, `Sorted`, and more. Mutable variants are available in `collection_mutable` for performance-sensitive code. See the [collections documentation]({{ '/features/collections/' | relative_url }}) for details.

## Documentation

| Document | Description |
|----------|-------------|
| [Language Specification]({{ '/features/sealed-types/' | relative_url }}) | Complete reference for GALA syntax and semantics |
| [Getting Started]({{ '/getting-started/' | relative_url }}) | Installation, project setup, and first program |
| [GALA vs Go]({{ '/vs-go/' | relative_url }}) | Side-by-side comparison with idiomatic Go |
| [Sealed Types]({{ '/features/sealed-types/' | relative_url }}) | Algebraic data types and closed hierarchies |
| [Pattern Matching]({{ '/features/pattern-matching/' | relative_url }}) | Exhaustive matching, destructuring, and guards |
| [Immutability]({{ '/features/immutability/' | relative_url }}) | `val`, immutable structs, `Copy()`, and `ConstPtr[T]` |
| [Error Handling]({{ '/features/error-handling/' | relative_url }}) | `Option[T]`, `Either[A,B]`, `Try[T]` monads |
| [Collections]({{ '/features/collections/' | relative_url }}) | Immutable and mutable functional collections |
| [Concurrency]({{ '/features/concurrency/' | relative_url }}) | `Future[T]`, `Promise[T]`, and `ExecutionContext` |
| [Go Interop]({{ '/features/go-interop/' | relative_url }}) | Using Go libraries and types from GALA |
| [Playground]({{ '/playground/' | relative_url }}) | Try GALA in your browser — no install needed |

### Showcase Projects

| Project | Description |
|---------|-------------|
| [GALA Playground](https://github.com/martianoff/gala-playground) | Web-based playground — [try it live](https://gala-playground.fly.dev) |
| [State Machine Example](https://github.com/martianoff/gala-state-machine-example) | State machines with sealed types and pattern matching |
| [Log Analyzer](https://github.com/martianoff/gala-log-analyzer) | Structured log parsing with Go stdlib interop and functional pipelines |
| [GALA Server](https://github.com/martianoff/gala-server) | Immutable HTTP server library with builder-pattern configuration |
