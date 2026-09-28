---
layout: default
title: "GALA Language Reference - Complete Specification"
description: "Complete GALA language specification. Variables, functions, structs, sealed types, pattern matching, generics, lambdas, interfaces, control flow, and standard library types — the full reference for the Go alternative language."
keywords: "gala language reference, gala specification, gala syntax, gala language guide, go alternative language reference, gala documentation"
permalink: /docs/language-reference/
last_modified_at: 2026-07-30
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Language Reference</p>

{% raw %}

**Related pages:** [Why GALA?](/docs/why-gala/) | [Code Examples](/docs/examples/) | [Immutable Collections](/docs/immutable-collections/) | [Mutable Collections](/docs/mutable-collections/) | [Streams](/docs/streams/) | [Strings](/docs/strings/) | [Time Utilities](/docs/time-utils/) | [Dependency Management](/docs/dependency-management/)

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

## 1. Project Structure {#1-project-structure}

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

## 2. Variable Declarations {#2-variable-declarations}

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

## 3. Functions {#3-functions}

GALA supports both Go-style block functions and Scala-style expression functions.

### Block Functions
```gala
func add(a int, b int) int {
    return a + b
}
```

A block-bodied function with a result type returns its trailing expression, exactly as
a block lambda or a match arm does — the `return` is optional. The trailing value takes
its type from the result type, as a `return` value would, and a trailing `if`/`else`
carries the value in its branches:

```gala
func logged[U any](o Option[U]) Option[U] {
    Println("visited")
    o                                  // same as `return o`
}

func sign(n int) string {
    if (n < 0) { "negative" } else { "non-negative" }
}
```

Prefer the implicit trailing value. `return` stays valid, both at the end and for early
exits such as `if (n < 0) { return None() }`. The body must end in a value, a `return`, or
a diverging call such as `Panic(...)`. A
body that can finish without a value — a trailing `Println(...)`, an assignment, an `if`
with no `else` — is a compile error. A value computed only to be thrown away (a bare
name, literal or operator expression as a statement, including the trailing statement
of a function with no result type) is rejected as evaluated but not used.

### Expression Functions
```gala
func square(x int) int = x * x
```

### Local Functions
Named functions and methods are declared only at the top level of a file. A
function local to a body is a lambda bound to a `val`; a lambda states its
result type after the parameter list, exactly where a function does:

```gala
func report(scores Array[int]) string {
    val clamp = (s int) int => if (s < 0) 0 else s
    return scores.Map((s) => s"${clamp(s)}").MkString(", ")
}
```

A recursive local helper is declared through `var` and then assigned, so the
lambda can refer to it:

```gala
var fact func(int) int
fact = (n int) => if (n <= 1) 1 else n * fact(n - 1)
```

A named `func` inside a body is rejected with
[GALA-E0052](/docs/errors/gala-e0052/), whose hint spells out the equivalent
lambda. A lambda takes no type parameters, so a generic helper stays at the top
level.

### Parameters
Function parameters can be marked as `val` or `var`. By default, they are `val` (immutable).

```gala
func process(val data string, var count int) {
    // data = "new" // Error
    count = count + 1 // OK
}
```

### Named Arguments

Function calls support named arguments. Named arguments can appear in any order — the compiler reorders them to match the function signature.

```gala
func divide(dividend int, divisor int) int = dividend / divisor

divide(divisor = 4, dividend = 20)  // 5 — reordered to divide(20, 4)
divide(20, 4)                        // 5 — positional works too
```

Named arguments work with struct construction, `Copy()` method overrides, and regular function calls.

### Default Parameter Values

Parameters can have default values. When a function is called without providing a defaulted argument, the default expression is injected at the call site. Default parameters must come after required parameters.

