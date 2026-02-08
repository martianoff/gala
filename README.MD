# GALA

[![Build](https://github.com/martianoff/gala/actions/workflows/test.yml/badge.svg)](https://github.com/martianoff/gala/actions/workflows/test.yml)
[![Release](https://github.com/martianoff/gala/actions/workflows/release.yml/badge.svg)](https://github.com/martianoff/gala/releases)
[![GitHub release (latest SemVer)](https://img.shields.io/github/v/release/martianoff/gala?sort=semver)](https://github.com/martianoff/gala/releases/latest)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Bazel](https://img.shields.io/badge/Built%20with-Bazel-43A047)](https://bazel.build)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

**GALA** (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It brings sealed types, pattern matching, immutability by default, and functional collections to the Go ecosystem -- without sacrificing Go's performance, tooling, or library compatibility.

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
}

func area(s Shape) string = s match {
    case Circle(r)       => fmt.Sprintf("circle area: %.2f", 3.14159 * r * r)
    case Rectangle(w, h) => fmt.Sprintf("rect area: %.2f", w * h)
}
```

---

## What is GALA?

GALA transpiles to clean, readable Go code. You get Scala-like expressiveness -- sealed types, exhaustive pattern matching, `Option[T]`/`Either[A,B]`/`Try[T]` monads, immutable collections -- and it compiles to a single native binary. Every Go library works out of the box.

---

## Key Features

**Sealed types and exhaustive pattern matching** -- Define closed type hierarchies. The compiler rejects incomplete matches.

```gala
sealed type Result[T any] {
    case Ok(Value T)
    case Err(Message string)
}

val msg = result match {
    case Ok(v)    => fmt.Sprintf("got %d", v)
    case Err(msg) => "error: " + msg
}
```

**Immutability by default** -- `val` and `:=` are immutable. Structs auto-generate `Copy()` and `Equal()`.

```gala
struct Config(Host string, Port int)
val updated = config.Copy(Port = 8080)
```

**Expression functions** -- Single-expression functions skip braces and `return`.

```gala
func square(x int) int = x * x
func max(a int, b int) int = if (a > b) a else b
```

**Lambda type inference** -- Parameter types and method type parameters are inferred from context.

```gala
val list = ListOf(1, 2, 3)
val doubled = list.Map((x) => x * 2)
val sum = list.FoldLeft(0, (acc, x) => acc + x)
```

**Monads** -- `Option[T]`, `Either[A,B]`, `Try[T]`, `Future[T]` with `Map`, `FlatMap`, `Recover`, and pattern matching.

```gala
val name = user.Name
    .Map((n) => strings.ToUpper(n))
    .GetOrElse("ANONYMOUS")
```

**Functional collections** -- Immutable `List`, `Array`, `HashMap`, `HashSet`, `TreeSet`, `TreeMap` with `Map`, `Filter`, `FoldLeft`, `Collect`, and more.

```gala
val nums = ArrayOf(1, 2, 3, 4, 5)
val evens = nums.Filter((x) => x % 2 == 0)
val evenDoubled = nums.Collect({ case n if n % 2 == 0 => n * 2 })
```

**Tuples with destructuring** -- Up to Tuple5, with pattern matching and concise syntax.

```gala
val pair = (1, "hello")
val (x, y) = pair
```

**Read-only pointers** -- `ConstPtr[T]` prevents accidental mutation through pointers.

```gala
val data = 42
val ptr = &data       // ConstPtr[int], not *int
val value = *ptr      // OK: read
// *ptr = 100         // compile error: cannot write through ConstPtr
```

**Full Go interop** -- Use any Go library.

---

## Quick Start

### 1. Install

Download a pre-built binary from [Releases](https://github.com/martianoff/gala/releases), or build from source:

```bash
git clone https://github.com/martianoff/gala.git && cd gala
bazel build //cmd/gala:gala
```

### 2. Write

```gala
package main

import "fmt"

struct Person(Name string, Age int)

func greet(p Person) string = p match {
    case Person(name, age) if age < 18 => "Hey, " + name + "!"
    case Person(name, _)               => "Hello, " + name
}

func main() {
    fmt.Println(greet(Person("Alice", 25)))
}
```

### 3. Run

```bash
gala run main.gala

# Or with Bazel (recommended for projects)
bazel run //myapp:myapp
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
if err != nil {
    result = 0
} else {
    result = result * 2
    result, err = divide(result, 3)
    if err != nil {
        result = 0
    }
}
```

</td>
</tr>
</table>

---

## Standard Library

### Types

| Type | Description |
|------|-------------|
| `Option[T]` | Optional values -- `Some(value)` / `None()` |
| `Either[A, B]` | Disjoint union -- `Left(a)` / `Right(b)` |
| `Try[T]` | Failable computation -- `Success(value)` / `Failure(err)` |
| `Future[T]` | Async computation with `Map`, `FlatMap`, `Zip`, `Await` |
| `Tuple[A, B]` | Pairs and triples with `(a, b)` syntax |
| `ConstPtr[T]` | Read-only pointer with auto-deref field access |

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

## Dependency Management

```bash
gala mod init github.com/user/project
gala mod add github.com/example/utils@v1.2.3
gala mod add github.com/google/uuid@v1.6.0 --go
gala mod tidy
```

---

## IDE Support

### IntelliJ IDEA

```bash
bazel build //ide/intellij:plugin
# Install bazel-bin/ide/intellij/gala-intellij-plugin.zip via Settings > Plugins
```

Features: syntax highlighting, code completion, brace matching, code folding.

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

## Documentation

- [Language Specification](docs/GALA.MD) -- Complete language reference
- [Why GALA?](docs/WHY_GALA.MD) -- Feature deep-dive and honest trade-offs
- [Examples](docs/EXAMPLES.MD) -- Code examples for all features
- [Type Inference](docs/TYPE_INFERENCE.MD) -- How type inference works
- [Concurrent](docs/CONCURRENT.MD) -- Future, Promise, and ExecutionContext
- [Stream](docs/STREAM.MD) -- Lazy, potentially infinite sequences
- [Immutable Collections](docs/IMMUTABLE_COLLECTIONS.MD) -- List, Array, HashMap, HashSet, TreeSet, TreeMap
- [Mutable Collections](docs/MUTABLE_COLLECTIONS.MD) -- Mutable variants for performance
- [String Utils](docs/STRING_UTILS.MD) -- Rich string operations
- [Time Utils](docs/TIME_UTILS.MD) -- Duration and Instant types
- [Dependency Management](docs/DEPENDENCY_MANAGEMENT.MD) -- Module system

---

## Contributing

Contributions are welcome. Please ensure:

1. `bazel build //...` passes
2. `bazel test //...` passes
3. New features include examples in `examples/`
4. Documentation is updated for grammar/feature changes

---

## License

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Apache License 2.0. See [LICENSE](LICENSE) for details.
