---
layout: default
title: "GALA — Scala on Go"
description: "Scala on Go. A statically typed, functional-first language that transpiles to Go — sealed types, pattern matching, monads, full Go interop."
keywords: "gala language, scala on go, golang sum types, golang pattern matching, golang option type, golang algebraic data types, transpile to go, golang functional programming, golang sealed types, golang zero reflection json"
schema_type: "SoftwareApplication"
permalink: /
---

<div class="hero">
  <h1>Scala on Go.</h1>
  <p class="tagline">The sum types, exhaustive pattern matching, and <code>Option</code> types Go still doesn't have — as a language, not a library. <code>Option</code>/<code>Either</code>/<code>Try</code> monads, zero-reflection JSON, first-class interop with every Go module, native binaries.</p>
  <a href="https://gala-playground.fly.dev" class="cta">Try in Playground</a>
  <a href="https://github.com/martianoff/gala" class="cta cta-secondary">View on GitHub</a>

  <pre class="hero-code"><code>sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
}

func area(s Shape) string = s match {
    case Circle(r)       =&gt; f"circle: ${3.14159 * r * r}%.2f"
    case Rectangle(w, h) =&gt; f"rect:   ${w * h}%.2f"
}</code></pre>
</div>

<p class="hero-aside"><em>GALA — Go Alternative LAnguage — is a statically typed, functional-first language that transpiles to Go.</em></p>

## Why GALA Exists

The [2024 Go Developer Survey](https://go.dev/blog/survey2024-h1-results) confirmed that **sum types and enums are the #1 most-requested missing feature** in Go. As of Go 1.25, they still don't exist. GALA delivers them today — along with exhaustive pattern matching, `Option[T]`/`Either[A,B]`/`Try[T]` monads, default parameters with named arguments, a zero-reflection JSON codec, regex pattern matching extractors, and immutable collections — all compiling to a single native binary through the standard Go toolchain.

Unlike libraries like samber/lo or IBM/fp-go that bolt functional patterns onto Go's syntax, GALA adds these features **at the language level** with clean, concise syntax. Every Go library works out of the box. Your existing Go modules, third-party packages, and tooling remain fully compatible.

GALA's transpiler performs type inference, exhaustive match checking, and immutability enforcement at compile time. The generated Go code is clean and readable — there is no runtime overhead beyond what the equivalent hand-written Go would have.

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
<h3>Monadic Binding (<code>bind</code> / <code>also</code>)</h3>
<p>Do-notation for any monad. <code>bind</code> flattens <code>FlatMap</code> chains; <code>also</code> marks independent steps that accumulate errors (<code>Validated</code>) or run concurrently (<code>Future</code>).</p>
<pre><code>func processOrder(id int) Try[Receipt] {
    bind o       = fetchOrder(id)
    bind valid   = validateOrder(o)
    bind payment = chargePayment(valid)
    Success(Receipt(o.Id, payment))
}</code></pre>
<p><a href="{{ '/features/monadic-binding/' | relative_url }}">Learn about bind / also</a></p>
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
<h3>Default Parameters &amp; Named Arguments</h3>
<p>No more functional options pattern or config structs. <strong>Default parameter values</strong> and <strong>named arguments</strong> work directly in function signatures.</p>
<pre><code>func connect(host string,
    port int = 8080, tls bool = true,
) Connection

connect("localhost", tls = false)</code></pre>
<p><a href="{{ '/docs/language-reference/' | relative_url }}">Learn about default parameters</a></p>
</div>

<div class="feature-card">
<h3>Zero-Reflection JSON Codec</h3>
<p>Compile-time <code>StructMeta[T]</code> generates typed serialization with no reflection, no struct tags. Builder pattern for <code>Rename</code>, <code>Omit</code>, and naming strategies.</p>
<pre><code>val codec = Codec[Person](SnakeCase())
val jsonStr = codec.Encode(person).Get()
val decoded = codec.Decode(jsonStr)</code></pre>
<p><a href="{{ '/docs/json/' | relative_url }}">Learn about JSON codec</a></p>
</div>

<div class="feature-card">
<h3>Regex Pattern Matching</h3>
<p>Compile-safe regex with extractors that destructure capture groups directly in <code>match</code> expressions. No manual group indexing.</p>
<pre><code>val date = regex.MustCompile(
    "(\\d{4})-(\\d{2})-(\\d{2})")

input match {
    case date(Array(y, m, d)) =&gt;
        s"$y/$m/$d"
}</code></pre>
<p><a href="{{ '/docs/regex/' | relative_url }}">Learn about regex</a></p>
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

<div class="feature-card">
<h3>IDE Support with LSP</h3>
<p>GoLand/IntelliJ plugin with syntax highlighting, type-aware dot completion, inlay type hints, and structure view. LSP server provides diagnostics, hover, go-to-definition, and more.</p>
<img src="{{ '/assets/images/ide/dot-completion.png' | relative_url }}" alt="GALA code completion in IntelliJ" style="max-width: 100%; border: 1px solid #e1e4e8; border-radius: 4px; margin: 0.5rem 0;">
<p><a href="{{ '/features/ide-support/' | relative_url }}">See IDE features</a></p>
</div>

</div>

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
gala mod init example.com/hello
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
| `Codec[T]` | Zero-reflection JSON codec — `Encode`, `Decode`, `Rename`, `Omit`, pattern matching | [JSON codec]({{ '/docs/json/' | relative_url }}) |
| `Regex` | Regular expressions with `Unapply` for pattern matching | [Regex]({{ '/docs/regex/' | relative_url }}) |
| `IO[T]` | Lazy composable effects — `Of`, `Suspend`, `Map`, `FlatMap` | [IO effect]({{ '/docs/io/' | relative_url }}) |

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
| [JSON Codec]({{ '/docs/json/' | relative_url }}) | Zero-reflection JSON serialization with `Codec[T]` |
| [Regex]({{ '/docs/regex/' | relative_url }}) | Pattern matching with regex extractors |
| [IO Effect]({{ '/docs/io/' | relative_url }}) | Lazy, composable side effects |
| [Go Interop]({{ '/features/go-interop/' | relative_url }}) | Using Go libraries and types from GALA |
| [Playground]({{ '/playground/' | relative_url }}) | Try GALA in your browser — no install needed |

### Showcase Projects

| Project | Description |
|---------|-------------|
| [GALA Playground](https://github.com/martianoff/gala-playground) | Web-based playground — [try it live](https://gala-playground.fly.dev) |
| [State Machine Example](https://github.com/martianoff/gala-state-machine-example) | State machines with sealed types and pattern matching |
| [Log Analyzer](https://github.com/martianoff/gala-log-analyzer) | Structured log parsing with Go stdlib interop and functional pipelines |
| [GALA Server](https://github.com/martianoff/gala-server) | Immutable HTTP server library with builder-pattern configuration |
| [GALA TUI](https://github.com/martianoff/gala-tui) | Elm-architecture TUI framework — immutable widgets, differential renderer, async runtime |
| [GALA Team](https://github.com/martianoff/gala-team) | Multi-agent Claude CLI orchestrator — Team Lead delegates to Engineers and QAs, reviews work, hands you a PR |