```gala
func connect(host string, port int = 8080, tls bool = true) string =
    s"$host:$port tls=$tls"

// Positional: omit trailing defaults
Println(connect("localhost"))                    // localhost:8080 tls=true
Println(connect("localhost", 3000))              // localhost:3000 tls=true
Println(connect("localhost", 3000, false))       // localhost:3000 tls=false

// Named arguments + defaults: skip any defaulted parameter
Println(connect("localhost", tls = false))       // localhost:8080 tls=false
Println(connect(host = "localhost", port = 443)) // localhost:443 tls=true
```

Default expressions are evaluated at each call site (not once at definition time):

```gala
func log(msg string, ts time.Time = time.Now()) {
    Println(s"[$ts] $msg")
}
```

Expression functions also support defaults:

```gala
func greet(name string, greeting string = "Hello") string = s"$greeting, $name!"
```

A default is checked against the parameter's declared type exactly as an explicit
argument would be. For a function-typed parameter, that means a lambda default
takes its parameter and result types from the declaration, and `nil` is a valid
default:

```gala
func shout(s string, decorate func(string) string = (x) => x + "!") string = decorate(s)
func run(hook func() int = nil) int = if (hook == nil) 0 else hook()

struct Backend(
    Name         string,
    Origin       func() Tuple[int, int] = () => (0, 0),
    CursorReport func(int, int)         = (r, c) => {},
)
```

The compiler validates defaults at compile time:
- Default expression type must match the parameter type
- Parameters with defaults must be contiguous at the end of the parameter list

### Higher-Order Functions
Functions are first-class values:

```gala
func apply(x int, f func(int) int) int = f(x)

func main() {
    val double = (x int) => x * 2
    val result = apply(5, double) // result = 10
}
```

### By-Name Arguments (Thunk Sugar)
When a parameter's expected type is a **zero-arg** function type (`func() T` or
void `func()`), you can pass a plain expression where a lambda is expected — it
is automatically lifted into `() => expr` and evaluated lazily each time the
thunk is called. This is what lets `Try` and `Future` read like direct calls:

<!-- doc-check: fragment -->
```gala
func runTwice(body func() int) int = body() + body()

func main() {
    val a = runTwice(compute())        // sugar — lifted to () => compute()
    val b = runTwice(() => compute())  // explicit form; identical

    val f = Future(loadFromDB(id))     // same as Future(() => loadFromDB(id))
    val r = Try(parseConfig(path))     // same as Try(() => parseConfig(path))
}
```

The type parameter is inferred from the lifted expression's own type
(`Future(compute())` where `compute() int` yields `Future[int]`). The lift is
purely additive: an argument that is already a function value is passed through
untouched, and multi-argument function parameters still require an explicit lambda.

### Variadic Functions
```gala
func sum(numbers ...int) int {
    var total = 0
    var i = 0
    for ; i < numbers.Size() ; {
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

## 4. Types and Structs {#4-types-and-structs}

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

<!-- doc-check: fragment -->
```gala
val p1 = Person{Name: "Alice", Age: 30}   // Named fields (Go-style)
val p2 = Person("Bob", 25)                 // Positional (Functional-style)
val p3 = Person(age = 20, name = "Charlie") // Named arguments
```

### Automatic Copy and Equal Methods

<!-- doc-check: fragment -->
```gala
val p1 = Person("Alice", 30)
val p2 = p1.Copy(age = 31) // p2 is Person("Alice", 31)
val same = p1.Equal(p2)    // false
```

A `Copy` override can be any expression, lambdas included. A lambda's parameter types come from the field's declared type, as with a named constructor argument: `calc.Copy(Op = (x) => x + 1)`.

### Sealed Types (Algebraic Data Types)

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

val c = Circle(3.14)
val desc = c match {
    case Circle(radius) => f"radius=$radius%.2f"
    case Rectangle(w, h) => f"$w%fx$h%f"
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

## 5. Interfaces {#5-interfaces}

```gala
type Shaper interface {
    Area() float64
}

