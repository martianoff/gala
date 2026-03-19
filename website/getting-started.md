---
layout: default
title: "Getting Started with GALA — Install and Write Your First Go Alternative Program"
description: "Install GALA and write your first program in minutes. Pre-built binaries for Linux, macOS, and Windows. Get sum types, pattern matching, and Option types for Go in 3 steps."
keywords: "install gala, gala getting started, gala tutorial, gala hello world, gala setup, gala quickstart, transpile gala to go, golang sum types install, go pattern matching setup"
permalink: /getting-started/
---

<div class="breadcrumb">
  <a href="{{ '/' | relative_url }}">Home</a> &raquo; Getting Started
</div>

# Getting Started with GALA

GALA transpiles to Go, so you get native binaries, full Go library access, and the entire Go toolchain. This guide takes you from installation to a running program in minutes.

---

## Installation

### Pre-built Binaries

Download the latest release for your platform from [GitHub Releases](https://github.com/martianoff/gala/releases):

| Platform | Binary |
|----------|--------|
| Linux (x64) | `gala-linux-amd64` |
| Linux (ARM64) | `gala-linux-arm64` |
| macOS (x64) | `gala-darwin-amd64` |
| macOS (Apple Silicon) | `gala-darwin-arm64` |
| Windows (x64) | `gala-windows-amd64.exe` |

After downloading, rename the binary to `gala` (or `gala.exe` on Windows) and add it to your `PATH`.

### Build from Source

If you prefer to build from source, you need [Git](https://git-scm.com/), [Go 1.22+](https://go.dev/dl/), and [Bazelisk](https://github.com/bazelbuild/bazelisk):

```bash
git clone https://github.com/martianoff/gala.git
cd gala
bazel build //cmd/gala:gala
```

The compiled binary will be at `bazel-bin/cmd/gala/gala_/gala`.

---

## Hello World

### 1. Write

Create a file called `main.gala`:

```gala
package main

func main() {
    Println("Hello, GALA!")
}
```

`Println` is a built-in function -- no imports needed.

### 2. Run

```bash
gala run main.gala
```

Output:

```
Hello, GALA!
```

That is it. GALA transpiles your code to Go, compiles it, and runs the binary.

---

## A Bigger Example

Here is a program that uses structs, pattern matching, and string interpolation:

```gala
package main

struct Person(Name string, Age int)

func greet(p Person) string = p match {
    case Person(name, age) if age < 18 => s"Hey, $name!"
    case Person(name, _)               => s"Hello, $name"
}

func main() {
    val people = SliceOf(
        Person("Alice", 25),
        Person("Bob", 15),
        Person("Charlie", 70),
    )

    for _, p := range people {
        Println(greet(p))
    }
}
```

Output:

```
Hello, Alice
Hey, Bob!
Hello, Charlie
```

Key features shown:

- **`struct Person(Name string, Age int)`** -- immutable struct with auto-generated `Copy()` and `Equal()`
- **`p match { ... }`** -- pattern matching with destructuring and guard conditions
- **`s"Hey, $name!"`** -- string interpolation with auto-inferred format verbs
- **Expression function** -- `func greet(...) string = ...` skips braces and `return`

---

## Project Setup

### Initialize a Module

For anything beyond a single file, initialize a GALA module:

```bash
mkdir myproject && cd myproject
gala mod init github.com/user/myproject
```

This creates a `gala.mod` file that tracks your module path and dependencies.

### Directory Structure

A typical GALA project looks like this:

```
myproject/
  gala.mod              # Module manifest
  gala.sum              # Dependency checksums (auto-generated)
  main.gala             # Entry point
  handler/
    handler.gala        # Library package
  model/
    user.gala           # Another package
    order.gala          # Multiple files per package
```

### Multi-File Packages

GALA packages can span multiple `.gala` files, just like Go packages. All files in a directory share the same package name. Types, functions, and methods defined in one file are visible to other files in the same package.

---

## Building and Running

### Without Bazel (Simple Projects)

For simple projects, use `gala build` and `gala run` directly:

```bash
# Build to a binary
gala build

# Build and run
gala run

# Build with verbose output
gala build -v
```

`gala build` creates a clean build workspace under `~/.gala/build/`, transpiles your GALA code to Go, and compiles it. Your project directory stays clean -- no generated files.

### With Bazel (Recommended for Larger Projects)

For larger projects, Bazel provides incremental builds, dependency management, and test orchestration. GALA provides three Bazel rules:

**`gala_binary`** -- builds an executable:

```python
load("//:gala.bzl", "gala_binary")

gala_binary(
    name = "myapp",
    src = "main.gala",
)
```

**`gala_library`** -- builds a reusable package:

```python
load("//:gala.bzl", "gala_library")

gala_library(
    name = "handler",
    src = "handler.gala",
    importpath = "github.com/user/myproject/handler",
)
```

**`gala_go_test`** -- builds and runs tests:

```python
load("//:gala.bzl", "gala_go_test")

gala_go_test(
    name = "handler_test",
    src = "handler_test.gala",
)
```

Build and test with:

```bash
bazel build //...
bazel test //...
bazel run //myapp:myapp
```

---

## Adding Dependencies

### Go Packages

Use any Go library by adding it with the `--go` flag:

```bash
gala mod add github.com/google/uuid@v1.6.0 --go
```

Then import and use it in your GALA code:

```gala
package main

import "github.com/google/uuid"

func GenerateID() string = uuid.New().String()

func main() {
    Println(s"ID: ${GenerateID()}")
}
```

### GALA Packages

Add GALA library dependencies without the `--go` flag:

```bash
gala mod add github.com/example/gala-utils@v1.2.3
```

GALA dependencies are automatically transpiled at build time -- no pre-compiled `.go` files needed.

### Dependency Commands

```bash
gala mod add <package>@<version>        # Add a dependency
gala mod add <package>@<version> --go   # Add a Go dependency
gala mod remove <package>               # Remove a dependency
gala mod update                         # Update all dependencies
gala mod tidy                           # Sync gala.mod with imports
gala mod graph                          # Print dependency tree
gala mod verify                         # Verify checksums
```

For Bazel projects, run `gala mod tidy` to generate the `go.mod` and `go.sum` files that Bazel needs.

See the [Dependency Management documentation](https://github.com/martianoff/gala/blob/master/docs/DEPENDENCY_MANAGEMENT.MD) for full details.

---

## IDE Support

### IntelliJ IDEA

GALA provides an IntelliJ plugin with syntax highlighting, code completion, brace matching, and code folding.

Build and install:

```bash
bazel build //ide/intellij:plugin
```

Then install `bazel-bin/ide/intellij/gala-intellij-plugin.zip` via **Settings > Plugins > Install Plugin from Disk**.

---

## Next Steps

You are set up and running. Here is where to go from here:

- **[Playground]({{ '/playground/' | relative_url }})** -- Try GALA in your browser without installing anything
- **[GALA vs Go]({{ '/vs-go/' | relative_url }})** -- Side-by-side comparison of GALA and Go code
- **[Language Specification](https://github.com/martianoff/gala/blob/master/docs/GALA.MD)** -- Complete reference for GALA syntax and semantics
- **[Examples](https://github.com/martianoff/gala/blob/master/docs/EXAMPLES.MD)** -- Code examples for all language features
- **[Sealed Types]({{ '/features/sealed-types/' | relative_url }})** -- Algebraic data types and exhaustive matching
- **[Collections]({{ '/features/collections/' | relative_url }})** -- Immutable functional collections
- **[Concurrency]({{ '/features/concurrency/' | relative_url }})** -- `Future[T]`, `Promise[T]`, and `ExecutionContext`

### Showcase Projects

| Project | Description |
|---------|-------------|
| [GALA Playground](https://github.com/martianoff/gala-playground) | Web-based playground -- [try it live](https://gala-playground.fly.dev) |
| [State Machine Example](https://github.com/martianoff/gala-state-machine-example) | State machines with sealed types and pattern matching |
| [Log Analyzer](https://github.com/martianoff/gala-log-analyzer) | Structured log parsing with Go stdlib interop and functional pipelines |
| [GALA Server](https://github.com/martianoff/gala-server) | Immutable HTTP server library with builder-pattern configuration |
