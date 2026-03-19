---
layout: default
title: "GALA Language Reference - Complete Specification"
description: "Complete GALA language specification. Variables, functions, structs, sealed types, pattern matching, generics, lambdas, interfaces, control flow, and standard library types — the full reference for the Go alternative language."
keywords: "gala language reference, gala specification, gala syntax, gala language guide, go alternative language reference, gala documentation"
permalink: /docs/language-reference/
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/language-reference/">Docs</a> / Language Reference</p>

{% raw %}

**Related pages:** [Why GALA?](/docs/why-gala/) | [Code Examples](/docs/examples/) | [Immutable Collections](/docs/immutable-collections/) | [Mutable Collections](/docs/mutable-collections/) | [Streams](/docs/streams/) | [String Utilities](/docs/string-utils/) | [Time Utilities](/docs/time-utils/) | [Dependency Management](/docs/dependency-management/)

# GALA Language Specification

GALA (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It combines Go's efficiency and simplicity with features inspired by Scala and other functional languages, such as immutability by default, pattern matching, and concise expression syntax.

---

## Table of Contents

1. [Project Structure](#1-project-structure)
2. [Variable Declarations](#2-variable-declarations)
3. [Functions](#3-functions)
4. [Types and Structs](#4-types-and-structs)
5. [Interfaces](#5-interfaces)
6. [Control Flow](#6-control-flow)
7. [Functional Features](#7-functional-features)
8. [Generics](#8-generics)
9. [Standard Library Types](#9-standard-library-types)
10. [Literals and Type Conversions](#10-literals-and-type-conversions)
11. [Go Built-in Functions](#11-go-built-in-functions)
12. [Immutability Under the Hood](#12-immutability-under-the-hood)
13. [GALA Packages](#13-gala-packages)
14. [Embedding Files](#14-embedding-files)
15. [Testing](#15-testing)
16. [Best Practices](#16-best-practices)
17. [Dependency Management](#17-dependency-management)
18. [Further Reading](#18-further-reading)
19. [IDE Support](#19-ide-support)

> **Note:** This is the complete language specification. For the full, continuously updated source, see the [GALA.MD file on GitHub](https://github.com/martianoff/gala/blob/master/docs/GALA.MD). The content below is a faithful copy of that specification.

---

## 1. Project Structure

GALA files use the `.gala` extension. Every file must start with a package declaration, followed by an empty line. All GALA files in the same directory must belong to the same package.

GALA supports Go-style imports, including aliases and dot imports. Import declarations must also be followed by an empty line.

```gala
package main

import (
    "fmt"
    m "math"
    . "net/http"
)
```

### Multi-File Packages

A GALA package can span multiple `.gala` files. Types, sealed types, and functions defined in one file are available in all other files of the same package. Each file must declare the same package name.

```
shapes/
  types.gala      # struct Point, sealed type Shape
  ops.gala        # func (p Point) String(), func Describe(s Shape)
```

In Bazel, list all source files in `srcs`:

```python
gala_library(
    name = "shapes",
    srcs = ["shapes/types.gala", "shapes/ops.gala"],
    importpath = "myapp/shapes",
)

gala_binary(
    name = "app",
    srcs = ["main.gala", "helpers.gala"],
)
```

## 2. Variable Declarations

GALA distinguishes between immutable and mutable variables.

### Immutable (`val`)
Variables declared with `val` are immutable. They must be initialized at declaration time.

```gala
val x = 10
val a, b = 1, 2
// x = 20 // Compile error: cannot assign to immutable variable
```

### Mutable (`var`)
Variables declared with `var` are mutable and can be reassigned.

```gala
var y = 20
y = 30 // OK
```

### Short Variable Declaration
Inside functions, `:=` declares **immutable** variables.

```gala
func main() {
    z := 40
    // z = 50 // Compile error: cannot assign to immutable variable
}
```

## 3. Functions

GALA supports both Go-style block functions and Scala-style expression functions.

### Block Functions
```gala
func add(a int, b int) int {
    return a + b
}
```

### Expression Functions
```gala
func square(x int) int = x * x
```

### Parameters
Function parameters can be marked as `val` or `var`. By default, they are `val` (immutable).

```gala
func process(val data string, var count int) {
    // data = "new" // Error
    count = count + 1 // OK
}
```

### Higher-Order Functions
Functions are first-class values:

```gala
func apply(x int, f func(int) int) int = f(x)

func main() {
    val double = (x int) => x * 2
    val result = apply(5, double) // result = 10
}
```

### Variadic Functions
```gala
func sum(numbers ...int) int {
    var total = 0
    var i = 0
    for ; i < len(numbers) ; {
        total = total + numbers[i]
        i = i + 1
    }
    return total
}
```

### Methods
```gala
type Box[T any] struct { Value T }

func (b Box[T]) GetValue() T = b.Value
func (b Box[T]) Transform[U any](f func(T) U) Box[U] = Box[U](Value = f(b.Value))
```

## 4. Types and Structs

### Shorthand Struct Declaration
```gala
struct Person(name string, age int, var score int)
```

### Block Struct Declaration
```gala
type Person struct {
    Name string    // Immutable
    age  int       // Immutable
    var Score int  // Mutable
}
```

### Struct Construction
```gala
val p1 = Person{Name: "Alice", Age: 30}   // Named fields (Go-style)
val p2 = Person("Bob", 25)                 // Positional (Functional-style)
val p3 = Person(age = 20, name = "Charlie") // Named arguments
```

### Automatic Copy and Equal Methods
```gala
val p1 = Person("Alice", 30)
val p2 = p1.Copy(age = 31) // p2 is Person("Alice", 31)
val same = p1.Equal(p2)    // false
```

### Sealed Types (Algebraic Data Types)

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

val c = Circle(3.14)
val desc = c match {
    case Circle(radius) => fmt.Sprintf("radius=%.2f", radius)
    case Rectangle(w, h) => fmt.Sprintf("%fx%f", w, h)
    case Point() => "point"
}
```

Generic sealed types:
```gala
sealed type Result[T any] {
    case Ok(Value T)
    case Err(Error error)
}
```

## 5. Interfaces

```gala
type Shaper interface {
    Area() float64
}

struct Rect(width float64, height float64)
func (r Rect) Area() float64 = r.width * r.height
```

## 6. Control Flow

### If Statement and Expression
```gala
val status = if (score > 50) "pass" else "fail"
```

### Match Expression
```gala
val result = x match {
    case 1 => "one"
    case 2 => "two"
    case n => "Value is " + fmt.Sprintf("%d", n)
    case _ => "other"
}

// Boolean exhaustive match
val desc = flag match {
    case true  => "enabled"
    case false => "disabled"
}
```

#### Type-Based Pattern Matching
```gala
val res = x match {
    case s: string => "Found string: " + s
    case i: int    => "Found int: " + fmt.Sprintf("%d", i)
    case _         => "Unknown type"
}
```

#### Extractors and Unapply
```gala
type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None[int]()

42 match {
    case Even(n) => s"$n is even"
    case _       => "odd"
}
```

#### Pattern Matching Filters (Guards)
```gala
val res = x match {
    case i: int if i > 100 => "Large integer"
    case Person(name, age) if age < 18 => name + " is a minor"
    case _ => "Other"
}
```

#### Sequence Pattern Matching
```gala
val arr = ArrayOf(1, 2, 3, 4, 5)
val res = arr match {
    case Array(head, tail...) => fmt.Sprintf("Head: %d, Tail size: %d", head, tail.Size())
    case _ => "Empty"
}
```

### For Statement
```gala
for i := 0; i < 10; i++ {
    fmt.Println(i)
}

for count < 5 {
    count++
}

for _, v := range items {
    fmt.Println(v)
}
```

## 7. Functional Features

### Lambda Expressions
```gala
val f = (x int) => x * x

// Inferred parameter types
val doubled = opt.Map((x) => x * 2)

// Void closures
opt.ForEach((x) => { fmt.Println(x) })
```

### Partial Function Literals
```gala
val pf = { case 1 => "one" case 2 => "two" }
val r1 = pf(1)  // Some("one")
val r2 = pf(5)  // None[string]

// Use with Collect
val evenDoubled = numbers.Collect({ case n if n % 2 == 0 => n * 2 })
```

## 8. Generics

```gala
func identity[T any](x T) T = x

type Box[T any] struct { Value T }
```

## 9. Standard Library Types

### Option Monad
```gala
val x = Some(10)
val y = None[int]()

val msg = x match {
    case Some(v) => s"Got: $v"
    case None()  => "Empty"
}

val result = x.Map((i) => i * 2)
val value = x.GetOrElse(0)
```

### Tuple
```gala
val t = (1, "hello")
val (a, b) = t         // a = 1, b = "hello"
```

### Either
```gala
val e = Right[int, string]("success")
val msg = e match {
    case Left(code) => s"Error code: $code"
    case Right(s)   => "Result: " + s
}
```

### Try Monad
```gala
val result = Try(() => riskyDivide(10, 0))
val dir = Try(os.TempDir)  // function reference sugar

val msg = result match {
    case Success(n) => s"Got: $n"
    case Failure(e) => s"Error: ${e.Error()}"
}
```

### Future Monad
```gala
import . "martianoff/gala/concurrent"

val async = FutureApply[int](() => expensiveComputation())
val result = async.Await()           // Returns Try[int]
val doubled = async.Map((v) => v * 2)
```

### Slices and Maps (Go Interop)
**Prefer GALA collections** over Go slices for most use cases. See [Immutable Collections](/docs/immutable-collections/).

```gala
// PREFERRED: GALA collections
val nums = ArrayOf(1, 2, 3, 4, 5)
val doubled = nums.Map((x) => x * 2)

// GO INTEROP: When you need []T
val goSlice = SliceOf(1, 2, 3)
```

## 10. Literals and Type Conversions

### String Interpolation
```gala
val name = "Alice"
val age = 30

Println(s"Hello $name!")                    // Hello Alice!
Println(s"$name is $age years old")         // Alice is 30 years old
Println(s"Next year: ${age + 1}")           // Next year: 31
Println(s"Price: $$99")                     // Price: $99

// Explicit format specs
Println(f"$count%04d items")                // 0007 items
Println(f"Total: $$$price%.2f")             // Total: $19.99
```

### Rune Literals
```gala
val asterisk = '*'
val space = ' '
val newline = '\n'
```

### Type Conversions
```gala
val n = int64(42)
val f = float64(10)
val r = rune(65)            // 'A'
val s = string(r)           // "A"
```

## 11. Go Built-in Functions

Go's built-in functions are available: `len`, `cap`, `make`, `new`, `append`, `delete`, `close`, `panic`, `recover`.

For most use cases, prefer GALA's collection types and their methods over Go built-ins.

## 12. Immutability Under the Hood

### Pointer Types and Immutability
```gala
var data = 42
val ptr1 = &data     // immutable pointer binding
*ptr1 = 100          // OK: can modify data through pointer
// ptr1 = &other     // ERROR: cannot reassign immutable pointer
```

### ConstPtr - Read-Only Pointers
```gala
val data = 42
val ptr = &data  // ptr is ConstPtr[int]
val value = *ptr // OK: read
// *ptr = 100    // ERROR: cannot write through ConstPtr
```

## 13. GALA Packages

GALA supports Go-style imports with aliases and dot imports.

```gala
import m "math"
import . "martianoff/gala/examples/mathlib"

func main() {
    val res = m.Sqrt(16.0)
    val sum = Add(10, 20) // from mathlib via dot import
}
```

See [Dependency Management](/docs/dependency-management/) for managing external packages.

## 14. Embedding Files

```gala
embed val readme = "README.md"              // string
embed val static EmbeddedFS = "static/*"    // EmbeddedFS

val content = static.ReadString("static/index.html")
```

## 15. Testing

GALA provides a test framework with 22 assertions, panic recovery, timing, table-driven test support, and benchmarking.

```gala
package main

import . "martianoff/gala/test"

func TestAddition(t T) T {
    val x = 1 + 1
    return Eq(t, x, 2)
}
```

### Table-Driven Tests
```gala
func TestDouble(t T) T {
    return RunCases[int, int](t,
        (sub T, input int, expected int) => Eq(sub, input * 2, expected),
        Case[int, int](Name = "zero", Input = 0, Expected = 0),
        Case[int, int](Name = "positive", Input = 5, Expected = 10),
    )
}
```

### Benchmarking
```gala
func main() {
    RunBenchmarks(
        BenchFunc(Name = "BenchmarkAdd", Func = (b B) => {
            for i := 0; i < b.N; i++ {
                val x = 1 + 1
            }
        }),
    )
}
```

## 16. Best Practices

- **Prefer `val` over `var`** - Use mutable variables only when necessary
- **Use `Copy()` for updates** - `person.Copy(age = 31)`
- **Prefer `match` over if-else chains** for type/value dispatch
- **Use extractors** - `case Some(x) =>` not `if opt.IsDefined() { x := opt.Get() }`
- **Omit type parameters when inferrable** - `Some(42)` not `Some[int](42)`
- **Prefer `s"..."` over `fmt.Sprintf`** - `s"Hello $name"` not `fmt.Sprintf("Hello %s", name)`
- **Prefer GALA collections over Go slices** - Use `Array` or `List` from `collection_immutable`
- **Use `Option[T]`** for nullable values, **`Try[T]`** for operations that may fail

## 17. Dependency Management

GALA provides a module system similar to Go modules. See [Dependency Management](/docs/dependency-management/) for the full guide.

```bash
gala mod init github.com/user/project
gala mod add github.com/example/utils@v1.2.3
gala mod add github.com/google/uuid@v1.6.0 --go
gala mod tidy
```

## 18. Further Reading

- [Code Examples](/docs/examples/) - More examples of GALA code
- [Streams](/docs/streams/) - Lazy, potentially infinite sequences
- [String Utilities](/docs/string-utils/) - Rich, immutable string operations
- [Time Utilities](/docs/time-utils/) - Duration and Instant types
- [Immutable Collections](/docs/immutable-collections/) - Array, List, HashMap, HashSet, TreeSet, TreeMap
- [Mutable Collections](/docs/mutable-collections/) - Mutable collection types
- [Why GALA?](/docs/why-gala/) - Features, trade-offs, and honest assessment
- [Dependency Management](/docs/dependency-management/) - Module system and package management

## 19. IDE Support

### IntelliJ IDEA
A basic IntelliJ IDEA plugin is available in `ide/intellij`.

1. Build: `bazel build //ide/intellij:plugin`
2. Install: Settings > Plugins > Install Plugin from Disk...

The plugin provides syntax highlighting, file type recognition (`.gala`), and basic code structure support.

---

> **Full specification:** This page contains a summary of the GALA language specification with all major features documented. For the complete, unabridged specification with every detail and edge case, see the [GALA.MD source file on GitHub](https://github.com/martianoff/gala/blob/master/docs/GALA.MD).

{% endraw %}