struct Rect(width float64, height float64)
func (r Rect) Area() float64 = r.width * r.height
```

## 6. Control Flow {#6-control-flow}

### If Statement and Expression

<!-- doc-check: fragment -->
```gala
val status = if (score > 50) "pass" else "fail"
```

### Match Expression
A default case is required unless the arms cover every value: all variants of a sealed type, both `true` and `false`, or an unguarded arm that matches anything — `case _`, a plain binding (`case n`), or a tuple pattern made only of wildcards, bindings and nested such tuples (`case (_, _, err)`).

<!-- doc-check: fragment -->
```gala
val result = x match {
    case 1 => "one"
    case 2 => "two"
    case n => s"Value is $n"
    case _ => "other"
}

// Boolean exhaustive match
val desc = flag match {
    case true  => "enabled"
    case false => "disabled"
}
```

#### Stable Identifiers (Constants in Patterns)
A capitalized identifier in a `case` pattern that names a value in scope (a local or package `val`/`var`, a parameter, a binding of an enclosing arm, or a `const`/`var` from a hand-written `.go` file of the same library package) compares with `==` instead of binding. A qualified name such as `math.MaxInt8` always compares. Lowercase identifiers always bind, so compare against a lowercase value with a guard. A name cannot appear twice in one pattern (`case (X, X)` is rejected), and a zero-field variant or extractor of the same name takes precedence.
```gala
type Environment string

val Development Environment = "development"

func mode(env Environment, limit Environment) string = env match {
    case Development     => "dev"       // env == Development
    case x if x == limit => "limit"     // lowercase value: use a guard
    case _               => "other"
}
```

#### Type-Based Pattern Matching

<!-- doc-check: fragment -->
```gala
val res = x match {
    case s: string => s"Found string: $s"
    case i: int    => s"Found int: $i"
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

<!-- doc-check: fragment -->
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
    case Array(head, tail...) => s"Head: $head, Tail size: ${tail.Size()}"
    case _ => "Empty"
}
```

### Monadic Binding (`bind` / `also`)

Inside a function whose result type is a monad `M`, `bind name = expr` unwraps `M` and binds the success value; the block lowers to a `FlatMap` chain, so every bound name stays in scope for later statements. `also` marks an *independent* clause: a `bind`+`also` group lowers through `Zip{N}` when the monad provides it — `Validated` (in the `validation` package) accumulates all errors, `Future` runs the clauses concurrently — and falls back to the sequential `FlatMap` chain for fail-fast monads (`Try`/`Option`/`Either`).

<!-- doc-check: fragment -->
```gala
// bind: each value stays in scope; the first Failure short-circuits.
func processOrder(id int) Try[Receipt] {
    bind o = fetchOrder(id)
    bind valid = validateOrder(o)
    bind payment = chargePayment(valid)
    Success(Receipt(o.Id, payment))
}

// also over Validated: report ALL invalid fields, not just the first.
func makePerson(name string, email string, age int) Validated[string, Person] {
    bind n = vName(name)
    also e = vEmail(email)
    also a = vAge(age)
    Valid(Person(n, e, a))
}
```

A Go call that returns `(T, error)` is already a `Try[T]` (see [Go functions that return several results](/docs/language-reference/#go-functions-that-return-several-results)), so `bind n = strconv.Atoi(text)` works directly inside a `Try` block.

Bound names are immutable `val`s. `bind`/`also` work over any user-defined monad that defines `FlatMap[U](f func(T) M[U]) M[U]`; a type without `FlatMap` is rejected with a clear compiler error. See [`bind` / `also` notation](https://github.com/martianoff/gala/blob/master/docs/BIND_NOTATION.MD) for the full specification and the user-monad extension guide.

### For Statement

<!-- doc-check: fragment -->
```gala
for i := 0; i < 10; i++ {
    Println(i)
}

for count < 5 {
    count++
}

for _, v := range items {
    Println(v)
}
```

## 7. Functional Features {#7-functional-features}

### Lambda Expressions

<!-- doc-check: fragment -->
```gala
val f = (x int) => x * x

