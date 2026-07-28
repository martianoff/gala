# GALA

**Sum types, exhaustive pattern matching, and `Option`/`Try` — the type system Go is missing, as a language, not a library.**

**Scala on Go.**

[![Build](https://github.com/martianoff/gala/actions/workflows/test.yml/badge.svg)](https://github.com/martianoff/gala/actions/workflows/test.yml)
[![Release](https://github.com/martianoff/gala/actions/workflows/release.yml/badge.svg)](https://github.com/martianoff/gala/releases)
[![GitHub release (latest SemVer)](https://img.shields.io/github/v/release/martianoff/gala?sort=semver)](https://github.com/martianoff/gala/releases/latest)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Bazel](https://img.shields.io/badge/Built%20with-Bazel-43A047)](https://bazel.build)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.MD)

<img alt="GALA Logo" height="384" src="gala_escape.png" width="700"/>

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
}

func area(s Shape) string = s match {
    case Circle(r)       => f"circle area: ${3.14159 * r * r}%.2f"
    case Rectangle(w, h) => f"rect area: ${w * h}%.2f"
}
```

**[Try it in your browser](https://gala-playground.fly.dev)** — no install.

---

## What is GALA?

GALA is a statically typed, functional-first language that transpiles to Go. You get sealed types, exhaustive pattern matching, immutable collections, and a real monad stack (`Option`, `Either`, `Try`, `Future`, `IO`) — and you keep the entire Go ecosystem, including third-party modules, working out of the box.

It's aimed at Go developers who have used Scala, Kotlin, F#, or OCaml and miss the type system.

*(GALA stands for Go Alternative LAnguage.)*

---

## Why GALA? — Safe, Ergonomic, Compatible

**Safe — Go's runtime bugs, caught by the compiler.**
- **Sealed types with exhaustive matching.** The compiler rejects incomplete matches at build time — including nested patterns, guards, and generic extractors. An incomplete `match` is a build error, not a production panic.
- **No `nil`.** `Option` / `Either` / `Try` replace nil checks and naked error returns.
- **Compile-time data-race safety.** A value crossing a goroutine boundary must be *deeply immutable*, and the compiler checks it — including what a closure captures. Because GALA is already immutable by default the check is silent in ordinary code; it fires on `collection_mutable` values, `var` fields, raw Go slices/maps/pointers, and opaque Go handles.
- **Immutable by default.** `val` bindings, immutable struct fields, read-only `ConstPtr[T]`; always concrete types, never a silent `any`.
- **Zero-reflection JSON.** `Codec[T]` is backed by a compiler-generated `StructMeta[T]` intrinsic — no `reflect`, no struct tags, and it doubles as a pattern-match extractor.

**Ergonomic — the functional code you want to write, minus the ceremony.**
- **`bind` / `also` do-notation.** Flat monadic binding over any monad — sequential `bind`, plus `also` for independent steps that accumulate errors (`Validated`) or run concurrently (`Future`). No nested `FlatMap` staircases.
- **Functional standard library.** `Option`, `Either`, `Try`, `Future`, `IO` with `Map` / `FlatMap` / `Recover`, plus immutable `List`, `Array`, `HashMap`, `HashSet`, `TreeSet`, `TreeMap`.
- **Less syntax.** String interpolation (`s"…"` / `f"…"`), named arguments and default parameters, type inference everywhere, expression functions, and regex extractors that destructure capture groups directly inside `match`.
- **Diagnostics that point at the fix.** Framed, Rust/Elm-style compile errors carrying a stable `GALA-Exxxx` code, the offending span, and a hint — and runtime panics that report *GALA* source positions (`foo.gala:12`), not generated Go.

**Compatible — every Go library, no bindings, native binaries.**
- **Full Go third-party module interop.** Any Go package works — not just stdlib. Return types are inferred **directly from the Go SDK** (no declaration files), and `(T, error)` returns are wrapped into `Try[T]` automatically.
- **Shipped IDE tooling.** GoLand/IntelliJ plugin and an LSP server (`gala lsp`) for VS Code and Neovim — diagnostics, hover, go-to-definition, inlay hints, completion, across GALA *and* Go stdlib/third-party types. Batched transpilation and analysis caching keep multi-file rebuilds fast.

### Recent highlights

- **[Compile-time data-race safety](docs/CONCURRENCY_SAFETY.MD)** — closures crossing a `Future` boundary are checked for what they capture ([GALA-E0037](docs/errors/GALA-E0037.md)).
- **[Structured concurrency](docs/CONCURRENT.MD)** — `Future` cancellation, `Race`, and `WithTimeout` that cancel the work they abandon.
- **Guaranteed tail-call elimination** for self-tail-recursive if-expression functions — constant stack space.
- **[GALA stack traces](docs/PERFORMANCE_DEBUGGING.MD)** — panics report `foo.gala:12`, including frames inside imported GALA packages.
- **[Framed compile diagnostics](docs/errors/README.md)** — every error carries a stable code with its own doc page.
- **`use` scoped-resource binding** — replaces Go's `defer x.Close()`, which is now rejected on the GALA surface.
- **[`subprocess`](docs/SUBPROCESS.MD)** — spawn and drive child processes, with async `Future`-returning methods.
- **IDE** — go-to-definition and completion now resolve third-party Go modules.

---

## Quick Start

### 1. Install

Download a pre-built binary from [Releases](https://github.com/martianoff/gala/releases), rename it to `gala` (or `gala.exe` on Windows), and put it on your `PATH`.

GALA transpiles to Go, so it needs [Go 1.25+](https://go.dev/dl/) on your `PATH` to compile programs.

Or build from source:

```bash
git clone https://github.com/martianoff/gala.git && cd gala
bazel build //cmd/gala:gala
```

### 2. Write `main.gala`

```gala
package main

struct Person(Name string, Age int)

func greet(p Person) string = p match {
    case Person(name, age) if age < 18 => s"Hey, $name!"
    case Person(name, _)               => s"Hello, $name"
    case _                             => "Unknown"
}

func main() {
    Println(greet(Person("Alice", 25)))
}
```

### 3. Run

```bash
gala mod init example.com/hello
gala run main.gala     # Transpile + compile + run
gala build             # Build project to a binary
gala test              # Run tests
```

**Need help?** Ask in [GitHub Discussions](https://github.com/martianoff/gala/discussions).

---

## GALA vs Go

### Pattern Matching vs Switch

<table>
<tr><th>GALA</th><th>Go</th></tr>
<tr>
<td>

```gala
val msg = shape match {
    case Circle(r)      => f"r=$r%.1f"
    case Rectangle(w,h) => f"$w%.0fx$h%.0f"
    case Point()        => "point"
}
```

</td>
<td>

```go
var msg string
switch s := shape.(type) {
case Circle:
    msg = fmt.Sprintf("r=%.1f", s.Radius)
case Rectangle:
    msg = fmt.Sprintf("%.0fx%.0f", s.Width, s.Height)
case Point:
    msg = "point"
}
```

</td>
</tr>
</table>

### Option Handling vs nil Checks

<table>
<tr><th>GALA</th><th>Go</th></tr>
<tr>
<td>

```gala
val name = user.Name
    .Map((n) => strings.ToUpper(n))
    .GetOrElse("ANONYMOUS")
```

</td>
<td>

```go
name := "ANONYMOUS"
if user.Name != nil {
    name = strings.ToUpper(*user.Name)
}
```

</td>
</tr>
</table>

### Immutable Structs vs Manual Copying

<table>
<tr><th>GALA</th><th>Go</th></tr>
<tr>
<td>

```gala
struct Config(Host string, Port int)
val updated = config.Copy(Port = 8080)
```

</td>
<td>

```go
type Config struct {
    Host string
    Port int
}
updated := config     // value copy; both stay mutable
updated.Port = 8080
```

</td>
</tr>
</table>

### Error Handling: Try vs if-err

<table>
<tr><th>GALA</th><th>Go</th></tr>
<tr>
<td>

```gala
val result = divide(10, 2)
    .Map((x) => x * 2)
    .FlatMap((x) => divide(x, 3))
    .Recover((e) => 0)
```

</td>
<td>

```go
result, err := divide(10, 2)
if err == nil {
    result, err = divide(result*2, 3)
}
if err != nil {
    result = 0
}
```

</td>
</tr>
</table>

---

## Monadic Binding: `bind` / `also`

`Map`/`FlatMap` chains nest badly the moment a later step needs an earlier value. `bind` flattens them — every binding is a normal immutable local that stays in scope, and the block short-circuits on the first failure:

```gala
func processOrder(id int) Try[Receipt] {
    bind o       = fetchOrder(id)
    bind valid   = validateOrder(o)
    bind payment = chargePayment(valid)
    Success(Receipt(o.Id, payment))   // `o` still in scope; no nested FlatMap
}
```

`also` marks a bind as **independent** of its group, and the block's type decides what that unlocks. Over `Validated` it **accumulates every error** instead of stopping at the first:

```gala
import . "martianoff/gala/validation"

func makePerson(name string, email string, age int) Validated[string, Person] {
    bind n = vName(name)
    also e = vEmail(email)
    also a = vAge(age)
    Valid(Person(n, e, a))
}
// makePerson("", "", -1).GetErrors().Size() == 3  — all failures at once
```

`Validated[E, A]` is a sealed type (`Valid` / `Invalid`) in its own `validation` package, deliberately separate from `Either`'s fail-fast semantics. It works without `bind`/`also` too: `Zip2` … `Zip10` combine up to ten independent checks and return **every** failure, and `ToEither()` converts back when you want to stop accumulating.

Over `Future`, an `also` group runs its clauses **concurrently**. And it's not special-cased to the standard library: any type with a `FlatMap` method is bindable, resolved structurally at transpile time — no higher-kinded types. See [Monadic binding](docs/BIND_NOTATION.MD).

---

## More Language Features

**Expression functions** — single-expression bodies skip braces and `return`.

```gala
func square(x int) int = x * x
func max(a int, b int) int = if (a > b) a else b
```

**Named arguments** — any order; compiler reorders to match the signature.

```gala
func connect(host string, port int = 8080, tls bool = true) Connection

connect("localhost")                  // port=8080, tls=true
connect("localhost", tls = false)     // port=8080, tls=false
connect(host = "db", port = 5432)     // tls=true
```

**Lambda type inference** — parameter types and method type parameters are inferred from context.

```gala
val list = ListOf(1, 2, 3)
val doubled = list.Map((x) => x * 2)
val sum = list.FoldLeft(0, (acc, x) => acc + x)
```

**Guaranteed tail-call elimination** — a function that calls *itself* in tail position compiles to a loop, so it runs in constant stack space.

```gala
func sumTo(n int, acc int) int = if (n <= 0) acc else sumTo(n - 1, acc + n)

Println(sumTo(1000000, 0))   // 500000500000 — no stack overflow
```

The guarantee is deliberately narrow: **expression-bodied plain functions** (no receiver) whose body is an **if-expression** with single-expression branches, calling themselves **directly** in tail position. Method receivers, `match`- and block-bodied functions, a tail call hidden inside a block branch, and mutual recursion all fall back to ordinary recursion. Verified in [`examples/tco_deep_sum.gala`](examples/tco_deep_sum.gala) and [`examples/tco_factorial.gala`](examples/tco_factorial.gala).

**`use` — scoped resource binding** — binds a resource for the rest of the block and guarantees `Close()` on every exit path, normal or panic. Multiple bindings release LIFO.

```gala
func work() {
    use a = open("a")
    use b = open("b")                          // acquired last, closes first
    Println(s"body sees ${a.name} and ${b.name}")
}
```

`use` is the replacement for Go's `defer x.Close()` — `defer`, `go`, `goto`, `fallthrough`, `select`, and `chan` are all rejected on the GALA surface (`GALA-E0036`), routed instead through `use`, the `resource` combinators (`Using` / `Bracket` / `WithLock`), and `go_interop`. Bare Go builtins are rejected the same way (`GALA-E0035`): `len(x)` becomes `x.Size()` (characters) or `x.ByteSize()` (bytes), and `append` / `make` / `new` / `delete` become `go_interop` helpers or GALA collections. Both checks are resolver-aware — a function you declared yourself that happens to share the name is left alone.

**Tuples with destructuring** — up to `Tuple5`, with pattern matching.

```gala
val pair = (1, "hello")
val (x, y) = pair
```

**Read-only pointers** — `ConstPtr[T]` prevents accidental mutation through pointers.

```gala
val data = 42
val ptr = &data       // ConstPtr[int], not *int
val value = *ptr      // OK: read
// *ptr = 100         // compile error: cannot write through ConstPtr
```

**String interpolation** — `s"..."` with auto-inferred format verbs and `f"..."` with explicit format specs. No imports needed.

```gala
val name = "Alice"
val age = 30
Println(s"$name is $age years old")       // Alice is 30 years old
Println(f"Pi = ${3.14159}%.2f")           // Pi = 3.14
Println(s"${nums.MkString(", ")}")        // 1, 2, 3
```

**Zero-reflection JSON codec** — `Codec[T]` uses the compiler-generated `StructMeta[T]` intrinsic for fully typed serialization with no reflection, no struct tags, and pattern matching support.

```gala
struct Person(FirstName string, LastName string, Age int)

val codec = Codec[Person](SnakeCase())
val jsonStr = codec.Encode(Person("Alice", "Smith", 30)).Get()
// {"first_name":"Alice","last_name":"Smith","age":30}

val decoded = codec.Decode(jsonStr)       // Try[Person]
val name = jsonStr match {
    case codec(p) => p.FirstName          // pattern matching!
    case _        => "unknown"
}
```

**Regex with pattern matching** — compile-safe regex with extractors that destructure capture groups directly in `match`.

```gala
val dateRegex = regex.MustCompile("(\\d{4})-(\\d{2})-(\\d{2})")

"2024-01-15" match {
    case dateRegex(Array(year, month, day)) => s"$year/$month/$day"
    case _ => "not a date"
}
```

**Pattern matching with guards.**

```gala
val status = p match {
    case Person(name, age) if age < 18 => name + " is a minor"
    case Person(name, age) if age > 65 => name + " is a senior"
    case Person(name, _)               => name + " is an adult"
    case _                             => "Unknown"
}
```

**Type-based pattern matching.**

```gala
val res = x match {
    case s: string => s"string: $s"
    case i: int    => s"int: $i"
    case _         => "unknown"
}
```

**Collect — filter and transform in one pass.**

```gala
val nums = ArrayOf(1, 2, 3, 4, 5, 6)
val evenDoubled = nums.Collect({ case n if n % 2 == 0 => n * 2 })
// Array(4, 8, 12)

val options = ArrayOf(Some(1), None[int](), Some(2), None[int](), Some(3))
val values = options.Collect({ case Some(v) => v * 10 })
// Array(10, 20, 30)
```

---

## Concurrency — and Data Races the Compiler Catches

`Future[T]` is a monad first: `Map`, `FlatMap`, `Recover`, `Zip2`…`Zip10`, `Sequence`, `Traverse`, plus `Succeeded` / `Failed` / `Completed` extractors for `match`. On top of that sits **structured concurrency** — a Future carries an opaque cancellation token, and the scope constructors cancel the work they abandon:

```gala
val bounded = slow.WithTimeout(Milliseconds(500)).Recover((e) => 0)
val winner  = Race[int](ArrayOf[Future[int]](a, b))   // first result; losers cancelled

val chain = source.Map((v) => step1(v)).FlatMap((v) => step2(v))
chain.Cancel()   // pending stages fail with CancellationError
```

Honest limits: cancellation is checked at **combinator boundaries**, so it short-circuits stages that have not started but cannot preempt a body already running (Go has no goroutine interruption). It is **graph-level** — a derived chain shares one token — while `Race`, `Sequence`, `FirstCompletedOf`, and `WithTimeout` open a fresh scope. `.Cancel()` on a completed Future does nothing. Full semantics: [Concurrent](docs/CONCURRENT.MD).

### The data-race check

Sharing a value across goroutines is safe when the value is **deeply immutable** — concurrent reads of something nobody can mutate never conflict. GALA turns that into a compile-time rule at every goroutine boundary, checking not just the value's type but what the closure **captures**:

```gala
var counter = 0
Future(() => counter + 1)      // GALA-E0037: captures a reassignable `var`

val buffer = collection_mutable.ArrayOf(1, 2, 3)
Future(() => buffer.Size())    // GALA-E0037: a method call on a mutable value

val xs = collection_immutable.ArrayOf(1, 2, 3)
Future(() => xs.Size())        // OK — deeply immutable
```

The check is **field-access-sensitive**: a closure that reads only immutable (`val`) fields of shareable type is accepted even when the enclosing value is unshareable as a whole. A boundary is marked by the `Sendable[F]` type — transparent (`Sendable[func() T]` *is* `func() T` in the generated Go, no wrapper, no cost) and available to any library, not just the standard one. Application code passing closure literals never writes `Sendable`; only a reusable wrapper annotates its forwarded parameter, pushing the obligation to whoever writes the closure:

```gala
func afterCompute(compute Sendable[func() int]) Future[int] = Future(() => compute() + 1)

val base = 41
afterCompute(() => base).Get()   // checked here, at the closure literal
```

The escape hatch is explicit: `go_interop.Spawn` / `SpawnWithRecover` are **not** `Sendable`, so raw goroutine plumbing is unchecked and synchronizing it is on you. See [Concurrency Safety](docs/CONCURRENCY_SAFETY.MD) and [GALA-E0037](docs/errors/GALA-E0037.md).

---

## Diagnostics and Debugging

Compile errors are framed in the Rust/Elm style: a stable code, the offending span, and a hint.

```
error[GALA-E0035]: bare Go builtin "len(...)" is not part of GALA's surface
  --> foo.gala:4:13
  |
4 |     val n = len(s)
  |             ^^^ use `.Size()`
  |
  = hint: use `.Size()` (logical size — characters for strings) or `.ByteSize()` (raw bytes) instead of `len(...)`
```

Codes are opaque and never change meaning, so tools and tests can pin to them, and each one has a page explaining when it fires, a minimal repro, and the fix — see the [error code index](docs/errors/README.md).

**Panics report GALA source positions**, not generated Go:

```
panic: runtime error: integer divide by zero
main.divide(...)
	foo.gala:4
main.main()
	foo.gala:8
```

This rides on Go's own `//line` directives, so there is no source-map file and no runtime cost. Imported GALA packages map too — a single trace can span your files and a library's `.gala` source. The mapping is exact for ordinary statements; it is *approximate* (right file, line possibly off by a few within the construct) for statements that lower to several Go statements, such as `use x = …`, and for IIFE-lowered constructs (`match`, `if`-expressions, `bind`/`also`). Details: [Performance and Debugging](docs/PERFORMANCE_DEBUGGING.MD).

---

## Standard Library

### Functional Types

| Type | Description |
|------|-------------|
| `Option[T]` | Optional values — `Some(value)` / `None()` |
| `Either[A, B]` | Disjoint union — `Left(a)` / `Right(b)` |
| `Try[T]` | Failable computation — `Success(value)` / `Failure(err)` |
| `Validated[E, A]` | Error-**accumulating** validation — `Valid(a)` / `Invalid(errors)`, with `Zip2`…`Zip10` |
| `Future[T]` | Async computation with `Map`, `FlatMap`, `Zip`, `Await`, `WithTimeout`, `Cancel` |
| `IO[T]` | Lazy, composable effect type — `Suspend`, `Map`, `FlatMap`, `Recover` |
| `Tuple[A, B]` | Pairs and triples with `(a, b)` syntax (up to `Tuple5`) |
| `ConstPtr[T]` | Read-only pointer with auto-deref field access |

### Serialization, Regex, IO, Processes

| Type | Description |
|------|-------------|
| `Codec[T]` | Zero-reflection JSON codec with `Encode`, `Decode`, `Rename`, `Omit`, pattern matching |
| `yaml.Codec[T]` | YAML codec sharing the same `StructMeta[T]` intrinsic |
| `Regex` | Compiled regex with `Matches`, `FindFirst`, `FindAll`, `ReplaceAll`, pattern matching |
| `IO[T]` | Lazy effect — separates description from execution, re-runs on every `.Run()` |
| `resource` | `Using` / `Bracket` / `WithLock` — cleanup that runs on every exit path, including panics |
| `subprocess.Process` | Value-type handle to a spawned child — `ReadLine`, `WriteLine`, `Wait`, `Kill`, `KillAfter` |

`subprocess` also ships `ReadLineAsync` / `WriteLineAsync` / `CloseStdinAsync`, which return a `Future` and own the goroutine-safety invariant internally — so you drive a child off-thread without capturing the handle across a `Sendable` boundary. See [Subprocess](docs/SUBPROCESS.MD).

### Collections

| Type | Kind | Key Operations | Best for |
|------|------|----------------|----------|
| `List[T]` | Immutable | O(1) prepend, O(n) index | Recursive processing, prepend-heavy workloads |
| `Array[T]` | Immutable | O(1) random access | General-purpose indexed sequences |
| `HashMap[K,V]` | Immutable | O(1) lookup | Functional key-value storage |
| `HashSet[T]` | Immutable | O(1) membership | Unique element collections |
| `TreeSet[T]` | Immutable | O(log n) sorted ops | Ordered unique elements, range queries |
| `TreeMap[K,V]` | Immutable | O(log n) sorted ops | Sorted key-value storage, range queries |

All collections support `Map`, `Filter`, `FoldLeft`, `ForEach`, `Exists`, `Find`, `Collect`, `MkString`, `Sorted`, `SortWith`, `SortBy`, and more.

`TreeMap[K,V]` is a Red-Black tree that maintains entries in sorted key order. It provides `MinKey`, `MaxKey`, `Range(from, to)`, `RangeFrom`, `RangeTo`, and conversion to `HashMap`, Go maps, or sorted arrays.

Mutable variants of all collection types are available in `collection_mutable` for performance-sensitive code.

---

## Built with GALA

GALA is dogfooded, not just demoed — its own tooling and these projects are written **in the language**, which is how the ergonomics get stress-tested. Real applications written in GALA:

| Project | Description |
|---------|-------------|
| [GALA Playground](https://github.com/martianoff/gala-playground) | Web playground ([live](https://gala-playground.fly.dev)) — write and run GALA in the browser with 9 built-in examples |
| [State Machine Example](https://github.com/martianoff/gala-state-machine-example) | State machines with sealed types + pattern matching — order FSM, traffic light, vending machine (with Go comparison) |
| [Log Analyzer](https://github.com/martianoff/gala-log-analyzer) | Structured log parsing with Go stdlib interop (`strings`, `strconv`, `fmt`) + functional pipelines (with Go comparison) |
| [GALA Server](https://github.com/martianoff/gala-server) | Immutable HTTP server library with builder-pattern configuration, route groups, filters, and pattern matching |
| [GALA TUI](https://github.com/martianoff/gala-tui) | Elm-architecture TUI framework — immutable widgets, differential renderer, async runtime, fuzzy command palette, markdown, mouse, themes |
| [GALA Team](https://github.com/martianoff/gala-team) | Multi-agent Claude CLI orchestrator — a Team Lead delegates to Engineers and QAs, reviews their work, and hands you a PR for sign-off |

All of the above are written in GALA, not just "use GALA somewhere."

---

## Dependency Management

```bash
gala mod init github.com/user/project
gala mod add github.com/example/utils@v1.2.3
gala mod add github.com/google/uuid@v1.6.0 --go
gala mod tidy
```

Third-party Go modules are first-class. GALA reads the Go SDK to infer return types, and multi-return `(T, error)` patterns are auto-wrapped into `Try[T]` at the call site.

---

## IDE Support

GALA ships with a GoLand/IntelliJ plugin and an LSP server (`gala lsp`) for editor-agnostic support.

### GoLand / IntelliJ IDEA

1. Install GALA CLI: download from [releases](https://github.com/martianoff/gala/releases) and add to PATH.
2. Install plugin: GoLand > Settings > Plugins > Install from Disk > select `gala-intellij-plugin.zip` from [releases](https://github.com/martianoff/gala/releases).
3. Restart GoLand — the LSP server starts automatically when a `.gala` file is opened.

The plugin works locally (ANTLR parser, semantic highlighting, structure view, 12 live templates). The LSP server (`gala lsp`) adds real-time diagnostics including match-exhaustiveness, hover types, cross-file go-to-definition, type-aware completion, and inlay hints — for VS Code and Neovim too.

Go interop is wired end to end: completion and go-to-definition resolve Go stdlib **and third-party Go modules**, navigating into the module's cached source the same way native GALA symbols do. The `.Size()` / `.ByteSize()` sugar type-resolves and completes on Go primitives, the `use` keyword is highlighted with its binding clickable, and the forbidden-builtin / forbidden-statement rules surface inline as `GALA-E0035` / `GALA-E0036`. Full feature list: [IDE Support](https://gala.fyi/features/ide-support/).

### VS Code

Add to `.vscode/settings.json`:

```json
{
  "lsp.servers": {
    "gala": {
      "command": "gala",
      "args": ["lsp"],
      "filetypes": ["gala"]
    }
  }
}
```

### Neovim

```lua
require('lspconfig.configs').gala = {
  default_config = {
    cmd = { 'gala', 'lsp' },
    filetypes = { 'gala' },
    root_dir = require('lspconfig.util').root_pattern('gala.mod', '.git'),
  },
}
require('lspconfig').gala.setup({})
```

---

## Installation

### Pre-built Binaries

Download from [Releases](https://github.com/martianoff/gala/releases):

| Platform | Binary |
|----------|--------|
| Linux (x64) | `gala-linux-amd64` |
| Linux (ARM64) | `gala-linux-arm64` |
| macOS (x64) | `gala-darwin-amd64` |
| macOS (Apple Silicon) | `gala-darwin-arm64` |
| Windows (x64) | `gala-windows-amd64.exe` |

After downloading, rename the binary to `gala` (or `gala.exe` on Windows) and add it to your `PATH`.

**Prerequisite:** GALA needs [Go 1.25+](https://go.dev/dl/) on your `PATH` to compile programs. Install Go before running `gala build` or `gala run`.

### Build from Source

```bash
git clone https://github.com/martianoff/gala.git
cd gala
bazel build //cmd/gala:gala
```

### Using Bazel

```python
load("@rules_gala//gala:defs.bzl", "gala_binary", "gala_library")

gala_binary(
    name = "myapp",
    src = "main.gala",
)
```

---

## Documentation

- [Language Specification](docs/GALA.MD) — complete language reference
- [Why GALA?](docs/WHY_GALA.MD) — feature deep-dive and honest trade-offs
- [Examples](docs/EXAMPLES.MD) — code examples for all features
- [Type Inference](docs/TYPE_INFERENCE.MD) — how type inference works
- [Monadic Binding](docs/BIND_NOTATION.MD) — `bind` / `also` do-notation over any monad
- [Error Codes](docs/errors/README.md) — every `GALA-Exxxx` diagnostic, with repro and fix
- [JSON Codec](https://gala.fyi/docs/json/) — zero-reflection JSON serialization
- [Regex](https://gala.fyi/docs/regex/) — pattern matching with regex extractors
- [IO Effect](https://gala.fyi/docs/io/) — lazy, composable side effects
- [Go Interop](https://gala.fyi/features/go-interop/) — seamless Go library integration
- [Concurrent](docs/CONCURRENT.MD) — Future, Promise, cancellation, and ExecutionContext
- [Concurrency Safety](docs/CONCURRENCY_SAFETY.MD) — the shareability model and the `Sendable` boundary
- [Subprocess](docs/SUBPROCESS.MD) — spawning and driving child processes
- [Performance and Debugging](docs/PERFORMANCE_DEBUGGING.MD) — GALA stack traces and profiling knobs
- [Stream](docs/STREAM.MD) — lazy, potentially infinite sequences
- [Immutable Collections](docs/IMMUTABLE_COLLECTIONS.MD) — List, Array, HashMap, HashSet, TreeSet, TreeMap
- [Mutable Collections](docs/MUTABLE_COLLECTIONS.MD) — mutable variants for performance
- [Strings](docs/STRINGS.MD) — free functions over `string`, `Str` wrapper, and `StringBuilder`
- [Time Utils](docs/TIME_UTILS.MD) — Duration and Instant types
- [Dependency Management](docs/DEPENDENCY_MANAGEMENT.MD) — module system

---

## Contributing

Contributions are welcome. Please ensure:

1. `bazel build //...` passes
2. `bazel test //...` passes
3. New features include examples in `examples/`
4. Documentation is updated for grammar/feature changes

See [CONTRIBUTING.MD](CONTRIBUTING.MD) for details.

---

## License

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Apache License 2.0. See [LICENSE](LICENSE) for details.
