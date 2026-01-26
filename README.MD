# GALA

[![Release](https://github.com/martianoff/gala/actions/workflows/release.yml/badge.svg)](https://github.com/martianoff/gala/releases)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

**GALA** (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It combines Go's efficiency and simplicity with features inspired by Scala and other functional languages.

**Code is mostly AI-Generated**

---

## Table of Contents

- [Features](#features)
- [Quick Start](#quick-start)
- [Feature Showcase](#feature-showcase)
- [Installation](#installation)
- [Standard Library](#standard-library)
- [Documentation](#documentation)
- [IDE Support](#ide-support)
- [Project Structure](#project-structure)
- [Contributing](#contributing)

---

## Features

| Feature | Description |
|---------|-------------|
| **Immutability by default** | Variables declared with `val` or `:=` are immutable |
| **Pattern matching** | Powerful `match` expressions with extractors, guards, and sequence patterns |
| **Expression-oriented** | `if` and `match` can be used as expressions |
| **Concise syntax** | Expression functions, shorthand structs, lambdas |
| **Type inference** | Less boilerplate while maintaining type safety |
| **Generics** | Full support for generic types and methods |
| **Functional collections** | Immutable and mutable collections with rich APIs |
| **Monads** | `Option`, `Either`, `Try`, `Future` for safe programming |
| **Go interoperability** | Seamlessly use Go libraries and tools |

---

## Quick Start

```gala
package main

import "fmt"

struct Person(Name string, Age int)

func greet(p Person) string = p match {
    case Person(name, age) if age < 18 => "Hello, young " + name
    case Person(name, _) => "Hello, " + name
    case _ => "Hello"
}

func main() {
    val alice = Person("Alice", 25)
    fmt.Println(greet(alice))
}
```

---

## Feature Showcase

### Immutable by Default

```gala
val x = 10           // Immutable - cannot be reassigned
var y = 20           // Mutable - can be reassigned
z := 30              // Short declaration - immutable

val person = Person("Alice", 30)
val older = person.Copy(Age = 31)  // Creates new instance, original unchanged
```

### Expression Functions

```gala
// Single-expression functions with = syntax
func square(x int) int = x * x
func max(a int, b int) int = if (a > b) a else b
func greet(name string) string = "Hello, " + name
```

### Pattern Matching with Extractors

```gala
// Option handling
val result = maybeValue match {
    case Some(x) if x > 0 => "positive: " + fmt.Sprint(x)
    case Some(x)          => "non-positive: " + fmt.Sprint(x)
    case None()           => "no value"
    case _                => "unknown"
}

// Struct deconstruction
val msg = person match {
    case Person(name, age) if age >= 18 => name + " is an adult"
    case Person(name, _)                => name + " is a minor"
    case _                              => "unknown"
}

// Custom extractors
type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None[int]()

val description = number match {
    case Even(n) => fmt.Sprintf("%d is even", n)
    case _       => "odd"
}
```

### Sequence Pattern Matching

```gala
import . "martianoff/gala/collection_immutable"

val list = ListOf(1, 2, 3, 4, 5)

val result = list match {
    case List(first, second, rest...) => fmt.Sprintf("First: %d, Second: %d, Rest: %d items", first, second, rest.Size())
    case List(only)                   => fmt.Sprintf("Single element: %d", only)
    case List()                       => "Empty list"
    case _                            => "Unknown"
}
```

### Functional Collections

```gala
import . "martianoff/gala/collection_immutable"

// Immutable list operations
val numbers = ListOf(1, 2, 3, 4, 5)
val doubled = numbers.Map[int]((x int) => x * 2)           // List(2, 4, 6, 8, 10)
val evens = numbers.Filter((x int) => x % 2 == 0)          // List(2, 4)
val sum = numbers.FoldLeft[int](0, (acc int, x int) => acc + x)  // 15

// Chained operations
val result = ListOf(1, 2, 3, 4, 5)
    .Filter((x int) => x % 2 == 1)
    .Map[int]((x int) => x * x)
    .FoldLeft[int](0, (acc int, x int) => acc + x)  // 1 + 9 + 25 = 35

// Immutable HashMap
val scores = HashMapOf[string, int](("alice", 100), ("bob", 85))
val updated = scores.Put("charlie", 90)  // Original unchanged
```

### Error Handling with Try

```gala
import . "martianoff/gala/std"

func divide(a int, b int) Try[int] =
    if (b == 0) Failure[int](DivisionByZeroError())
    else Success(a / b)

// Railway-oriented programming
val result = divide(10, 2)
    .Map[int]((x int) => x * 2)
    .FlatMap[int]((x int) => divide(x, 2))
    .GetOrElse(0)

// Pattern matching on Try
val msg = divide(10, 0) match {
    case Success(v) => fmt.Sprintf("Result: %d", v)
    case Failure(e) => fmt.Sprintf("Error: %s", e.Error())
    case _          => "Unknown"
}
```

### Async Programming with Futures

```gala
import . "martianoff/gala/concurrent"

// Create async computations
val f1 = FutureApply[int](() => expensiveComputation())
val f2 = FutureApply[string](() => fetchData())

// Chain operations
val result = f1
    .Map[int]((v int) => v * 2)
    .FlatMap[string]((v int) => fetchName(v))

// Combine futures
val combined = f1.Zip[string](f2)  // Future[Tuple[int, string]]

// Pattern matching on completed futures
val msg = future match {
    case Succeeded(v) => fmt.Sprintf("Got: %v", v)
    case Failed(e)    => fmt.Sprintf("Error: %s", e.Error())
    case _            => "Unknown"
}
```

### Tuple Syntax

```gala
// Concise tuple creation
val pair = (1, "hello")
val triple = (1, "hello", true)

// Pattern matching
val (x, y) = pair
val msg = triple match {
    case (n, s, true)  => fmt.Sprintf("%d: %s (active)", n, s)
    case (n, s, false) => fmt.Sprintf("%d: %s (inactive)", n, s)
    case _             => "unknown"
}
```

### Generic Methods

```gala
type Box[T any] struct { Value T }

func (b Box[T]) Map[U any](f func(T) U) Box[U] = Box[U](Value = f(b.Value))
func (b Box[T]) FlatMap[U any](f func(T) Box[U]) Box[U] = f(b.Value)

val intBox = Box[int](Value = 42)
val strBox = intBox.Map[string]((x int) => fmt.Sprintf("Value: %d", x))
```

### Partial Functions

```gala
// Partial function syntax - returns Option automatically
val describe = { case 1 => "one" case 2 => "two" }

describe(1)  // Some("one")
describe(5)  // None[string]

// Use with Collect for filter+map in one operation
val numbers = ArrayOf(1, 2, 3, 4, 5)
val result = numbers.Collect({ case n if n % 2 == 0 => n * 2 })  // Array(4, 8)
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

### Build from Source

```bash
git clone https://github.com/martianoff/gala.git
cd gala
bazel build //cmd/gala:gala
```

### Usage

```bash
# Transpile GALA to Go
gala -input main.gala -output main.go

# Compile with Go
go build -o myapp main.go
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

## Standard Library

| Type | Description |
|------|-------------|
| `Option[T]` | Safe handling of optional values (`Some`, `None`) |
| `Either[A, B]` | Represents one of two possible values (`Left`, `Right`) |
| `Try[T]` | Computation that may succeed or fail (`Success`, `Failure`) |
| `Future[T]` | Asynchronous computation with callbacks and combinators |
| `Tuple[A, B]` | Pair of values with pattern matching support |
| `List[T]` | Immutable linked list with O(1) prepend |
| `Array[T]` | Immutable indexed sequence with O(1) random access |
| `HashMap[K, V]` | Immutable hash map with O(1) operations |
| `HashSet[T]` | Immutable hash set with O(1) operations |
| `TreeSet[T]` | Immutable sorted set with O(log n) operations |

---

## Documentation

- [Language Specification](docs/GALA.MD) - Complete language reference
- [Examples](docs/EXAMPLES.MD) - Code examples for all features
- [Type Inference](docs/TYPE_INFERENCE.MD) - How type inference works
- [Immutable Collections](docs/IMMUTABLE_COLLECTIONS.MD) - List, Array, HashMap, HashSet, TreeSet
- [Mutable Collections](docs/MUTABLE_COLLECTIONS.MD) - Mutable variants for performance

---

## IDE Support

### IntelliJ IDEA

```bash
bazel build //ide/intellij:plugin
# Install bazel-bin/ide/intellij/gala-intellij-plugin.zip via Settings > Plugins
```

Features: Syntax highlighting, code completion, brace matching, code folding.

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
├── concurrent/            # Future and Promise
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