// Inferred parameter types
val doubled = opt.Map((x) => x * 2)

// Void closures
opt.ForEach((x) => { Println(x) })
```

### Partial Function Literals

<!-- doc-check: fragment -->
```gala
val pf = { case 1 => "one" case 2 => "two" }
val r1 = pf(1)  // Some("one")
val r2 = pf(5)  // None[string]

// Use with Collect
val evenDoubled = numbers.Collect({ case n if n % 2 == 0 => n * 2 })
```

## 8. Generics {#8-generics}

```gala
func identity[T any](x T) T = x

type Box[T any] struct { Value T }
```

## 9. Standard Library Types {#9-standard-library-types}

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

GALA functions return one value; to return several, return a Tuple and destructure it at the call site (`val (q, r) = divmod(17, 5)`).

#### Go functions that return several results {#go-functions-that-return-several-results}

Many Go functions return more than one result — most often a value and an
`error` that says whether the call worked. GALA expressions are always **one
value**, so a call like that is presented as one GALA value, automatically,
wherever it is used as a value:

| The Go function returns | A call of it is a | Example | Its GALA value |
|---|---|---|---|
| a value and an error `(T, error)` | `Try[T]` | `os.ReadFile(path)` | `Try[[]byte]` |
| two or more values and an error | `Try` of a Tuple | `net.SplitHostPort(addr)` | `Try[Tuple[string, string]]` |
| two values `(A, B)` | `Tuple[A, B]` | `math.Modf(x)` | `Tuple[float64, float64]` |
| three to ten values | `Tuple3` … `Tuple10` | `strings.Cut(s, "=")` | `Tuple3[string, string, bool]` |
| only an `error` | the `error` itself | `os.Remove(path)` | `error` (nil when it worked) |

A `Try` is `Success(value)` when the call worked and `Failure(err)` when it
returned an error, so the usual `Try` tools apply directly:

```gala
import (
    "os"
    "strconv"
    "strings"
)

val port = strconv.Atoi("8080") match {                  // Try[int]
    case Success(n) => n
    case Failure(_) => 80
}
val size = os.ReadFile("app.conf").Map((data) => data.Size()).GetOrElse(0)
val (key, value, found) = strings.Cut("port=8080", "=") // Tuple3 destructuring
```

This holds in every position that takes one value: a `val`, a match subject,
a function or method argument, a lambda or function result, an if or match
branch, a struct field, a receiver (`strconv.Atoi(s).Map(...)`). Passed to a
Go function such as `fmt.Println`, the GALA value is what is passed:
`fmt.Println(strconv.Atoi("7"))` prints `Success(7)`.

A Go function that returns only an `error` gives you that error as a value;
wrap the call in `Try(...)` to treat a non-nil error as a failure:
`Try(os.Remove(path))` is a `Try[Void]`. `FromError(os.Remove(path))` means the
same.

`Try(goCall())` is the call's own Try — the conversion is not applied twice —
so `Try(strconv.Atoi(s))`, `Try(() => strconv.Atoi(s))` and `strconv.Atoi(s)`
are all a `Try[int]`. Going through `Try(...)` also turns a panic inside the
call into a `Failure`. Any other by-name parameter takes the call's value like
an ordinary argument: `Future(os.ReadFile(path))` is a `Future[Try[[]byte]]`
that completes with the `Try`.

**Taking the results one by one.** A binding of several names (no
parentheses) still receives Go's results as they are, which is the way to hand
them straight back to Go code:

```gala
import (
    "os"
    "strconv"
)

