# GALA

[![Release](https://github.com/martianoff/gala/actions/workflows/release.yml/badge.svg)](https://github.com/martianoff/gala/releases)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

**GALA** (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It combines Go's efficiency and simplicity with features inspired by Scala and other functional languages.

**Code is mostly AI-Generated**

## Features

- **Immutability by default** — Variables declared with `val` or `:=` are immutable
- **Pattern matching** — Powerful `match` expressions with extractors, guards, and sequence patterns
- **Expression-oriented** — `if` and `match` can be used as expressions
- **Concise syntax** — Expression functions, shorthand struct declarations, lambdas
- **Type inference** — Less boilerplate while maintaining type safety
- **Generics** — Full support for generic types and methods
- **Go interoperability** — Seamlessly use Go libraries and tools

## Quick Example

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

## Installation

### Pre-built Binaries

Download the latest release for your platform from the [Releases](https://github.com/martianoff/gala/releases) page.

| Platform | Binary |
|----------|--------|
| Linux (x64) | `gala-linux-amd64` |
| Linux (ARM64) | `gala-linux-arm64` |
| macOS (x64) | `gala-darwin-amd64` |
| macOS (Apple Silicon) | `gala-darwin-arm64` |
| Windows (x64) | `gala-windows-amd64.exe` |

### Build from Source

Requires [Bazel](https://bazel.build/) and Go 1.25+.

```bash
# Clone the repository
git clone https://github.com/martianoff/gala.git
cd gala

# Build
bazel build //cmd/gala:gala

# The binary is at bazel-bin/cmd/gala/gala_/gala
```

## Usage

```bash
# Transpile a GALA file to Go
gala -input main.gala -output main.go

# Transpile with search paths for imports
gala -input main.gala -output main.go -search ./lib,./vendor
```

## Compiling to Binaries

### Using the CLI

GALA transpiles to Go, so you compile in two steps:

```bash
# 1. Transpile GALA to Go
gala -input main.gala -output main.go

# 2. Compile Go to binary
go build -o myapp main.go
```

For projects using the standard library, ensure the `std` package is available:

```bash
# With Go modules
go build -o myapp main.go
```

### Using Bazel (Recommended)

For projects with multiple files or dependencies, use Bazel with the provided macros:

```python
# BUILD.bazel
load("//:gala.bzl", "gala_binary", "gala_library")

# Build a binary from a GALA file
gala_binary(
    name = "myapp",
    src = "main.gala",
)

# Build a library for use by other targets
gala_library(
    name = "mylib",
    src = "lib.gala",
    importpath = "example.com/myproject/mylib",
    visibility = ["//visibility:public"],
)
```

Then build and run:

```bash
# Build
bazel build //:myapp

# Run
bazel run //:myapp

# Or execute the binary directly
./bazel-bin/myapp_/myapp
```

## Language Highlights

### Immutable Variables

```gala
val x = 10          // Immutable
var y = 20          // Mutable
z := 30             // Immutable (short declaration)
```

### Expression Functions

```gala
func square(x int) int = x * x
func add(a int, b int) int = a + b
```

### Pattern Matching

```gala
val result = opt match {
    case Some(x) if x > 0 => "positive"
    case Some(_) => "non-positive"
    case None() => "empty"
    case _ => "unknown"
}
```

### Lambdas

```gala
val double = (x int) => x * 2
val numbers = list.Map((n int) => n * n)
```

### Generic Types

```gala
type Box[T any] struct { Value T }

func (b Box[T]) Map[U any](f func(T) U) Box[U] = Box[U](Value = f(b.Value))
```

## Standard Library

GALA includes a standard library with common functional types:

- `Option[T]` — Safe handling of optional values (`Some`, `None`)
- `Either[A, B]` — Represents one of two possible values (`Left`, `Right`)
- `Tuple[A, B]` — Pair of values
- Immutable collections (`List`, `Array`)

## Documentation

- [Language Specification](docs/GALA.md)
- [Examples](docs/examples.md)
- [Type Inference Rules](docs/TYPE_INFERENCE.md)

## IDE Support

### IntelliJ IDEA

A plugin providing syntax highlighting and file type recognition is available in `ide/intellij`.

```bash
bazel build //ide/intellij:plugin
# Install bazel-bin/ide/intellij/gala-intellij-plugin.zip via Settings > Plugins
```

## Project Structure

```
gala/
├── cmd/gala/           # Compiler CLI
├── internal/
│   ├── parser/         # ANTLR4 parser and grammar
│   └── transpiler/     # Go code generation
├── std/                # Standard library (GALA)
├── collection_immutable/ # Immutable collections
├── test/               # Test framework
├── examples/           # Example programs
└── docs/               # Documentation
```

## Contributing

Contributions are welcome! Please ensure:

1. `bazel build //...` passes
2. `bazel test //...` passes
3. New features include examples in `examples/`
4. Documentation is updated for grammar/feature changes

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.
