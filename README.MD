# GALA

[![Release](https://github.com/martianoff/gala/actions/workflows/release.yml/badge.svg)](https://github.com/martianoff/gala/releases)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev)

**GALA** (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It brings functional programming, pattern matching, and immutability-first design to the Go ecosystem - without sacrificing Go's performance, tooling, or library compatibility.

```gala
package main

import "fmt"

sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
}

func area(s Shape) string = s match {
    case Circle(r)      => fmt.Sprintf("circle area: %.2f", 3.14159 * r * r)
    case Rectangle(w, h) => fmt.Sprintf("rect area: %.2f", w * h)
}

func main() {
    val shapes = SliceOf(Circle(5.0), Rectangle(3.0, 4.0))
    for _, s := range shapes {
        fmt.Println(area(s))
    }
}
```

**Code is mostly AI-Generated**

---

## Why GALA?

GALA gives you the features you miss when writing Go - without leaving the Go ecosystem:

| What you get | How it works |
|---|---|
| **Sealed types (ADTs)** | Define closed type hierarchies with exhaustive pattern matching |
| **Immutability by default** | `val` and `:=` are immutable; mutation is opt-in with `var` |
| **Pattern matching** | Extractors, guards, sequence patterns, nested matching |
| **Expression functions** | `func square(x int) int = x * x` |
| **Lambda type inference** | `list.Map((x) => x * 2)` - parameter types inferred from context |
| **Monads** | `Option[T]`, `Either[A,B]`, `Try[T]`, `Future[T]` with full APIs |
| **Functional collections** | Immutable `List`, `Array`, `HashMap`, `HashSet`, `TreeSet` |
| **Tuples** | `val pair = (1, "hello")` with destructuring and pattern matching |
| **Read-only pointers** | `ConstPtr[T]` prevents accidental mutation through pointers |
| **Full Go interop** | Use any Go library. Output is clean, readable Go code |

---

## Quick Start

### Install

Download a pre-built binary from [Releases](https://github.com/martianoff/gala/releases), or build from source:

```bash
git clone https://github.com/martianoff/gala.git && cd gala
bazel build //cmd/gala:gala
```

### Write

```gala
package main

import "fmt"

struct Person(Name string, Age int)

func greet(p Person) string = p match {
    case Person(name, age) if age < 18 => "Hey, " + name + "!"
    case Person(name, _)               => "Hello, " + name
    case _                             => "Hello"
}

func main() {
    val alice = Person("Alice", 25)
    fmt.Println(greet(alice))
}
```

### Run

```bash
gala run main.gala

# Or use Bazel (recommended)
bazel run //myapp:myapp
```

---

## Feature Showcase

### Sealed Types (Algebraic Data Types)

Define closed type hierarchies. The compiler enforces exhaustive matching - miss a variant and you get an error.

```gala
sealed type Result[T any] {
    case Ok(Value T)
    case Err(Message string)
}

val r = Ok[int](42)
val msg = r match {
    case Ok(v)    => fmt.Sprintf("got %d", v)
    case Err(msg) => "error: " + msg
    // No case _ needed - all variants covered
}
```

The standard library uses sealed types for `Option[T]`, `Either[A,B]`, and `Try[T]`.

### Pattern Matching

GALA's match expressions support extractors, guards, type patterns, sequence patterns, and boolean exhaustiveness.

```gala
// Extractor patterns
val opt = Some(42)
val result = opt match {
    case Some(x) if x > 0 => "positive"
    case Some(_)           => "non-positive"
    case None()            => "empty"
}

// Sequence patterns
val list = ListOf(1, 2, 3, 4, 5)
val head = list match {
    case List(first, second, rest...) => fmt.Sprintf("%d, %d + %d more", first, second, rest.Size())
    case List(only)                   => fmt.Sprintf("just %d", only)
    case List()                       => "empty"
}

// Boolean exhaustive match
val label = enabled match {
    case true  => "on"
    case false => "off"
}
```

### Immutability by Default

Variables and struct fields are immutable unless explicitly marked `var`. Structs auto-generate `Copy()` and `Equal()` methods.

```gala
val x = 10             // immutable
var y = 20             // mutable
z := 30                // immutable (short form)

struct Person(Name string, Age int, var Score int)

val p = Person("Alice", 30, 100)
val older = p.Copy(Age = 31)     // new instance, original unchanged
p.Score = 200                     // OK - Score is var
// p.Name = "Bob"                 // compile error - Name is val
```

### Expression Functions

Single-expression functions skip the braces and `return`:

```gala
func square(x int) int = x * x
func max(a int, b int) int = if (a > b) a else b
func greet(name string) string = "Hello, " + name
```

### Type Inference for Lambdas

Lambda parameter types and method type parameters are inferred from context:

```gala
val opt = Some(42)
val doubled = opt.Map((x) => x * 2)         // x inferred as int
val positive = opt.Filter((x) => x > 0)     // x inferred as int
opt.ForEach((x) => { fmt.Println(x) })       // void closure

val list = ListOf(1, 2, 3)
val squares = list.Map((x) => x * x)        // no type annotations needed
```

### Error Handling with Try

`Try[T]` captures panics and enables railway-oriented programming:

```gala
// Try.Apply catches panics
val result = Try[int](() => riskyDivide(10, 0))  // Failure

// Railway-oriented chaining
val output = divide(10, 2)
    .Map((x) => x * 2)
    .FlatMap((x) => divide(x, 3))
    .Recover((e) => 0)

// Exhaustive pattern matching
val msg = result match {
    case Success(v) => fmt.Sprintf("got %d", v)
    case Failure(e) => "error: " + e.Error()
}
```

### Async Programming with Futures

```gala
import . "martianoff/gala/concurrent"

val f1 = FutureApply[int](() => expensiveComputation())
val f2 = FutureApply[string](() => fetchData())

val result = f1
    .Map((v) => v * 2)
    .FlatMap((v) => fetchName(v))

val combined = f1.Zip(f2)  // Future[Tuple[int, string]]
```

### Tuples

Concise syntax with destructuring and pattern matching (up to Tuple5):

```gala
val pair = (1, "hello")
val (x, y) = pair              // destructuring

val msg = pair match {
    case (n, s) => fmt.Sprintf("%d: %s", n, s)
    case _      => "unknown"
}

// Useful for map entries
val scores = HashMapOf[string, int](("alice", 100), ("bob", 85))
```

### Read-Only Pointers (ConstPtr)

`ConstPtr[T]` provides C++-style `const T*` semantics. Taking the address of a `val` returns a `ConstPtr`:

```gala
val data = 42
val ptr = &data      // ConstPtr[int], not *int

val value = *ptr     // OK: read
// *ptr = 100        // compile error: cannot write through ConstPtr

// Auto-deref field access
val alice = Person("Alice", 30)
val ref = &alice
fmt.Println(ref.Name)  // auto-derefs to alice.Name
```

### Partial Functions

```gala
// Returns Option automatically
val describe = { case 1 => "one" case 2 => "two" }
describe(1)  // Some("one")
describe(5)  // None[string]

// Filter + transform in one pass
val evens = ArrayOf(1, 2, 3, 4, 5).Collect({ case n if n % 2 == 0 => n * 2 })
// [4, 8]
```

### Custom Extractors

```gala
type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None[int]()

val desc = number match {
    case Even(n) => fmt.Sprintf("%d is even", n)
    case _       => "odd"
}
```

### Generic Methods

```gala
type Box[T any] struct { Value T }

func (b Box[T]) Map[U any](f func(T) U) Box[U] = Box[U](Value = f(b.Value))
func (b Box[T]) FlatMap[U any](f func(T) Box[U]) Box[U] = f(b.Value)

val intBox = Box[int](Value = 42)
val strBox = intBox.Map((x) => fmt.Sprintf("Value: %d", x))
```

---

## GALA vs Go

### Pattern Matching vs Switch

<table>
<tr><th>GALA</th><th>Go</th></tr>
<tr>
<td>

```gala
val msg = shape match {
    case Circle(r)      => fmt.Sprintf("r=%.1f", r)
    case Rectangle(w,h) => fmt.Sprintf("%fx%f", w, h)
    case Point()        => "point"
}
```

</td>
<td>

```go
var msg string
switch shape._variant {
case Shape_Circle:
    msg = fmt.Sprintf("r=%.1f", shape.Radius.Get())
case Shape_Rectangle:
    msg = fmt.Sprintf("%fx%f", shape.Width.Get(), shape.Height.Get())
case Shape_Point:
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
updated := Config{Host: config.Host, Port: 8080}
```

</td>
</tr>
</table>

---

## GALA vs Scala

GALA borrows many ideas from Scala - pattern matching, sealed types, `Option`/`Either`/`Try` monads, expression functions. But GALA targets the Go ecosystem, which means different trade-offs.

### Why GALA if you already know Scala?

| | Scala | GALA |
|---|---|---|
| **Runtime** | JVM (startup overhead, GC tuning) | Native binary, fast startup, small footprint |
| **Deployment** | JAR/fat JAR, requires JVM | Single static binary, cross-compiles to any OS |
| **Concurrency** | Akka, ZIO, Cats Effect | Goroutines + `Future[T]` (built on Go's lightweight threads) |
| **Interop** | Java ecosystem | Go ecosystem |
| **Learning curve** | Steep (implicits, macros, higher-kinded types) | Moderate (no implicits, no macros, no HKT) |
| **Build** | sbt (slow), Mill, Gradle | Bazel or `gala run` (fast) |
| **Type inference** | Global (Hindley-Milner + local) | Local + targeted HM for generics |

### Sealed Types

<table>
<tr><th>GALA</th><th>Scala 3</th></tr>
<tr>
<td>

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

val desc = shape match {
    case Circle(r)      => fmt.Sprintf("r=%.1f", r)
    case Rectangle(w,h) => fmt.Sprintf("%fx%f", w, h)
    case Point()        => "point"
}
```

</td>
<td>

```scala
enum Shape:
  case Circle(radius: Double)
  case Rectangle(width: Double, height: Double)
  case Point()

val desc = shape match
  case Circle(r)         => f"r=$r%.1f"
  case Rectangle(w, h)   => f"${w}x$h"
  case Point()           => "point"
```

</td>
</tr>
</table>

Nearly identical expressiveness. The key difference is what happens *after* compilation: GALA produces a native binary, Scala produces JVM bytecode.

### Option Handling

<table>
<tr><th>GALA</th><th>Scala</th></tr>
<tr>
<td>

```gala
val name = user.Name
    .Map((n) => strings.ToUpper(n))
    .GetOrElse("ANONYMOUS")
```

</td>
<td>

```scala
val name = user.name
    .map(_.toUpperCase)
    .getOrElse("ANONYMOUS")
```

</td>
</tr>
</table>

Scala has placeholder syntax (`_`) for terse lambdas. GALA requires explicit parameter names but infers their types from context.

### What GALA doesn't have (by design)

These Scala features are intentionally omitted to keep GALA simple and its output readable:

| Scala feature | GALA approach |
|---|---|
| Implicits / `given` / `using` | Explicit parameter passing |
| Higher-kinded types (`F[_]`) | Concrete generics only |
| Macros | No compile-time metaprogramming |
| Traits with implementations | Interfaces (Go-style, no default methods) |
| For-comprehensions | Method chaining (`.Map`, `.FlatMap`) |
| Companion objects | Auto-generated companions for sealed types + `Apply`/`Unapply` |
| Variance (`+T`, `-T`) | Invariant generics only |
| Lazy vals | Not supported |

### Native binaries vs JVM in containers

| | GALA (native) | Scala (JVM) |
|---|---|---|
| **Container image size** | ~10-20 MB (scratch/distroless) | ~200-400 MB (JRE + fat JAR) |
| **Cold start** | Milliseconds | Seconds (class loading, JIT warmup) |
| **Memory at idle** | ~5-10 MB | ~100-200 MB (JVM heap overhead) |
| **Scaling to zero** | Practical (fast restarts) | Painful (cold start penalty on every scale-up) |
| **Dockerfile** | `COPY binary /app` + `ENTRYPOINT` | JRE base image + JAR layering + JVM flags tuning |

In Kubernetes and serverless environments, these differences compound: faster autoscaling, lower resource requests, cheaper node packing, and simpler container pipelines.

### When to choose GALA over Scala

- You're building **microservices** where cold start, memory footprint, and container density matter
- You want **scale-to-zero** without paying seconds of JVM startup on every request
- You want **Scala-like expressiveness** without Scala's complexity budget
- You prefer **simple container pipelines** - one static binary, no JVM flags, no GC tuning

### When to choose Scala over GALA

- You need the **JVM ecosystem** (Spark, Kafka client, Akka, Play Framework)
- Your problem requires **higher-kinded types** or advanced type-level programming
- You need **mature IDE support** (IntelliJ Scala plugin is world-class)
- Your services are **long-running** and benefit from JIT optimization over time

---

## Standard Library

| Type | Description |
|------|-------------|
| `Option[T]` | Optional values - `Some(value)` / `None()` |
| `Either[A, B]` | Disjoint union - `Left(a)` / `Right(b)` |
| `Try[T]` | Failable computation - `Success(value)` / `Failure(err)` |
| `Future[T]` | Async computation with `Map`, `FlatMap`, `Zip`, `Await` |
| `Tuple[A, B]` | Pairs and triples with `(a, b)` syntax |
| `ConstPtr[T]` | Read-only pointer with auto-deref field access |

### Collections

| Type | Kind | Access | Best for |
|------|------|--------|----------|
| `List[T]` | Immutable | O(1) prepend, O(n) index | Recursive processing, prepend-heavy workloads |
| `Array[T]` | Immutable | O(1) random access | General-purpose indexed sequences |
| `HashMap[K,V]` | Immutable | O(1) lookup | Functional key-value storage |
| `HashSet[T]` | Immutable | O(1) membership | Unique element collections |
| `TreeSet[T]` | Immutable | O(log n) sorted | Ordered unique elements |

All collections support `Map`, `Filter`, `FoldLeft`, `ForEach`, `Exists`, `Find`, `Collect`, and more.

Mutable variants are available in `collection_mutable` for performance-sensitive code.

---

## Dependency Management

```bash
gala mod init github.com/user/project    # Initialize
gala mod add github.com/example/utils@v1.2.3   # Add GALA dep
gala mod add github.com/google/uuid@v1.6.0 --go  # Add Go dep
gala mod tidy                              # Sync imports
```

---

## Documentation

- [Language Specification](docs/GALA.MD) - Complete language reference
- [Examples](docs/EXAMPLES.MD) - Code examples for all features
- [Type Inference](docs/TYPE_INFERENCE.MD) - How type inference works
- [Concurrent](docs/CONCURRENT.MD) - Future, Promise, and ExecutionContext
- [Stream](docs/STREAM.MD) - Lazy, potentially infinite sequences
- [Immutable Collections](docs/IMMUTABLE_COLLECTIONS.MD) - List, Array, HashMap, HashSet, TreeSet
- [Mutable Collections](docs/MUTABLE_COLLECTIONS.MD) - Mutable variants for performance
- [String Utils](docs/STRING_UTILS.MD) - Rich string operations
- [Time Utils](docs/TIME_UTILS.MD) - Duration and Instant types
- [Dependency Management](docs/DEPENDENCY_MANAGEMENT.MD) - Module system

---

## IDE Support

### IntelliJ IDEA

```bash
bazel build //ide/intellij:plugin
# Install bazel-bin/ide/intellij/gala-intellij-plugin.zip via Settings > Plugins
```

Features: Syntax highlighting, code completion, brace matching, code folding.

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

### Build from Source

```bash
git clone https://github.com/martianoff/gala.git
cd gala
bazel build //cmd/gala:gala
```

### Using Bazel (Recommended)

```python
load("//:gala.bzl", "gala_binary", "gala_library")

gala_binary(
    name = "myapp",
    src = "main.gala",
)
```

---

## Project Structure

```
gala/
├── cmd/gala/              # Compiler CLI
├── internal/
│   ├── parser/            # ANTLR4 parser and grammar
│   └── transpiler/        # Go code generation
├── std/                   # Standard library (GALA)
├── collection_immutable/  # Immutable collections
├── collection_mutable/    # Mutable collections
├── concurrent/            # Future, Promise, ExecutionContext
├── stream/                # Lazy sequences
├── string_utils/          # Rich string operations
├── time_utils/            # Duration and Instant
├── test/                  # Test framework
├── examples/              # Example programs
└── docs/                  # Documentation
```

---

## Contributing

Contributions are welcome! Please ensure:

1. `bazel build //...` passes
2. `bazel test //...` passes
3. New features include examples in `examples/`
4. Documentation is updated for grammar/feature changes

---

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.