val data, err = os.ReadFile("app.conf")  // data []byte, err error
var n, parseErr = strconv.Atoi("42")     // var: raw values, reassignable
```

The sole argument of a Go function whose parameters take the results one for
one is passed the same way, as Go does: `template.Must(tmpl.Parse(text))`.

**Using the Try where the plain value is expected** is a compile-time error,
[GALA-E0049](/docs/errors/gala-e0049/), which names the call and the ways to the
value:

```
error[GALA-E0049]: `data` holds the result of `os.ReadFile(...)`, which can fail, so it is a `Try[[]byte]`; `[]byte` is expected here
  = hint: take the value with `.Get()` (panics on failure), `.GetOrElse(default)`, or `match { case Success(v) => ... case Failure(e) => ... }`; or bind the results Go-style: `val v, err = os.ReadFile(...)`
```

A statement whose value is not used — `sb.WriteString("x")`, `fmt.Fprintf(w, ...)`
on its own line — calls the Go function as it is. So does a branch of an
if-expression, or an arm of a match, whose value is not used:
`if (verbose) fmt.Println(msg) else log.Print(msg)` on its own line is an if
statement.

### Either
```gala
val e = Right[int, string]("success")
val msg = e match {
    case Left(code) => s"Error code: $code"
    case Right(s)   => "Result: " + s
}
```

### Try Monad

<!-- doc-check: fragment -->
```gala
val result = Try(riskyDivide(10, 0))  // by-name sugar: runs () => riskyDivide(...) lazily
val parsed = Try(strconv.Atoi("42"))  // Success(42) — the same Try[int] as strconv.Atoi("42") itself
val dir = Try(os.TempDir)             // function reference sugar (bare zero-arg call)

val msg = result match {
    case Success(n) => s"Got: $n"
    case Failure(e) => s"Error: ${e.Error()}"
}
```

#### Try with Go Functions {#try-with-go-functions}

A Go function that returns `(T, error)` already gives a `Try[T]` when its call
is used as a value (see [Go functions that return several
results](/docs/language-reference/#go-functions-that-return-several-results)): a non-nil error is the
`Failure`.

```gala
import "strconv"

val result = strconv.Atoi("42") match {
    case Success(n) => s"Parsed: $n"
    case Failure(err) => s"Error: ${err.Error()}"
}
// result: "Parsed: 42"
```

A Go function returning two or more values and an error gives a `Try` of a
Tuple, which a tuple pattern takes apart:

```gala
import "net"

// net.SplitHostPort returns (string, string, error): a Try[Tuple[string, string]]
val result = net.SplitHostPort("localhost:8080") match {
    case Success((host, port)) => s"host=$host port=$port"
    case Failure(err) => s"Error: ${err.Error()}"
}
// result: "host=localhost port=8080"
```

Writing `Try(...)` around such a call gives the same Try — never a Try inside
a Try — and additionally turns a panic inside the call into a `Failure`. It is
also how a Go function returning only an `error` becomes a `Try[Void]`:

| Go Return Signature | The call | `Try(call)` |
|---------------------|----------|-------------|
| `(T, error)` | `Try[T]` | `Try[T]` |
| `(A, B, error)` | `Try[Tuple[A, B]]` | `Try[Tuple[A, B]]` |
| `(A, B, C, error)` | `Try[Tuple3[A, B, C]]` | `Try[Tuple3[A, B, C]]` |
| `error` | `error` | `Try[Void]` |

### Future Monad

<!-- doc-check: fragment -->
```gala
import . "martianoff/gala/concurrent"

val async = Future[int](expensiveComputation())  // by-name: runs () => expensiveComputation() async
val result = async.Await()           // Returns Try[int]
val doubled = async.Map((v) => v * 2)
```

### Slices and Maps (Go Interop)
**Prefer GALA collections** over Go slices for most use cases. See [Immutable Collections](/docs/immutable-collections/).

```gala
import . "martianoff/gala/collection_immutable"
import . "martianoff/gala/go_interop"

// PREFERRED: GALA collections
val nums = ArrayOf(1, 2, 3, 4, 5)
val doubled = nums.Map((x) => x * 2)

// GO INTEROP: When you need []T (SliceOf lives in go_interop)
val goSlice = SliceOf(1, 2, 3)
```

## 10. Literals and Type Conversions {#10-literals-and-type-conversions}

### String Interpolation

<!-- doc-check: fragment -->
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

## 11. Go Built-in Functions {#11-go-built-in-functions}

Bare Go builtins are **not** part of GALA's surface: calling one is a transpile error ([GALA-E0035](/docs/errors/gala-e0035/)). Each has a GALA-native form or a sanctioned interop wrapper:

| Forbidden builtin | Use instead |
|---|---|
| `len(x)` | `x.Size()` (logical size; **characters** for strings) or `x.ByteSize()` (string bytes) |
| `cap(x)` | `go_interop.SliceCap(x)` |
| `new(T)` | `go_interop.New[T]()` (pointer), or a zero value / `Option[T]` |
| `append(s, v)` | `go_interop.SliceAppend(s, v)` / `SliceAppendAll(s, more)`, or an `Array`/`List` |
| `make([]T, n)` | `go_interop.SliceWithSize` / `SliceWithCapacity`; `MapEmpty` for maps |
| `delete(m, k)` | `go_interop.MapDelete(m, k)`, or `HashMap.Remove(k)` |
| `close(ch)` | `go_interop.CloseChan(ch)` (or `CloseSignal` for a signal channel) |
| `panic(v)` | `go_builtins.Panic(v)` — but prefer `Option` / `Try` / `Either` |
| `recover()` | not available — `Try` captures panics |

## 12. Immutability Under the Hood {#12-immutability-under-the-hood}

### Package-Level Bindings
A package-level `val` is a `std.Immutable[T]` in the generated Go but reads as a plain `T` everywhere — its own file, sibling files, and other packages (qualified, aliased, or dot-imported). A package-level `var` stays a plain, reassignable variable.

<!-- doc-check: fragment -->
```gala
// package colors
val Green = NamedColor(2)
var Hits = 0

// package main
Println(colors.ToSgr(colors.Green))  // Green reads as colors.Color
colors.Hits = colors.Hits + 1        // OK: a var
// colors.Green = NamedColor(3)      // ERROR: cannot assign to immutable variable colors.Green
```

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

### Pointer-Receiver Methods on a `val`
A method with a pointer receiver (a GALA `func (c *Counter) Bump()`, or Go's `url.URL.String`) can be called on a `val`, a field reached through one, a call result or a literal. Go only calls such a method on an addressable value, so it runs on a fresh copy and whatever it assigns to the receiver's own fields is lost. A `var`, a function parameter and a pattern binding are addressable, so there the method changes the variable itself.

```gala
struct Counter(var n int)

func (c *Counter) Bump() int {
    c.n = c.n + 1
    return c.n
}

func main() {
    val c = Counter(n = 1)
    Println(c.Bump())  // 2 — bumped a copy
    Println(c.n)       // 1 — the val is unchanged

    var m = Counter(n = 10)
    m.Bump()
    Println(m.n)       // 11
}
```

The copy is shallow: writes through a map, slice or pointer the value holds reach data the `val` shares. A value that must not be copied is rejected instead with [GALA-E0053](/docs/errors/gala-e0053/): any `sync` or `sync/atomic` type, a type whose pointer has `Lock()`/`Unlock()`, a `noCopy` marker, a struct holding one of them, `strings.Builder` and `bytes.Buffer`. Hold such a value in a `var` or behind a pointer (`&sync.Mutex{}`).

## 13. GALA Packages {#13-gala-packages}

GALA supports Go-style imports with aliases and dot imports.

```gala
import m "math"
import . "martianoff/gala/examples/mathlib"

func main() {
    val res = m.Sqrt(16.0)
    val sum = Add(10, 20) // from mathlib via dot import
}
```

### Package Visibility {#package-visibility}

Visibility is controlled at two levels. **Identifiers** use casing, as in Go: a `PascalCase` type, function, field or method is exported from its package; a `camelCase` one is not.

**Packages** use `internal` directories. A package under a directory named `internal` is importable only from the tree rooted at that directory's parent — Go's rule, applied to GALA import paths:

```
mylib/
  mylib.gala                   package mylib    — public
  internal/
    detail/detail.gala         package detail   — private to example.com/mylib
  sub/
    internal/
      deep/deep.gala           package deep     — private to example.com/mylib/sub
```

`a/b/c/internal/d` is importable only from the tree rooted at `a/b/c`. Only whole path elements count — a directory named `internalize` is an ordinary public package. There is no exemption for the standard library.

This lets a library share code across its own packages without adding it to the API it must keep supporting: consumers call what the parent package exports and cannot reach `internal/detail`, so it can be renamed or deleted freely. A forbidden import is rejected with [GALA-E0041](/docs/errors/gala-e0041/).

Publishing an internal package means moving it out of the `internal` directory, which changes its import path — so it is worth deciding deliberately. For an application, the whole module is already private to you; reach for `internal` where there is a real API boundary to defend, not as a default layout.

See [Dependency Management](/docs/dependency-management/) for managing external packages.

## 14. Embedding Files {#14-embedding-files}

```gala
embed val readme = "README.md"              // string
embed val static EmbeddedFS = "static/*"    // EmbeddedFS

val content = static.ReadString("static/index.html")
```

## 15. Testing {#15-testing}

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

## 16. Best Practices {#16-best-practices}

- **Prefer `val` over `var`** - Use mutable variables only when necessary
- **Use `Copy()` for updates** - `person.Copy(age = 31)`
- **Prefer `match` over if-else chains** for type/value dispatch
- **Use extractors** - `case Some(x) =>` not `if opt.IsDefined() { x := opt.Get() }`
- **Omit type parameters when inferrable** - `Some(42)` not `Some[int](42)`
- **Prefer `s"..."` over `fmt.Sprintf`** - `s"Hello $name"` not `fmt.Sprintf("Hello %s", name)`
- **Prefer GALA collections over Go slices** - Use `Array` or `List` from `collection_immutable`
- **Use `Option[T]`** for nullable values, **`Try[T]`** for operations that may fail

## 17. Dependency Management {#17-dependency-management}

GALA provides a module system similar to Go modules. See [Dependency Management](/docs/dependency-management/) for the full guide.

```bash
gala mod init github.com/user/project
gala mod add github.com/example/utils@v1.2.3
gala mod add github.com/google/uuid@v1.6.0 --go
gala mod tidy
```

## 18. Further Reading {#18-further-reading}

- [Code Examples](/docs/examples/) - More examples of GALA code
- [Streams](/docs/streams/) - Lazy, potentially infinite sequences
- [Strings](/docs/strings/) - Free functions over `string`, `Str` wrapper, and `StringBuilder`
- [Time Utilities](/docs/time-utils/) - Duration and Instant types
- [Immutable Collections](/docs/immutable-collections/) - Array, List, HashMap, HashSet, TreeSet, TreeMap
- [Mutable Collections](/docs/mutable-collections/) - Mutable collection types
- [Why GALA?](/docs/why-gala/) - Features, trade-offs, and honest assessment
- [Dependency Management](/docs/dependency-management/) - Module system and package management

## 19. IDE Support {#19-ide-support}

### IntelliJ IDEA
A basic IntelliJ IDEA plugin is available in `ide/intellij`.

1. Build: `bazel build //ide/intellij:plugin`
2. Install: Settings > Plugins > Install Plugin from Disk...

The plugin provides syntax highlighting, file type recognition (`.gala`), and basic code structure support.

---

> **Full specification:** This page contains a summary of the GALA language specification with all major features documented. For the complete, unabridged specification with every detail and edge case, see the [GALA.MD source file on GitHub](https://github.com/martianoff/gala/blob/master/docs/GALA.MD).

{% endraw %}
