# GALA Language Specification

GALA (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It combines Go's efficiency and simplicity with features inspired by Scala and other functional languages, such as immutability by default, pattern matching, and concise expression syntax.

---

## Table of Contents

1. [Project Structure](#1-project-structure)
2. [Variable Declarations](#2-variable-declarations)
3. [Functions](#3-functions)
   - [Named Arguments](#named-arguments)
   - [Default Parameter Values](#default-parameter-values)
4. [Types and Structs](#4-types-and-structs)
   - [Sealed Types (ADTs)](#sealed-types-algebraic-data-types)
   - [Type Aliases](#type-aliases)
5. [Interfaces](#5-interfaces)
6. [Control Flow](#6-control-flow)
   - [If Statement and Expression](#if-statement-and-expression)
   - [Match Expression](#match-expression)
   - [For Statement](#for-statement)
7. [Functional Features](#7-functional-features)
   - [Lambda Expressions](#lambda-expressions)
   - [Partial Function Literals](#partial-function-literals)
8. [Generics](#8-generics)
9. [Standard Library Types](#9-standard-library-types)
   - [Option Monad](#option-monad)
   - [Tuple](#tuple)
   - [Either](#either)
   - [Try Monad](#try-monad)
   - [Future Monad](#future-monad)
   - [Json](#json)
   - [Yaml](#yaml)
   - [Regex](#regex)
   - [IO Effect](#io-effect)
   - [Path](#path)
   - [Filesystem](#filesystem)
   - [Strings (free functions)](#strings-free-functions)
   - [Slices](#slices)
   - [Maps](#maps)
   - [HashMap](#hashmap)
10. [Literals and Type Conversions](#10-literals-and-type-conversions)
   - [String Interpolation](#string-interpolation)
11. [Go Built-in Functions](#11-go-built-in-functions)
12. [Immutability Under the Hood](#12-immutability-under-the-hood)
    - [ConstPtr - Read-Only Pointers](#constptr---read-only-pointers)
13. [GALA Packages](#13-gala-packages)
14. [Embedding Files](#14-embedding-files)
15. [Testing](#15-testing)
16. [Best Practices](#16-best-practices)
17. [Dependency Management](#17-dependency-management)
18. [Further Reading](#18-further-reading)
19. [IDE Support](#19-ide-support)

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

In Bazel, run `bazel run //:gazelle` to generate and maintain these targets —
it lists every `.gala` file in `srcs` for you (and handles mixed GALA+GO
packages in the same pass). The generated rules look like:

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

The transpiler automatically passes sibling file information so that each file can resolve types, sealed types, and methods defined in other files of the same package.

## 2. Variable Declarations

GALA distinguishes between immutable and mutable variables.

### Immutable (`val`)
Variables declared with `val` are immutable. They must be initialized with a value at the time of declaration. Multiple variables can be declared at once.

```gala
val x = 10
val a, b = 1, 2
// x = 20 // Compile error: cannot assign to immutable variable
```

### Mutable (`var`)
Variables declared with `var` are mutable and can be reassigned. Multiple variables can be declared at once.

```gala
var y = 20
var x, y int = 10, 20
y = 30 // OK
```

### Short Variable Declaration
Inside functions, you can use the short variable declaration operator `:=`. Variables declared this way are **immutable** in GALA.

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
For simple functions, you can use the `=` syntax.

```gala
func square(x int) int = x * x
```

> **Note:** Expression-bodied functions do not support `return` statements. The expression
> after `=` is implicitly the return value. This includes match arms — use expression arms
> (the value itself), not `return` statements:
>
> ```gala
> // CORRECT: expression arms — values are implicit returns
> func classify(x int) string = x match {
>     case 0 => "zero"
>     case _ => "other"
> }
>
> // WRONG: return is not an expression — this causes a parse error
> // func classify(x int) string = x match {
> //     case 0 => return "zero"
> // }
>
> // If you need return statements, use a block-bodied function:
> func classify(x int) string {
>     return x match {
>         case 0 => "zero"
>         case _ => "other"
>     }
> }
> ```

### Parameters
Function parameters can be explicitly marked as `val` or `var`. By default, they are `val` (immutable).

```gala
func process(val data string, var count int) {
    // data = "new" // Error
    count = count + 1 // OK
}
```

#### Multi-Line Parameters and Trailing Commas
Parameter lists support trailing commas, enabling clean multi-line formatting:

```gala
func createServer(
    host string,
    port int = 8080,
    tls bool = true,
    maxConnections int = 100,
) Server = ...

struct Cookie(
    Name string,
    Value string,
    Path string,
    MaxAge int,
)
```

Trailing commas are optional — single-line parameter lists work with or without them. This also applies to sealed type case fields, function call arguments, and tuple literals (e.g. `(model, cmd,)`), so a multi-line tuple body in a `match` arm parses cleanly.

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
func connect(host string, port int = 8080, tls bool = true) Connection {
    // ...
}

// Positional: omit trailing defaults
connect("localhost")                    // port=8080, tls=true
connect("localhost", 3000)              // tls=true
connect("localhost", 3000, false)       // all explicit

// Named arguments + defaults: skip any defaulted parameter
connect("localhost", tls = false)       // port=8080
connect(host = "localhost", port = 443) // tls=true
```

Default expressions are evaluated at each call site (not once at definition time):

```gala
func log(msg string, timestamp time.Time = time.Now()) {
    Println(s"[$timestamp] $msg")
}
log("starting")  // timestamp = current time at each call
```

Expression functions also support defaults:

```gala
func greet(name string, greeting string = "Hello") string = s"$greeting, $name!"
```

The compiler validates defaults at compile time:
- Default expression type must match the parameter type (e.g., `x int = "hello"` is a compile error)
- Parameters with defaults must be contiguous at the end of the parameter list
- Missing required arguments produce a clear error pointing to the GALA source line

### Function Type Parameters (Higher-Order Functions)
GALA supports functions as first-class values. You can pass functions as parameters and return them from other functions.

#### Basic Function Parameters
Use the `func` keyword followed by the parameter and return types:

```gala
func apply(x int, f func(int) int) int = f(x)

func main() {
    val double = (x int) => x * 2
    val result = apply(5, double) // result = 10
}
```

#### Generic Function Parameters
Function type parameters work seamlessly with generics:

```gala
func map[T any, U any](value T, f func(T) U) U = f(value)

func main() {
    val result = map[int, string](42, (x int) => fmt.Sprintf("%d", x))
}
```

#### Functions Returning Functions
Functions can return other functions:

```gala
func multiplier(factor int) func(int) int {
    return (x int) => x * factor
}

func main() {
    val triple = multiplier(3)
    val result = triple(10) // result = 30
}
```

#### Function Parameters with Multiple Arguments
Function types can have multiple parameters:

```gala
func combine(a int, b int, f func(int, int) int) int = f(a, b)

func main() {
    val sum = combine(3, 4, (x int, y int) => x + y)      // sum = 7
    val product = combine(3, 4, (x int, y int) => x * y)  // product = 12
}
```

#### Function Parameters in Generic Methods
Methods on generic structs can accept function parameters:

```gala
type Box[T any] struct { Value T }

func (b Box[T]) Transform[U any](f func(T) U) Box[U] = Box[U](Value = f(b.Value))

func main() {
    val intBox = Box[int](Value = 42)
    val strBox = intBox.Transform((x int) => fmt.Sprintf("Value: %d", x))
}
```

#### By-Name Arguments (Thunk Sugar)
When a parameter's expected type is a **zero-arg** function type (`func() T` or
the void `func()`), you may pass a plain expression where a lambda is expected.
The expression is automatically lifted into `() => expr`, so it is evaluated
lazily — each time the thunk is invoked — instead of eagerly at the call site:

```gala
func runTwice(body func() int) int = body() + body()

func main() {
    // These two calls are identical — the bare expression is lifted to a thunk.
    val a = runTwice(compute())        // sugar: compute() runs on each body() call
    val b = runTwice(() => compute())  // explicit lambda

    // The headline case — the async constructor takes a `func() T`:
    val f = Future(loadFromDB(id))     // same as Future(() => loadFromDB(id))
    val g = Future[int](expensive())   // T may be explicit or inferred from the body
}
```

The element type `T` is inferred from the lifted expression's own type, so
`Future(compute())` where `compute() int` yields a `Future[int]`.

This sugar is purely additive: it only applies where a `func() T` is expected
(passing a bare `T` there would otherwise be a type error), and an argument that
is *already* a function value is passed through untouched — never double-wrapped.
For multi-argument function parameters (`func(a, b) T`) you must still write an
explicit lambda; only zero-arg function parameters accept a bare expression.

Related: a zero-arg *function reference* can likewise be passed directly (the
"Function reference sugar" under Error Handling, e.g. `Try(os.TempDir)`). The
difference is that a reference passes the existing function value, whereas
by-name sugar wraps an arbitrary expression in a fresh thunk.

### Variadic Functions
GALA supports variadic functions using the `...T` syntax, allowing functions to accept a variable number of arguments. The variadic parameter must be the last parameter.

```gala
// Simple variadic function
func sum(numbers ...int) int {
    var total = 0
    var i = 0
    for ; i < len(numbers) ; {
        total = total + numbers[i]
        i = i + 1
    }
    return total
}

// Usage
val result = sum(1, 2, 3, 4, 5) // result = 15
```

#### Generic Variadic Functions
Variadic functions can be combined with generics for flexible factory functions.

```gala
func ListOf[T any](elements ...T) List[T] {
    var result = EmptyList[T]()
    var i = len(elements) - 1
    for ; i >= 0 ; {
        result = result.Prepend(elements[i])
        i = i - 1
    }
    return result
}

// Usage
val numbers = ListOf(1, 2, 3)
val names = ListOf("Alice", "Bob", "Charlie")
```

#### Spreading Slices
You can spread a slice into a variadic function call using `...`.

```gala
val items = SliceOf(1, 2, 3, 4)
val total = sum(items...)  // Spreads slice as variadic arguments
```

### Methods
GALA supports methods as well as generic methods on structs. Methods are declared by providing a receiver before the function name. GALA methods can have their own type parameters.

```gala
type Box[T any] struct { Value T }

// Simple method
func (b Box[T]) GetValue() T = b.Value

// Generic method on a generic struct
func (b Box[T]) Transform[U any](f func(T) U) Box[U] = Box[U](Value = f(b.Value))

func main() {
    val b = Box[int](Value = 10)
    val s = b.Transform((i int) => "Value is " + fmt.Sprintf("%d", i))
}
```

## 4. Types and Structs

### Structs
GALA supports two ways to define structs: traditional Go-style blocks and concise shorthand declarations. In both cases, fields are **immutable by default**. To make a field mutable, use the `var` keyword.

#### Shorthand Struct Declaration
A concise way to define a struct, similar to Scala case classes.

```gala
struct Person(name string, age int, var score int)
```

Generic type parameters are supported in the same position as for generic functions — between the name and the `(`:

```gala
struct Box[T any](Value T)
struct Pair[A any, B any](First A, Second B)

val b = Box[int](Value = 42)
val p = Pair(First = 1, Second = "two") // type params inferred from arguments
```

#### Block Struct Declaration
Traditional Go-style declaration.

```gala
type Person struct {
    Name string    // Immutable
    age  int       // Immutable
    var Score int  // Mutable
}
```

#### Struct Construction
Structs can be constructed using traditional Go-style named fields or using a functional shorthand (positional or named arguments).

```gala
// Named fields (Go-style)
val p1 = Person{Name: "Alice", Age: 30}

// Positional fields (Functional-style)
val p2 = Person("Bob", 25)

// Named arguments (Functional-style)
val p3 = Person(age = 20, name = "Charlie")
```

### Automatic Copy and Equal Methods
Every GALA struct automatically provides `Copy()` and `Equal(other)` methods.

#### Copy Method with Overrides
The `Copy()` method allows creating a copy of a struct with optional field overrides. This is similar to Scala's `copy` method.

```gala
val p1 = Person("Alice", 30)
val p2 = p1.Copy(age = 31) // p2 is Person("Alice", 31)
```

If no overrides are provided, it performs a complete copy of the original object.
**Note:** Providing an override on non-struct types will result in a compilation error.

#### Equal Method
The `Equal(other)` method compares the struct with another of the same type, deeply comparing all fields.

```gala
val p1 = Person("Alice", 30)
val p2 = Person("Alice", 30)
val same = p1.Equal(p2) // true
```

#### Apply Method
If a struct has an `Apply` method, it can be called like a function. GALA automatically expands `object(args)` to `object.Apply(args)`.

```gala
type Append struct { Name string }
func (a Append) Apply(param string) string = param + a.Name

val a = Append("cherry")
val res = a("apple") // expanded to a.Apply("apple")
```

If the struct has no properties, it can be called using the type name directly without explicit instantiation:

```gala
type Implode struct {}
func (i Implode) Apply(params []string) string = strings.Join(params, "")

val res = Implode(SliceOf("a", "b")) // expanded to Implode{}.Apply(SliceOf("a", "b"))
```

### Type Aliases

Type aliases create an alternative name for an existing type. The alias is identical to the original type — they can be used interchangeably.

```gala
type MyString string
type Handler func(Request) Response
```

Type aliases transpile to Go type aliases (`type X = Y`), so methods defined on the original type are available on the alias.

> **Note:** Type aliases are generally not recommended. Prefer using the original type directly — it keeps code clearer and avoids indirection. Type aliases are mainly useful for Go interop scenarios where you need to bridge between GALA and existing Go type names, or when mixing `.gala` and `.go` files in the same package (where GALA type aliases avoid dot-import conflicts with Go type alias declarations).

### Sealed Types (Algebraic Data Types)

Sealed types define algebraic data types (ADTs) concisely. The transpiler auto-generates the parent struct, companion objects, `Apply`/`Unapply` methods, `IsXxx()` discriminators, `Copy`, and `Equal`.

#### Basic Sealed Type
```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}
```

Parentheses are optional on zero-field variants. The declaration above can also be written as:

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point                   // bare identifier is equivalent to `case Point()`
}
```

The same applies in match patterns — `case Point` and `case Point()` are interchangeable:

```gala
val desc = s match {
    case Circle(r) => ...
    case Rectangle(w, h) => ...
    case Point => "a point"       // bare form
    // case Point() => "a point"  // equivalent
}
```

Variants with one or more fields always require parentheses (`case Rectangle(w, h)`, not `case Rectangle`).

This generates:
- Parent struct `Shape` with all variant fields merged + `_variant` discriminator
- Empty companion structs `Circle{}`, `Rectangle{}`, `Point{}`
- `Apply` methods on each companion for construction
- `Unapply` methods for pattern matching
- `IsCircle()`, `IsRectangle()`, `IsPoint()` methods on `Shape`
- `Copy()` and `Equal(other Shape) bool` on `Shape` — the same auto-synthesis as for ordinary structs (see [§4 Automatic Copy and Equal Methods](#automatic-copy-and-equal-methods)). `Equal` first compares the `_variant` tag, then dispatches field-wise via `std.Equal`, so `a.Equal(b)` is the canonical way to test sealed values for structural equality. The Go `==` operator also works on sealed values when every variant field is a Go-comparable type.

#### Construction and Pattern Matching
```gala
val c = Circle(3.14)
val r = Rectangle(10.0, 20.0)

// IsXxx() checks
fmt.Println(c.IsCircle())     // true
fmt.Println(c.IsRectangle())  // false

// Pattern matching
val desc = c match {
    case Circle(radius) => fmt.Sprintf("radius=%.2f", radius)
    case Rectangle(w, h) => fmt.Sprintf("%fx%f", w, h)
    case Point() => "point"
}
```

#### Generic Sealed Types
Sealed types support type parameters:
```gala
sealed type Result[T any] {
    case Ok(Value T)
    case Err(Error error)
}

val success = Ok[int](42)
val failure = Err[int](fmt.Errorf("oops"))
```

When a generic sealed variant takes no fields that mention the parent's
type parameter (e.g. `case None()` of `Option[T]`, or `case NoCmd()` of
`Cmd[T]`), the parameter is pinned by downward inference from the
surrounding context — match subject, `val`/`var` annotation, or
function return. If no signal is available, the transpiler rejects the
call with [`GALA-E0018`](errors/GALA-E0018.md). The full set of
contexts and the propagation mechanism are described in
[Downward Inference for Generic Sealed-Type Case Constructors](TYPE_INFERENCE.MD#downward-inference-for-generic-sealed-type-case-constructors).

#### Wildcard Catch-All in Sealed Match
When you only care about a subset of variants, use `case _ =>` as a catch-all instead of listing every variant. This is valid for exhaustive matching:

```gala
sealed type Animal {
    case Dog(Name string)
    case Cat(Name string)
    case Bird(Name string)
}

// Only match specific variants, catch rest with wildcard
val msg = animal match {
    case Dog(name) => "Woof! I'm " + name
    case _ => "I'm not a dog"
}
```

A **guarded** wildcard (`case _ if cond`) is conditional, not a catch-all:
control falls through to later cases when the guard is false. So a guarded
wildcard may precede a real `case _` default without conflicting with it — only
an *unguarded* `case _` counts as the default:

```gala
val label = n match {
    case 0           => "zero"
    case _ if n < 10 => "small"   // conditional — not the default
    case _           => "large"   // the actual default
}
```

#### Match in statement position (side-effect dispatch)
When a `match` is used as a statement — its value is discarded — the arms are
pure side-effect dispatch and need not produce a common type. This lets each
arm call a different function whose result is ignored:

```gala
// value discarded: arms may return different types (string, bool, void)
c match {
    case '"' => { readString() }   // returns string
    case 't' => { readBool() }     // returns bool
    case _   => { skipOther() }    // returns nothing
}
```

When the match's value *is* used (assigned to a `val`, returned, passed as an
argument), all arms must still unify to one type.

#### Standard Library Sealed Types
The `std` package defines `Option[T]`, `Either[A, B]`, and `Try[T]` as sealed types. See [Standard Library Types](#9-standard-library-types) for details.

## 5. Interfaces

GALA supports interfaces with semantics similar to Go. Interfaces define a set of method signatures that a type must implement to satisfy the interface.

```gala
type Shaper interface {
    Area() float64
}

struct Rect(width float64, height float64)

func (r Rect) Area() float64 = r.width * r.height

func main() {
    val r = Rect(10.0, 5.0)
    val s Shaper = r
    fmt.Println(s.Area())
}
```

Interfaces can also be generic:

```gala
type Container[T any] interface {
    Get() T
}

struct MyInt(v int)
func (m MyInt) Get() int = m.v

func main() {
    val c Container[int] = MyInt(42)
    fmt.Println(c.Get())
}
```

## 6. Control Flow

### If Statement and Expression
GALA supports `if` in two forms: as a **statement** (with blocks) and as an **expression** (without blocks). These two forms have different syntax and cannot be mixed.

#### If Statement (with blocks)
Use blocks `{ ... }` for side effects. The `else` branch is optional. Braces are required.

```gala
if x > 10 {
    fmt.Println("large")
} else {
    fmt.Println("small")
}

// else-if chain
if x > 100 {
    fmt.Println("huge")
} else if x > 10 {
    fmt.Println("large")
} else {
    fmt.Println("small")
}
```

#### If Expression
Use the expression form to produce a value. Parentheses around the condition are required, and both branches must be present. Branches can be either simple expressions or blocks.

```gala
// Simple expressions
val status = if (score > 50) "pass" else "fail"
val abs = if (x >= 0) x else -x

// Block branches — last expression in the block is the result
val url = if (query != "") {
    val encoded = encode(query)
    s"$base?$encoded"
} else {
    base
}

// Mixed: one block, one expression
val label = if (count > 1) {
    val suffix = "s"
    s"$count item$suffix"
} else "1 item"
```

Both forms compile to a Go IIFE (immediately invoked function expression), so they can be used anywhere an expression is expected: `val` initializers, function arguments, return values, etc.

### Match Expression
The `match` expression provides powerful pattern matching, supporting literals, variable bindings, and extractors. GALA follows Scala semantics for pattern matching, where the pattern (or an extractor) is responsible for matching against the object. A default case (`_`) is required unless matching on a sealed type with all variants covered (exhaustive match) or matching on boolean values with both `true` and `false` cases.

```gala
val result = x match {
    case 1 => "one"
    case 2 => "two"
    case n => "Value is " + fmt.Sprintf("%d", n) // Binding: n is bound to x
    case _ => "other"
}

// Boolean exhaustive match — no default case needed
val desc = flag match {
    case true  => "enabled"
    case false => "disabled"
}
```

**Unused variable rule:** All variables extracted in match patterns must be referenced in the branch body or guard expression. Unused variables cause a compiler error. Use `_` to explicitly discard values you don't need:

```gala
// ERROR: unused variable 'y' in match branch
case Some(y) => "has value"

// OK: use '_' to discard
case Some(_) => "has value"

// OK: variable is used in body
case Some(y) => fmt.Sprintf("got %d", y)

// OK: variable is used in guard
case Some(y) if y > 0 => "positive"
```

#### Type-Based Pattern Matching
GALA supports matching based on the type of an object. This is useful when working with `any` or interface types.

```gala
val res = x match {
    case s: string => "Found string: " + s
    case i: int    => "Found int: " + fmt.Sprintf("%d", i)
    case _         => "Unknown type"
}
```

#### Generic Type Pattern Matching
GALA supports matching against generic types. This is particularly useful for generic containers like `Option[T]`, `Either[A, B]`, or custom generic structs.

```gala
type Wrap[T any] struct { Value T }

val w = Wrap[int](Value = 42)
val res = w match {
    case i: Wrap[int]    => "Wrapped int"
    case s: Wrap[string] => "Wrapped string"
    case _               => "Other"
}
```

Typed patterns can also be nested within other patterns:

```gala
val opt = Some(42)
opt match {
    case Some(i: int) => fmt.Println("Integer:", i)
    case Some(s: string) => fmt.Println("String:", s)
    case _ => fmt.Println("Other")
}
```

#### Wildcard Type Patterns
GALA supports using the underscore `_` as a type in typed patterns. This matches any type and is equivalent to matching against `any`.

```gala
val res = x match {
    case v: _ => "Matched any type: " + fmt.Sprintf("%T", v)
    case _    => "unknown"
}
```

#### Generic Wildcard Type Patterns
GALA supports wildcard matching for generic types using `Name[_]`. This allows matching any instantiation of a generic struct and accessing its fields.

```gala
type Wrap[T any] struct { Value T }
func (w Wrap[T]) GetValue() any = w.Value

val w = Wrap[int](Value = 42)
val res = w match {
    case w1: Wrap[_] => "Wrapped value: " + fmt.Sprintf("%v", w1.GetValue())
    case _           => "Other"
}
```

#### Extractors and `Unapply`
GALA follows Scala semantics for extractors. When a struct or object is used as a pattern, its `Unapply` method is called with the matched object as an argument.

Every GALA struct automatically generates an `Unapply` method, provided all its fields are public (starting with an uppercase letter). This allows for positional property extraction.

```gala
struct Person(Name string, Age int)

val p = Person("Alice", 30)
val msg = p match {
    case Person(name, 30) => "Alice is 30"
    case Person(_, age)   => "Someone else is " + fmt.Sprintf("%d", age)
    case _                => "Unknown"
}
```

##### Defining Custom Extractors

Custom extractors must implement an `Unapply` method with one of the following signatures:

**1. Option-returning extractors** (for extracting values):
```gala
// Non-generic extractor with concrete parameter type
type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None[int]()

// Generic extractor with parameterized type
type Wrapper[T any] struct {}
func (w Wrapper[T]) Unapply(o Wrap[T]) Option[T] = Some[T](o.Value)
```

**2. Boolean-returning extractors** (for guard patterns):
```gala
type Positive struct {}
func (p Positive) Unapply(i int) bool = i > 0
```

**Important:** The parameter and return types must be concrete or properly parameterized. The following is **NOT allowed**:
```gala
// INVALID - will cause a compile error
func (e MyExtractor) Unapply(v any) any  // Unapply(any) any is not supported
```

##### Generic Extractors

For extractors that work with generic container types, define a generic extractor that mirrors the container's type parameters:

```gala
// Extractor for Wrap[T] containers
type Wrapper[T any] struct {}
func (w Wrapper[T]) Apply(v T) Wrap[T] = Wrap[T](Value = v)
func (w Wrapper[T]) Unapply(o Wrap[T]) Option[T] = Some[T](o.Value)

val w = Wrapper(100)  // Creates Wrap[int]
val res = w match {
    case Wrapper(i: int) => fmt.Sprintf("Extracted: %d", i)
    case _ => "Other"
}
```

The transpiler infers the type parameter `T` from the matched type, so `Wrapper` in the pattern becomes `Wrapper[int]`.

**Pointer types** are also supported for extractor type inference:

```gala
// Extractor for pointer to generic container
type Unwrap[T any] struct {}
func (u Unwrap[T]) Unapply(w *Container[T]) Option[T] = Some[T](w.value)

val c = &Container[int](value = 42)
val res = c match {
    case Unwrap(v) => v  // T inferred as int from *Container[int]
    case _ => 0
}
```

**Explicit type parameters** can also be provided when needed:

```gala
val res = c match {
    case Unwrap[int](v) => v  // Explicit type parameter
    case _ => 0
}
```

##### Using Extractors

Extractors can be nested: `case Some(Even(n)) => ...`. You can use the underscore `_` to skip variable bindings in any extractor: `case Some(_) => "Got something"`.

##### Mixing guard and Option extractors in one match

Boolean-returning and `Option`-returning extractors can appear side-by-side in the same `match` expression. The transpiler dispatches on the `Unapply` method's return type: `bool` becomes an `if`-style guard with no binding, `Option[T]` unwraps the inner value via `IsDefined` + `Get` and exposes it to the case body. This lets a single match express "shape" checks (guards) and "shape-plus-destructure" checks (extractors) without having to choose one style upfront.

```gala
type IsZero struct {}
func (_ IsZero) Unapply(n int) bool = n == 0

type NonZero struct {}
func (_ NonZero) Unapply(n int) Option[int] =
    if (n == 0) None[int]() else Some(n)

func classify(n int) string = n match {
    case IsZero()              => "zero"                // bool-return guard
    case NonZero(v) if v < 0   => s"negative($v)"       // Option-return extractor + guard
    case NonZero(v)            => s"positive($v)"       // Option-return extractor
    case _                     => "impossible"
}
```

See `examples/mixed_guard_extractor.gala` for a runnable version.

##### Sealed-Variant Pattern Arity

Each variant pattern must bind the exact number of fields the variant
declares — use `_` for fields you don't care about. See
[docs/errors/GALA-E0004.md](errors/GALA-E0004.md).

#### Pattern Matching Filters (Guards)
Similar to Scala, GALA supports additional `if` conditions in pattern match clauses, often referred to as guards. These filters allow you to apply additional constraints to the extracted variables.

```gala
val res = x match {
    case i: int if i > 100 => "Large integer"
    case i: int if i > 0   => "Positive integer"
    case Person(name, age) if age < 18 => name + " is a minor"
    case _ => "Other"
}
```

#### Sequence Pattern Matching
GALA supports Scala-like sequence pattern matching for collections that implement the `Seq` interface (such as `Array` and `List` from `collection_immutable`). This allows extracting elements from sequences with rest patterns.

##### Basic Sequence Patterns
Use the `...` suffix to match zero or more remaining elements:

```gala
import . "martianoff/gala/collection_immutable"

val arr = ArrayOf(1, 2, 3, 4, 5)

// Extract first two elements, ignore rest
val res1 = arr match {
    case Array(first, second, _...) => fmt.Sprintf("First: %d, Second: %d", first, second)
    case _ => "Not enough elements"
}

// Extract head and capture tail
val res2 = arr match {
    case Array(head, tail...) => fmt.Sprintf("Head: %d, Tail size: %d", head, tail.Size())
    case _ => "Empty"
}
```

##### Rest Pattern Variants
- `_...` - Match and discard remaining elements (wildcard rest)
- `name...` - Match and capture remaining elements as a sequence

```gala
val list = ListOf("a", "b", "c", "d")

// Capture the rest as a variable
val res = list match {
    case List(first, second, rest...) => fmt.Sprintf("First: %s, Second: %s, Rest size: %d", first, second, rest.Size())
    case _ => "Not enough elements"
}

// Check minimum length without capturing
val hasThree = list match {
    case List(_, _, _, _...) => "Has at least 3 elements"
    case _ => "Less than 3 elements"
}
```

##### How It Works
Sequence patterns work with any type implementing the `Seq[T]` interface:

```gala
type Seq[T any] interface {
    Size() int
    Get(index int) T
    SeqDrop(n int) any
}
```

The pattern generates a size check (`Size() >= N`) before extracting elements, ensuring safe access. The rest pattern (`...`) captures remaining elements using `SeqDrop(N)`.

### For Statement
GALA supports Go-style for loops with the following variants:

#### Full For Loop
The classic C-style for loop with init, condition, and post statements:

```gala
for i := 0; i < 10; i++ {
    fmt.Println(i)
}
```

Variables declared with `:=` in the init statement are **mutable**, allowing them to be modified by the post statement (`i++` or `i--`).

#### Condition-Only Loop
A while-style loop that runs as long as the condition is true:

```gala
var count = 0
for count < 5 {
    fmt.Println(count)
    count++
}
```

#### Infinite Loop
An infinite loop that runs until explicitly broken:

```gala
for {
    // Process forever or break when done
    if shouldStop() {
        break
    }
}
```

#### Range Loop
Iterate over slices, arrays, or maps:

```gala
val items = SliceOf(1, 2, 3)
for i, v := range items {
    fmt.Printf("index %d: %d\n", i, v)
}
```

#### Break and Continue
GALA supports `break` and `continue` statements inside for loops, with the same semantics as Go.

- `break` exits the innermost enclosing loop immediately
- `continue` skips the rest of the current iteration and advances to the next

```gala
// Break: exit loop early
for i := 0; i < 10; i++ {
    if i >= 3 {
        break
    }
    fmt.Println(i) // Prints 0, 1, 2
}

// Continue: skip even numbers
for i := 0; i < 6; i++ {
    if i % 2 == 0 {
        continue
    }
    fmt.Println(i) // Prints 1, 3, 5
}
```

#### Increment and Decrement Operators
GALA supports `++` and `--` operators for incrementing and decrementing mutable variables:

```gala
var x = 0
x++  // x is now 1
x--  // x is now 0

// Commonly used in for loops
for i := 0; i < 10; i++ {
    // ...
}
```

**Note:** The `++` and `--` operators can only be used on **mutable** variables (declared with `var` or `:=` in for loop init).

#### Unary Operators
GALA supports unary prefix operators:

```gala
val x = 10
val negated = -x        // -10
val negLit = -42         // negative literal
val negFloat = -3.14     // negative float
val notTrue = !true      // false

func negate(x int) int = -x
fmt.Println(negate(-5))  // 5
```

Supported unary operators: `+`, `-`, `!`, `^`, `*` (dereference), `&` (address-of), `<-` (channel receive).

## 7. Functional Features

### Lambda Expressions
Lambdas use the `=>` syntax. Parameter types can be omitted when inferrable from context (e.g., as arguments to typed methods).

```gala
// Explicit parameter types
val f = (x int) => x * x
val g = (a int, b int) => {
    val sum = a + b
    return sum * sum
}

// Inferred parameter types (when passed to typed methods)
val opt = Some(42)
val doubled = opt.Map((x) => x * 2)             // x inferred as int
val filtered = opt.Filter((x) => x > 10)        // x inferred as int

// Void closures (no return value)
opt.ForEach((x) => {
    fmt.Println(x)
})
```

#### Placeholder Lambda Shorthand

For single-expression lambdas, you can omit the parameter list entirely and
use `_` as a placeholder for each argument. The transpiler rewrites the
expression into a lambda whose parameter count equals the number of `_`
tokens, left-to-right:

```gala
val nums = ArrayOf[int](1, 2, 3, 4, 5)

// Single placeholder — one parameter.
val doubled = nums.Map(_ * 2)              // ≡ (x) => x * 2
val evens   = nums.Filter(_ % 2 == 0)      // ≡ (x) => x % 2 == 0
val names   = people.Map(_.Name)           // ≡ (p) => p.Name

// Multiple placeholders — positional, each becomes a separate parameter.
val sum     = nums.FoldLeft(0, _ + _)      // ≡ (a, b) => a + b
val product = nums.FoldLeft(1, _ * _)      // ≡ (a, b) => a * b

// Parenthesized subexpressions are fine.
val plus1Doubled = nums.Map((_ + 1) * 2)   // ≡ (x) => (x + 1) * 2
```

The rewrite is gated on the argument's expected type being a function type
— GALA only rewrites when it can see that a lambda is wanted at the call
site. Outside that context, `_` retains its existing meanings: the wildcard
pattern in `match`, the unused-binding marker in `val (a, _) = tuple`, and
the "any" type when written in a type position.

Parameter types are inferred from the expected function type (e.g. `Map`
on an `Array[int]` gives `_` type `int`). Placeholder lambdas may not
escape an argument position: you cannot assign `_ * 2` to a val, return it
from a function, or use it as a standalone expression.

### Partial Function Literals
GALA supports Scala-style partial function syntax where `{ case pattern => result }` creates a function that returns `Option[T]`. This enables concise pattern matching that automatically wraps results in `Some` and returns `None` for unmatched cases.

```gala
// Basic partial function with literal patterns
val pf = { case 1 => "one" case 2 => "two" }

val r1 = pf(1)  // Some("one")
val r2 = pf(5)  // None[string]
```

#### Syntax
A partial function literal is a block that starts with `case` clauses:

```gala
{ case pattern1 => result1 case pattern2 => result2 }
```

The resulting function has type `func(T) Option[U]` where:
- `T` is inferred from the patterns or calling context
- `U` is the type of the result expressions

#### Return Type Behavior
- Matched cases automatically wrap results in `Some(result)`
- Unmatched cases return `None[U]()`

```gala
val describeNumber = { case 1 => "one" case 2 => "two" }

// Equivalent to:
val describeNumber = (x any) => x match {
    case 1 => Some("one")
    case 2 => Some("two")
    case _ => None[string]()
}
```

#### Use Cases
Partial functions are especially useful with collection operations that expect optional results:

```gala
// Filter and transform in one operation
val numbers = ArrayOf(1, 2, 3, 4, 5)
val evenDoubled = numbers.Collect({ case n if n % 2 == 0 => n * 2 })
// Result: [4, 8]
```

**Tip**: `Collect` is preferred over chaining `.Filter(p).Map(f)` as it combines both operations in a single pass.

#### Extractors in Partial Functions
When using extractors like `Some(n)`, the parameter type needs to be inferrable from context:

```gala
// Type inferred from collection element type
val options = ArrayOf(Some(1), None[int](), Some(2))
val values = options.Collect({ case Some(n) => n * 2 })
// Result: [2, 4]
```

## 8. Generics

GALA supports generics using square brackets `[]`.

```gala
func identity[T any](x T) T = x

type Box[T any] struct {
    Value T
}
```

## 9. Standard Library Types

GALA provides several built-in types in the `std` package for common patterns.

### Option Monad
`Option[T]` is a sealed type for handling optional values safely.

```gala
// Defined as a sealed type in std:
sealed type Option[T any] {
    case Some(Value T)
    case None()
}

val x = Some(10)
val y = None[int]()

// Pattern matching (exhaustive - no case _ needed)
val msg = x match {
    case Some(v) => s"Got: $v"
    case None()  => "Empty"
}

// Monadic operations
val result = x.Map((i) => i * 2)

// Safe value extraction
val value = x.GetOrElse(0)
val alternative = x.OrElse(Some(99))

// Side-effect methods (return original value for chaining)
x.OnSome((v) => { Println(s"Found: $v") })
 .OnNone(() => { Println("Not found") })
 .Map((i) => i * 2)
```

#### Key Methods
| Method | Description |
|--------|-------------|
| `IsDefined()` / `IsEmpty()` | Check the state |
| `Get()` | Get value or panic |
| `GetOrElse(default)` | Get value or return default |
| `OrElse(alternative)` | Return this if Some, otherwise alternative |
| `OnSome(f)` | Execute side-effect if Some, return original Option |
| `OnNone(f)` | Execute side-effect if None, return original Option |
| `ForEach(f)` | Apply procedure if nonempty |
| `Map[U](f)` | Transform value if Some |
| `FlatMap[U](f)` | Chain operations returning Option |
| `Filter(predicate)` | Keep Some if predicate holds |

### Tuple
`Tuple[A, B]` represents a pair of values. GALA supports concise parenthesis syntax for tuples (up to Tuple5).

```gala
// Concise syntax (preferred)
val t = (1, "hello")
val triple = (1, "hello", true)

// Verbose syntax (when needed)
val t2 = Tuple[int, string](V1 = 1, V2 = "hello")

// Pattern matching works with both
val msg = t match {
    case (a, b) => s"Got $a and $b"
    case _      => "Unknown"
}

// Tuple destructuring in val declarations
val (a, b) = t         // a = 1, b = "hello"
val (x, y, z) = triple // x = 1, y = "hello", z = true

// Accessing fields directly
val first = t.V1   // 1
val second = t.V2  // "hello"
```

#### Multiple Return Values

GALA does not support Go-style multi-value returns `(A, B, error)`. Instead, use **Tuple types** to return multiple values from a function, and **tuple destructuring** at the call site.

```gala
// Return two values using Tuple[A, B]
func swap(a int, b int) Tuple[int, int] = (b, a)

// Return three values using Tuple3[A, B, C]
func divmod(a int, b int) Tuple3[int, int, string] {
    if (b == 0) {
        return (0, 0, "division by zero")
    }
    return (a / b, a % b, "ok")
}

// Destructure at call site (note parentheses around variable names)
val (x, y) = swap(3, 7)            // x = 7, y = 3
val (q, r, status) = divmod(17, 5) // q = 3, r = 2, status = "ok"

// Or use pattern matching
val result = divmod(10, 3) match {
    case (q, r, "ok") => s"$q remainder $r"
    case (_, _, err)   => s"Error: $err"
}
```

**Tuple type reference:**
| Arity | Type | Construction |
|-------|------|-------------|
| 2 | `Tuple[A, B]` | `(a, b)` |
| 3 | `Tuple3[A, B, C]` | `(a, b, c)` |
| 4 | `Tuple4[A, B, C, D]` | `(a, b, c, d)` |
| 5+ | `Tuple5[...]` ... `Tuple10[...]` | `(a, b, c, d, e, ...)` |

**For Go interop** with functions that return `(T, error)`, use `var` to get raw Go values:
```gala
var data, err = goFunction()  // var avoids Immutable wrapping
```

See [Either](#either) for typed error handling, and [Try](#try-monad) for exception-safe error handling.

### Either
`Either[A, B]` is a sealed type representing a value that can be one of two types. It is often used for error handling where `Left` is the error and `Right` is the success value.

```gala
// Defined as a sealed type in std:
sealed type Either[A any, B any] {
    case Left(LeftValue A)
    case Right(RightValue B)
}

val e = Right[int, string]("success")
val msg = e match {
    case Left(code) => s"Error code: $code"
    case Right(s)   => "Result: " + s
}

// Monadic operations are biased towards Right
val length = e.Map((s) => len(s))

// FlatMap chains computations that return Either
val result = Right[string, int](5).FlatMap((x) => Right[string, int](x + 100))

// Chaining Map and FlatMap
val chained = Right[string, int](3).Map((x) => x * 10).FlatMap((x) => Right[string, int](x + 1))

// Side-effect methods (return original value for chaining)
val logged = Right[string, int](42)
    .OnRight((v) => { Println(s"Success: $v") })
    .OnLeft((e) => { Println(s"Error: $e") })
    .Map((x) => x * 2)
```

### Try Monad
`Try[T]` is a sealed type representing a computation that may either succeed with a value of type T or fail with an error. It provides a functional approach to error handling, similar to Scala's Try monad.

```gala
// Defined as a sealed type in std:
sealed type Try[T any] {
    case Success(Value T)
    case Failure(Err error)
}

// Create Try values
val success = Success(42)
val failure = Failure[int](NoSuchElementError(Message = "not found"))

// Try: safely execute code that may panic. Try's parameter is a `func() T`
// thunk, so by the by-name sugar (see §3, By-Name Arguments) a bare expression
// is lifted into `() => expr` and run lazily inside Try — no explicit lambda:
val result = Try(riskyDivide(10, 0))  // same as Try(() => riskyDivide(10, 0)) — Failure (catches panic)
val safe = Try(riskyDivide(10, 2))    // Success(5)
val parsed = Try(strconv.Atoi("42"))  // (T, error) auto-wrapped — Success(42)

// Function reference sugar: when the body is exactly a zero-arg call, pass the
// function itself for the most concise form.
val dir = Try(os.TempDir)               // same as Try(() => os.TempDir())
val answer = Try(getAnswer)             // works with GALA functions too

// Pattern matching (exhaustive - no case _ needed)
val msg = success match {
    case Success(n) => s"Got: $n"
    case Failure(e) => s"Error: ${e.Error()}"
}

// Monadic operations
val doubled = success.Map((n) => n * 2)
val result = success.FlatMap((n) => divide(n, 2))

// Recovery
val recovered = failure.Recover((e) => 0)
val recoveredWith = failure.RecoverWith((e) => Success(0))

// Side-effect methods (return original value for chaining)
val logged = success
    .OnSuccess((n) => { Println(s"Got: $n") })
    .OnFailure((e) => { Println(s"Error: ${e.Error()}") })
    .Map((x) => x * 2)

// Safe value extraction
val value = failure.GetOrElse(0)
val alternative = failure.OrElse(Success(100))

// Conversion
val opt = success.ToOption()      // Option[int]
val either = success.ToEither()   // Either[error, int]
```

#### Railway-Oriented Programming
Try enables elegant error handling pipelines where errors short-circuit the chain:

```gala
func processOrder(id int) Try[Receipt] =
    fetchOrder(id)
        .FlatMap[Order]((o Order) => validateOrder(o))
        .FlatMap[Order]((o Order) => chargePayment(o))
        .FlatMap[Receipt]((o Order) => createReceipt(o))
        .RecoverWith((e error) => {
            logError(e)
            return Failure[Receipt](e)
        })
```

#### Key Methods
| Method | Description |
|--------|-------------|
| `Try[T](f)` | Execute f, catch panics as Failure |
| `Try(funcRef)` | Pass a zero-arg function reference directly (sugar) |
| `IsSuccess()` / `IsFailure()` | Check the state |
| `Get()` | Get value or panic |
| `GetOrElse(default)` | Get value or return default |
| `OrElse(alternative)` | Return this if Success, otherwise alternative |
| `OnSuccess(f)` | Execute side-effect if Success, return original Try |
| `OnFailure(f)` | Execute side-effect if Failure, return original Try |
| `Map[U](f)` | Transform value if Success |
| `FlatMap[U](f)` | Chain operations returning Try |
| `Filter(predicate)` | Keep Success if predicate holds |
| `Recover(f)` | Recover from Failure with a value |
| `RecoverWith(f)` | Recover from Failure with a new Try |
| `Transform[U](onSuccess, onFailure)` | Handle both cases |
| `Fold[U](onFailure, onSuccess)` | Reduce to a single value |
| `ToOption()` | Convert to Option |
| `ToEither()` | Convert to Either[error, T] |

#### Try with Go Multi-Return Functions

Go functions that return `(T, error)` work naturally with `Try` — the transpiler automatically
detects the error return and wraps the call so that a non-nil error becomes `Failure`:

```gala
import "strconv"

val result = Try(strconv.Atoi("42")) match {
    case Success(n) => s"Parsed: $n"
    case Failure(err) => s"Error: ${err.Error()}"
}
// result: "Parsed: 42"
```

For Go functions returning more than two values like `(A, B, error)`, `Try` automatically
wraps the non-error values in a `Tuple`:

```gala
import "net"

// net.SplitHostPort returns (string, string, error)
// Try wraps it as Try[Tuple[string, string]]
val result = Try(net.SplitHostPort("localhost:8080")) match {
    case Success(hp) => s"host=${hp.V1} port=${hp.V2}"
    case Failure(err) => s"Error: ${err.Error()}"
}
// result: "host=localhost port=8080"
```

The transpiler automatically detects the Go function signature and generates the appropriate
tuple wrapping. Access tuple fields via `.V1`, `.V2`, etc. Supported patterns:

| Go Return Signature | Try Type |
|---------------------|----------|
| `(T, error)` | `Try[T]` |
| `(A, B, error)` | `Try[Tuple[A, B]]` |
| `(A, B, C, error)` | `Try[Tuple3[A, B, C]]` |

### Future Monad

`Future[T]` represents an asynchronous computation that will eventually produce a value of type T or fail with an error. It provides a functional approach to concurrent programming, similar to Scala's Future monad.

**Value type (handle pattern):** `Future[T]` is a value type — pass it by value, never as `*Future[T]`. Although a Future holds mutable shared state (mutex, signal, completion flag), that state lives behind a pointer wrapped inside the Future, so copying a Future copies the handle and preserves its identity and synchronization. Use `Future[T]` in explicit type annotations (function signatures, struct fields):

```gala
// Function returning a Future — use Future[T]
func fetchUser(id int) Future[User] = Future[User](loadFromDB(id))

// Struct with a Future field — use Future[T]
type AsyncResult struct {
    future Future[string]
}
```

The async constructor is the type name itself: `Future[T](body)` runs body
asynchronously on `GlobalEC()`. Its parameter is a `func() T` thunk, so by the
by-name sugar (see §3, By-Name Arguments) a bare expression is lifted into
`() => expr` — `Future(loadFromDB(id))` means `Future(() => loadFromDB(id))` and
defers the work onto the execution context rather than running it eagerly at the
call site. With `val` and type inference, the type parameter is inferred from the
body automatically (`Future(expr)` needs no explicit `[T]`):

```gala
import . "martianoff/gala/concurrent"

// Create Futures — type is inferred as Future[T]
val immediate = FutureOf[int](42)                    // Already completed with value
val async = Future[int](expensiveComputation())  // Runs asynchronously

// Blocking operations
val result = async.Await()           // Returns Try[int]
val value = async.GetOrElse(0)       // Returns int, default on failure

// Monadic operations
val doubled = async.Map((v) => v * 2)
val chained = async.FlatMap((v) => fetchName(v))

// Pattern matching
val msg = async match {
    case Succeeded(v) => s"Got: $v"
    case Failed(e) => s"Error: ${e.Error()}"
    case _ => "Unknown"
}
```

For comprehensive documentation including Promise, ExecutionContext, sequence operations, and all methods, see [Concurrent](CONCURRENT.MD).

### Json

The `json` package provides zero-reflection, compile-time JSON serialization with builder pattern configuration. Located in the `json` package.

```gala
import . "martianoff/gala/json"
```

#### Codec — Builder Pattern API

```gala
struct Person(FirstName string, LastName string, Age int)

// Create codec with naming strategy
val codec = Codec[Person](SnakeCase())

// Builder methods for field configuration
val codec = Codec[Person](SnakeCase())
    .Omit("Password")
    .Rename("Email", "email_address")
    .OmitEmpty("Bio")
```

#### Encode and Decode

All operations return `Try[T]` for safe error handling:

```gala
val person = Person("Alice", "Smith", 30)

// Encode to JSON string
val jsonStr = codec.Encode(person).Get()
// => {"first_name":"Alice","last_name":"Smith","age":30}

// Pretty-printed JSON
val pretty = codec.EncodePretty(person).Get()

// Decode from JSON — returns Try[Person], fully typed
val decoded = codec.Decode(jsonStr)
decoded.ForEach((p) => Println(s"Name: ${p.FirstName}"))
```

#### Pattern Matching

Codec instances work as pattern matching extractors via `Unapply`:

```gala
val result = jsonStr match {
    case codec(p) => s"Found: ${p.FirstName}, age ${p.Age}"
    case _ => "invalid JSON"
}
```

#### Naming Strategies

| Strategy | Example | Result |
|----------|---------|--------|
| `AsIs()` | `FirstName` | `FirstName` |
| `CamelCase()` | `FirstName` | `firstName` |
| `SnakeCase()` | `FirstName` | `first_name` |
| `KebabCase()` | `FirstName` | `first-name` |

#### Qualified Import

```gala
import "martianoff/gala/json"

val codec = json.Codec[Person](json.SnakeCase())
```

#### API Reference

| Method | Signature | Description |
|--------|-----------|-------------|
| `Codec[T](naming)` | `Naming → JsonEncoder[T]` | Create codec (StructMeta auto-injected) |
| `.Naming(n)` | `Naming → JsonEncoder[T]` | Set naming strategy |
| `.Omit(field)` | `string → JsonEncoder[T]` | Exclude field |
| `.Rename(field, key)` | `string, string → JsonEncoder[T]` | Custom key |
| `.OmitEmpty(field)` | `string → JsonEncoder[T]` | Skip zero-value field |
| `.Encode(v)` | `T → Try[string]` | Serialize to JSON |
| `.EncodePretty(v)` | `T → Try[string]` | Pretty-printed JSON |
| `.Decode(s)` | `string → Try[T]` | Deserialize from JSON |
| `.Unapply(s)` | `string → Option[T]` | Pattern matching extractor |

#### Nested Structures and Unknown Fields

`Codec[T]` handles nested struct fields including `Array[Struct]`, `List[Struct]`, `HashMap[string, Struct]`, and combinations such as `Array[Array[Struct]]`. The transpiler discovers reachable structs transitively (including across packages), and the naming strategy propagates into nested fields automatically.

```gala
struct Tag(Key string, Color string)
struct User(Name string, Tags Array[Tag])

val codec = Codec[User](SnakeCase())
val tags = EmptyArray[Tag]().Append(Tag("urgent", "red"))
val user = User("alice", tags)
codec.Encode(user).Get()
// => {"name":"alice","tags":[{"key":"urgent","color":"red"}]}
```

The decoder silently drops fields that are not declared on the target struct — there is no strict mode. See the website [Json docs](https://martianov.dev/docs/json/) for the long-form treatment.

### Yaml

The `yaml` package mirrors `json` exactly: same builder API, same auto-injected `StructMeta[T]`, same `Encode` / `Decode` / `Unapply` surface. The only difference is the emitted format — block-style YAML instead of JSON. Located in the `yaml` package.

```gala
import . "martianoff/gala/yaml"

struct Person(FirstName string, LastName string, Age int)

val codec = Codec[Person](SnakeCase())

val yamlStr = codec.Encode(Person("Alice", "Smith", 30)).Get()
// =>
// first_name: Alice
// last_name: Smith
// age: 30

val decoded = codec.Decode(yamlStr)  // Try[Person]

// Pattern matching extractor
val msg = yamlStr match {
    case codec(p) => s"Hello ${p.FirstName}"
    case _ => "invalid"
}
```

Builder methods (`Naming`, `Omit`, `Rename`, `OmitEmpty`), naming strategies (`AsIs`, `CamelCase`, `SnakeCase`, `KebabCase`), nested struct collections, and the silent-drop behaviour for unknown fields all match the JSON codec — see the [Json](#json) section for details. There is no `EncodePretty` for YAML; block-style output is already readable.

#### Supported YAML Subset

The codec covers a focused subset: block-style mappings, block-style sequences, scalars (`string`, `int`, `float`, `bool`, `null`), literal block scalars (`|`), comments, and nested structures. Out of scope: anchors, aliases, flow style, custom tags.

See the website [Yaml docs](https://martianov.dev/docs/yaml/) for the long-form treatment.

### StructMeta — Compile-Time Struct Introspection

`StructMeta[T]` is a compiler intrinsic that provides zero-reflection, type-safe access to struct fields. It is the foundation for building codecs and any code that needs to inspect struct layout at compile time.

```gala
struct Person(FirstName string, LastName string, Age int)

val meta = StructMeta[Person]()
meta.NumFields()     // 3
meta.FieldName(0)    // "FirstName"
```

The transpiler generates a specialised struct (`_StructMeta_Person`) that satisfies the generic `StructMeta[Person]` interface from `std/meta.gala`. Every dispatch method is typed end to end — no `any`, no reflection, no runtime type assertions on user data.

#### Auto-Injection

When a generic type's `Apply` method declares its first parameter as `StructMeta[T]` (the typed interface in `std/meta.gala`), the transpiler auto-generates and injects the appropriate `_StructMeta_T{}` value. This is how `Codec[Person](SnakeCase())` works — the user never needs to write `StructMeta[Person]()` explicitly. `Codec[T]` itself is implemented as a pure GALA library (`json/json.gala`, with the `yaml` package as its YAML twin) built on top of the neutral `FieldEncoder` / `FieldDecoder` interfaces from `std/meta.gala`; the transpiler carries no format-specific knowledge.

#### Building Custom Codecs

`StructMeta` is format-agnostic. To support a new format (YAML, TOML, etc.), implement the `FieldEncoder` / `FieldDecoder` interfaces from `std/meta.gala`:

```gala
type FieldEncoder interface {
    WriteKey(name string)
    WriteString(v string)
    WriteInt(v int)
    WriteInt64(v int64)
    WriteFloat64(v float64)
    WriteBool(v bool)
    WriteRune(v rune)
    WriteNull()
    WriteStartObject()
    WriteEndObject()
    WriteStartArray()
    WriteEndArray()
}
```

Then create a codec type with an `Apply(meta StructMeta[T], ...)` method — the transpiler will auto-inject StructMeta for your format too.

#### StructMeta API

| Method | Signature | Description |
|--------|-----------|-------------|
| `NumFields` | `() int` | Number of fields in the struct |
| `FieldName` | `(i int) string` | Name of field at index i |
| `EncodeFields` | `(w FieldEncoder, t T, nameFn func(int) string, omitFn func(int) bool, naming func(string) string)` | Typed serialise of a T via the writer interface; `naming` propagates into nested struct dispatch |
| `DecodeFields` | `(r FieldDecoder, lookup func(string) int, naming func(string) string) T` | Typed construct of a T via the reader interface; `naming` propagates into nested struct dispatch |

### Regex

`Regex` provides regular expression support with pattern matching extractors. Located in the `regex` package.

```gala
import "martianoff/gala/regex"
import . "martianoff/gala/collection_immutable"
```

#### Compilation

```gala
// Safe compilation (returns Try[Regex])
val r = regex.Compile("\\d+")

// Panicking compilation (for known-good patterns)
val digits = regex.MustCompile("\\d+")
```

#### Matching and Searching

```gala
digits.Matches("abc123")          // true
digits.FindFirst("abc 123 def")   // Some("123")
digits.FindAll("a1 b2 c3")       // Array("1", "2", "3")
digits.ReplaceAll("a1b2", "X")   // "aXbX"
digits.Split("a1b2c3")           // Array("a", "b", "c")
```

#### Regex Pattern Matching with Array Destructuring

The `Unapply` method enables regex extractors in `match` expressions. Combined with `Array` sequence patterns, capture groups destructure into individual variables:

```gala
val dateRegex = regex.MustCompile("(\\d{4})-(\\d{2})-(\\d{2})")

val result = "2024-01-15" match {
    case dateRegex(Array(year, month, day)) => s"$year-$month-$day"
    case _ => "not a date"
}

val emailRegex = regex.MustCompile("([\\w.]+)@([\\w.]+)")
val parts = "user@example.com" match {
    case emailRegex(Array(user, domain)) => s"User: $user, Domain: $domain"
    case _ => "not an email"
}
```

### IO Effect

`IO[T]` is a lazy, composable effect type for separating pure and impure code. Located in the `io` package. Unlike `Lazy[T]` (which caches results), `IO[T]` re-executes on every `.Run()`.

```gala
import "martianoff/gala/io"
```

#### Creating IO Values

```gala
// Pure value (no side effects)
val pure = io.Of(42)

// Lazy thunk (panics caught as Failure)
val suspended = io.Suspend(expensiveComputation())

// Failing IO
val failing = io.Fail[int](errors.New("boom"))

// Side-effecting void operation
val logging = io.Effect(() => { Println("log message") })

// From existing Try
val fromTry = io.FromTry(Success(42))
```

#### Running IO

```gala
val result = pure.Run()        // Try[int] — Success(42)
val value = pure.UnsafeRun()   // int — 42 (panics on failure)
```

#### Composing IO

```gala
// Map: transform success value
val doubled = io.Map(io.Of(21), (x) => x * 2)

// FlatMap: chain dependent computations
val chained = io.FlatMap(io.Of(10), (x) => io.Of(x + 32))

// AndThen: sequence, discard first result
val program = io.AndThen(
    io.Effect(() => { Println("setup") }),
    io.Of("done"),
)

// Recover: handle errors
val safe = io.Recover(failing, (err) => -1)

// ForEach: side effect on success
io.ForEach(io.Of(42), (v) => { Println(v) }).Run()
```

#### IO vs Lazy

| | `Lazy[T]` | `IO[T]` |
|---|---|---|
| Re-execution | Cached — `.Get()` returns same result | Fresh — every `.Run()` re-executes |
| Thread safety | Yes (via `sync.Once`) | No memoization |
| Use case | Expensive pure computations | Side effects (HTTP, file I/O, logging) |

### Path

The `path` package wraps Go's `path/filepath` in a string-in / string-out
surface so consumers can reach for `path.Join(...)`, `path.Dir(...)`, etc.
without dropping back to the Go stdlib. Names mirror `filepath` so
migration is mechanical.

```gala
import . "martianoff/gala/path"
```

#### Surface

| Function | Signature | Notes |
|----------|-----------|-------|
| `Join` | `(parts ...string) string` | Variadic; joins with the OS separator. |
| `Dir` | `(p string) string` | Drops the last element. |
| `Base` | `(p string) string` | Returns the last element. |
| `Ext` | `(p string) string` | Returns the suffix at the final dot. |
| `IsAbs` | `(p string) bool` | Platform-aware absolute check. |
| `Abs` | `(p string) Try[string]` | Resolves against cwd; `Failure` if cwd is unreadable. |
| `ToSlash` | `(p string) string` | Replaces OS separators with `/`. Useful for printing. |
| `FromSlash` | `(p string) string` | Reverse of `ToSlash`. |
| `Clean` | `(p string) string` | Lexical normalisation (collapses `..`, `.`, repeated separators). |

```gala
val full = Join("workspace", "src", "main.go")
Println(ToSlash(Dir(full)))   // workspace/src
Println(Base(full))           // main.go
Println(Ext(full))            // .go
Println(IsAbs("relative"))    // false
val abs = Abs(".").GetOrElse("")
```

There is no `Path` type and no string wrapper — `path` operates on plain
strings so every call site is a one-line replacement for raw `filepath`.

### Filesystem

The `fs` package is the GALA wrapper around the parts of Go's `os` that
consumers reach for to read, write, and stat regular files. Every
fallible operation returns `Try[T]`; non-fallible convenience checks
return `bool`. Bytes flow through `Array[byte]` (immutable) at the
language boundary, with `ToGoSlice` / `ArrayFromSlice` round trips
internally at the Go-interop seam.

```gala
import . "martianoff/gala/fs"
```

#### Surface

| Function | Signature |
|----------|-----------|
| `ReadFile` | `(path string) Try[Array[byte]]` |
| `ReadFileString` | `(path string) Try[string]` |
| `WriteFile` | `(path string, data Array[byte], mode int) Try[bool]` |
| `WriteFileString` | `(path string, contents string, mode int) Try[bool]` |
| `Stat` | `(path string) Try[FileInfo]` |
| `Exists` | `(path string) bool` — non-fallible; `false` on any error |
| `MkdirAll` | `(path string, mode int) Try[bool]` |
| `MkdirTemp` | `(dir string, pattern string) Try[string]` — returns the created path |
| `RemoveAll` | `(path string) Try[bool]` — `Success` if path didn't exist |
| `ReadDir` | `(path string) Try[Array[FileInfo]]` |

`FileInfo` is the immutable snapshot returned by `Stat` and `ReadDir`:

```gala
struct FileInfo(
    Name    string,
    Size    int64,
    IsDir   bool,
    Mode    int,
    ModTime time_utils.Instant,
)
```

The raw `os.FileInfo` interface is intentionally not leaked.

```gala
import . "martianoff/gala/fs"
import "martianoff/gala/path"

val dir = MkdirTemp("", "scratch_").Get()
val target = path.Join(dir, "greeting.txt")
WriteFileString(target, "hello", 0o644).Get()
val info = Stat(target).Get()
Println(s"size=${info.Size} dir=${info.IsDir}")
val payload = ReadFileString(target).Get()
RemoveAll(dir).Get()
```

`Try`-returning ops chain naturally with `FlatMap` so a multi-step
filesystem flow short-circuits on the first failure:

```gala
val flow = MkdirTemp("", "x_")
    .FlatMap[string]((d) => WriteFileString(path.Join(d, "f"), "ok", 0o644)
        .Map((_) => d))
    .FlatMap[bool]((d) => RemoveAll(d))
```

Modes are passed as plain `int` (e.g. `0o644`, `0o755`); GALA does not
introduce a separate `FileMode` wrapper.

### Strings

The `strings` package is GALA's unified string library. One package, two surfaces:

- **Free functions** over plain Go `string` — `TrimSpace`, `Split`, `Contains`, etc. — mirroring Go's `strings.X` names exactly.
- **`Str`** — an immutable wrapper that caches the Go string and lazily materialises an `Array[rune]` for functional methods. Chainable: every transform returns a new `Str`.
- **`StringBuilder`** — a mutable, O(n)-assembly builder for hot-path concatenation.

```gala
import . "martianoff/gala/strings"
```

Pick the shape that fits the call site. Free functions are right for one-shots and `strings.X` migrations; `Str` is right for multi-step pipelines that benefit from caching the rune view. See [STRINGS.MD](STRINGS.MD) for the full reference.

#### Free function API

| Function | Signature | Notes |
|---|---|---|
| `Contains` | `(s, substr string) bool` | |
| `HasPrefix` | `(s, prefix string) bool` | |
| `HasSuffix` | `(s, suffix string) bool` | |
| `EqualFold` | `(a, b string) bool` | Unicode case-folding (rune-by-rune; ß≠"ss"). |
| `LastIndex` | `(s, substr string) int` | -1 when absent. |
| `TrimSpace` | `(s string) string` | |
| `Trim` | `(s, cutset string) string` | |
| `TrimLeft` | `(s, cutset string) string` | |
| `TrimRight` | `(s, cutset string) string` | |
| `TrimPrefix` | `(s, prefix string) string` | |
| `TrimSuffix` | `(s, suffix string) string` | |
| `ToLower` | `(s string) string` | |
| `ToUpper` | `(s string) string` | |
| `Repeat` | `(s string, count int) string` | |
| `Replace` | `(s, old, new string, n int) string` | `n < 0` ⇒ replace all. |
| `ReplaceAll` | `(s, old, new string) string` | |
| `Split` | `(s, sep string) Array[string]` | |
| `SplitN` | `(s, sep string, n int) Array[string]` | |
| `Fields` | `(s string) Array[string]` | |
| `Join` | `(parts Array[string], sep string) string` | |
| `JoinStrs` | `(strs Array[Str], sep string) Str` | `Str`-shaped sibling of `Join`. |

`Split`, `SplitN`, and `Fields` return a GALA `Array[string]` (from `collection_immutable`), not a Go `[]string`. `Join` accepts an `Array[string]` for symmetry. See `examples/strings_basics.gala` for a runnable tour of every function.

#### `Str` wrapper

`Str` adds chainable, immutable transformations on top of the free functions, plus a lazily evaluated rune view for functional ops. Construct with `S(...)`. Pattern-match with `NonEmptyStr(head, tail)` / `EmptyStr(_)`. Build incrementally with `NewStringBuilder()`. Full reference in [STRINGS.MD](STRINGS.MD).

#### Example

```gala
import . "martianoff/gala/strings"
import . "martianoff/gala/collection_immutable"

func main() {
    val ok = HasPrefix("/api/v1/users", "/api/")        // true
    val parts = Split("alpha,beta,gamma", ",")           // Array("alpha", "beta", "gamma")
    val joined = Join(ArrayOf("a", "b", "c"), " - ")     // "a - b - c"
    Println(s"$ok $parts $joined")

    val cleaned = S("  Hello, World!  ").Trim().ToLower().ReplaceAll(",", "")
    Println(cleaned.ToString())                          // hello world!
}
```

### Slices (Go Interop)

**Prefer GALA collections** (`Array`, `List`) over Go slices for most use cases. GALA collections provide rich functional APIs (Map, Filter, FoldLeft, ForEach, etc.) and are immutable by default. See [Immutable Collections](IMMUTABLE_COLLECTIONS.MD) for details.

Use Go slices only when interfacing with Go libraries or when you specifically need Go's `[]T` semantics. Native Go slice literals (`[]int{1,2,3}`) and `make()` are not supported in GALA.

```gala
// PREFERRED: Use GALA collections
import . "martianoff/gala/collection_immutable"

val nums = ArrayOf(1, 2, 3, 4, 5)       // Immutable Array with functional API
val names = ListOf("Alice", "Bob")       // Immutable List
val doubled = nums.Map((x) => x * 2)    // Functional operations
val sum = nums.FoldLeft(0, (acc, x) => acc + x)

// GO INTEROP: Use go_interop slice functions when you need []T
import . "martianoff/gala/go_interop"

val goSlice = SliceOf(1, 2, 3, 4, 5)    // Creates Go []int
val empty = SliceEmpty[int]()            // Creates Go []int{}
```

**When to use Go slices vs GALA collections:**

| Use Case | Recommendation |
|----------|---------------|
| General programming | `Array` or `List` from `collection_immutable` |
| Need Map/Filter/FoldLeft | `Array` or `List` (have full functional API) |
| Passing data to Go libraries | `SliceOf` from `go_interop`, or `.ToGoSlice()` on GALA collections |
| Variadic function arguments | Go slices (`[]T`) with `SliceOf` |
| Performance-critical Go interop | `SliceOf` / `SliceWithCapacity` from `go_interop` |

**Available Slice Functions** (from `go_interop` package):

| Function | Description |
|----------|-------------|
| `SliceOf(elements...)` | Create Go slice from values |
| `SliceEmpty[T]()` | Create empty Go slice |
| `SliceWithCapacity[T](cap)` | Empty slice with capacity |
| `SliceWithSize[T](size)` | Zero-initialized slice |
| `SliceWithSizeAndCapacity[T](size, cap)` | Slice with length and capacity |
| `SliceCopy(slice)` | Copy a slice |
| `SliceAppendAll(dst, src)` | Append all elements |
| `SlicePrepend(s, value)` | Insert at front |
| `SlicePrependAll(s, values)` | Prepend all elements |
| `SliceInsert(s, index, value)` | Insert at index |
| `SliceRemoveAt(s, index)` | Remove at index |
| `SliceDrop(s, n)` | Drop first n elements |
| `SliceTake(s, n)` | Take first n elements |

**Note:** Slice types (`[]T`) are still valid in function signatures and struct fields.

### Maps

GALA does not support direct Go map literals (`map[K]V{}`). Instead, use the type-safe collection types.

#### Recommended: Type-Safe HashMaps

For most use cases, use the collection types which provide rich functional APIs:

```gala
import "martianoff/gala/collection_immutable"
import "martianoff/gala/collection_mutable"

// Immutable HashMap (functional, thread-safe)
val m1 = collection_immutable.EmptyHashMap[string, int]()
val m2 = m1.Put("a", 1).Put("b", 2)  // Returns new map

// Mutable HashMap (imperative, single-threaded)
var m3 = collection_mutable.EmptyHashMap[string, int]()
m3.Put("a", 1)  // Modifies in place
```

#### Go Interoperability: std.Map Functions

When you need Go-native `map[K]V` for interoperability with Go libraries:

```gala
// Create and populate
var goMap = MapEmpty[string, int]()
goMap = MapPut(goMap, "key", 42)

// Query
val value, ok = MapGet(goMap, "key")
val exists = MapContains(goMap, "key")
val size = MapLen(goMap)

// Iterate
MapForEach(goMap, (k string, v int) => {
    fmt.Println(k, v)
})

// Iterate Go maps with complex value types (e.g., map[string][]string)
// V can be any type including slices — MapForEach[K comparable, V any] handles it
var headers = MapEmpty[string, []string]()
headers = MapPut(headers, "Accept", SliceOf("text/html", "application/json"))
MapForEach(headers, (k string, v []string) => {
    fmt.Printf("%s: %v\n", k, v)
})

// You can also use for-range directly on Go maps
for k, v := range headers {
    fmt.Printf("%s: %v\n", k, v)
}

// Convert between HashMap and Go map
val hashMap = collection_immutable.HashMapFromGoMap(goMap)
val backToGoMap = hashMap.ToGoMap()
```

**Available std.Map Functions:**

| Function | Description |
|----------|-------------|
| `MapEmpty[K, V]()` | Create empty map |
| `MapWithCapacity[K, V](n)` | Create map with capacity hint |
| `MapPut(m, k, v)` | Add/update entry, returns map |
| `MapGet(m, k)` | Get value and existence flag |
| `MapContains(m, k)` | Check if key exists |
| `MapDelete(m, k)` | Remove key, returns map |
| `MapLen(m)` | Get entry count |
| `MapForEach(m, fn)` | Iterate over entries |
| `MapKeys(m)` | Get all keys as slice |
| `MapValues(m)` | Get all values as slice |
| `MapCopy(m)` | Shallow copy |

**Note:** Map types (`map[K]V`) are still valid in function signatures and struct fields, similar to slices.

### HashMap

GALA provides both immutable and mutable HashMap implementations for key-value storage. Both support generic key and value types, with keys requiring the `comparable` constraint.

#### Immutable HashMap (collection_immutable)

The immutable HashMap uses a Hash Array Mapped Trie (HAMT) structure for effectively O(1) operations while maintaining immutability. All operations return new maps, leaving the original unchanged.

```gala
import . "martianoff/gala/collection_immutable"

// Creating maps
val m1 = EmptyHashMap[string, int]()
val m2 = m1.Put("apple", 1).Put("banana", 2).Put("cherry", 3)

// Using factory with tuple syntax
val m3 = HashMapOf[string, int](("a", 1), ("b", 2))

// From Go map
var goMap = make(map[string]int)
goMap["x"] = 100
val m4 = HashMapFromGoMap[string, int](goMap)

// Getting values
val opt = m2.Get("apple")           // Option[int]
val value = m2.GetOrElse("apple", 0) // int
val exists = m2.Contains("banana")   // bool

// Immutability: original map unchanged
val m5 = m2.Remove("banana")
fmt.Println(m2.Contains("banana"))  // true
fmt.Println(m5.Contains("banana"))  // false
```

**Key Methods:**

| Method | Description |
|--------|-------------|
| `Put(key, value)` | Add/update entry, returns new map |
| `Get(key)` | Get value as Option[V] |
| `GetOrElse(key, default)` | Get value or default |
| `Contains(key)` | Check if key exists |
| `Remove(key)` | Remove entry, returns new map |
| `PutAll(other)` | Merge another map |
| `Merge(other, f)` | Merge with custom combine function |
| `Filter(predicate)` | Filter entries |
| `MapValues[U](f)` | Transform values |
| `FoldLeft[U](init, f)` | Reduce to single value |
| `Keys()` | Get HashSet of keys |
| `Values()` | Get List of values |
| `ToGoMap()` | Convert to Go map |
| `Sorted()` | Sort entries by natural key order, returns `Array[Tuple[K, V]]` |
| `SortWith(less)` | Sort entries with custom comparator |
| `SortBy(f)` | Sort entries by key function |
| `MkString(sep)` | Join entries as `"key -> value"` with separator |

#### Mutable HashMap (collection_mutable)

The mutable HashMap uses hash buckets with O(1) average operations. It modifies the map in place for better performance when mutation is acceptable.

```gala
import . "martianoff/gala/collection_mutable"

// Creating maps
val m = EmptyHashMap[string, int]()
m.Put("apple", 1)
m.Put("banana", 2)
m.Put("cherry", 3)

// Advanced operations
m.PutIfAbsent("apple", 99)  // No change, key exists
m.Update("apple", (v int) => v * 2)  // Double apple's value

// Get or compute
val value = m.GetOrElseUpdate("date", () => {
    fmt.Println("Computing...")
    return 4
})

// In-place transformations
m.FilterInPlace((e Entry[string, int]) => e.Value > 1)
m.UpdateAll((k string, v int) => v * 10)

// Cloning for immutability when needed
val snapshot = m.Clone()
m.Clear()
fmt.Println(snapshot.Size())  // Still has data
```

**Key Methods:**

| Method | Description |
|--------|-------------|
| `Put(key, value)` | Add/update entry in place |
| `PutIfAbsent(key, value)` | Add only if key missing |
| `GetOrElseUpdate(key, f)` | Get or compute and store |
| `Update(key, f)` | Update value with function |
| `Remove(key)` | Remove entry in place |
| `Clear()` | Remove all entries |
| `FilterInPlace(predicate)` | Filter entries in place |
| `UpdateAll(f)` | Update all values in place |
| `Clone()` | Create a copy |
| `Merge(other, f)` | Merge another map |
| `Sorted()` | Sort entries by natural key order, returns `*Array[Tuple[K, V]]` |
| `SortWith(less)` | Sort entries with custom comparator |
| `SortBy(f)` | Sort entries by key function |
| `MkString(sep)` | Join entries as `"key -> value"` with separator |

#### Custom Key Types

For custom types as keys, implement the `Hashable` interface:

```gala
type Person struct {
    Name string
    Age  int
}

func (p Person) Hash() uint32 {
    return HashCombine(HashString(p.Name), HashInt(int64(p.Age)))
}

// Now Person can be used as a map key
val scores = EmptyHashMap[Person, int]()
val alice = Person(Name = "Alice", Age = 30)
val m = scores.Put(alice, 100)
```

#### Performance Characteristics

| Operation | Immutable HashMap | Mutable HashMap |
|-----------|------------------|-----------------|
| Put | O(log32 n) ≈ O(1) | O(1) amortized |
| Get | O(log32 n) ≈ O(1) | O(1) average |
| Remove | O(log32 n) ≈ O(1) | O(1) amortized |
| Contains | O(log32 n) ≈ O(1) | O(1) average |
| Size | O(1) | O(1) |

Choose immutable when you need:
- Thread safety without locks
- Undo/redo functionality
- Functional programming patterns

Choose mutable when you need:
- Maximum performance
- Frequent updates in a single thread
- Memory efficiency

## 10. Literals and Type Conversions

### Numeric Literals

Integer and floating-point literals match Go's literal syntax.

```gala
// Decimal integers
val n = 42

// Hexadecimal integers (0x or 0X prefix, mixed-case digits allowed)
val red    = 0xFF
val mask   = 0x1F
val color  = 0xDEADBEEF
val upper  = 0X1f     // uppercase prefix works too

// Floats
val pi     = 3.14
val scaled = 1.5e-3   // scientific notation
```

Hex literals transpile to Go as-is, so any valid Go hex integer is a valid GALA hex integer.

### String Literals
GALA supports Go-style string literals:

```gala
// Double-quoted strings with escape sequences
val greeting = "Hello, World!\n"
val tab = "col1\tcol2"
val quote = "She said \"hello\""

// Raw strings with backticks (no escape processing)
val raw = `C:\Users\path\to\file`
val multiLine = `line one
line two
line three`
```

**Escape sequences** in double-quoted strings:
| Escape | Character |
|--------|-----------|
| `\n` | Newline |
| `\t` | Tab |
| `\"` | Double quote |
| `\\` | Backslash |
| `\r` | Carriage return |

**Raw strings** (backtick-delimited) can span multiple lines and do not process escape sequences. They are useful for regular expressions, file paths, and multi-line text.

### String Interpolation

GALA supports string interpolation with two prefixes: `s` for auto-inferred formatting and `f` for explicit format control. No `import "fmt"` is needed — the transpiler auto-imports it.

#### `s"..."` — Auto-Inferred Format Verbs

Use `$variable` for simple references and `${expression}` for arbitrary expressions:

```gala
val name = "Alice"
val age = 30
val pi = 3.14159

Println(s"Hello $name!")                    // Hello Alice!
Println(s"$name is $age years old")         // Alice is 30 years old
Println(s"Pi ≈ $pi")                        // Pi ≈ 3.14159
Println(s"Next year: ${age + 1}")           // Next year: 31
Println(s"Price: $$99")                     // Price: $99 (use $$ for literal $)
```

Format verbs are chosen from the expression type:

| Type | Format verb |
|------|-------------|
| `string` | `%s` |
| `int`, `int64`, etc. | `%d` |
| `float64`, `float32` | `%g` |
| `bool` | `%t` |
| `rune` | `%c` |
| everything else | `%v` |

#### `f"..."` — Explicit Format Specs

Add a Go printf format specifier after `$variable` or `${expression}`:

```gala
val count = 7
val price = 19.99

Println(f"$count%04d items")                // 0007 items
Println(f"Total: $$$price%.2f")             // Total: $19.99
Println(f"Hex: $count%x")                   // Hex: 7
Println(f"${count * 100}%010d")             // 0000000700
```

If no `%spec` is given after a variable in an `f"..."` string, the format verb is auto-inferred (same as `s"..."`).

#### Nested Strings in `${...}`

String literals can be used freely inside `${...}` expressions — the lexer correctly tracks brace nesting and embedded quotes:

```gala
import "strings"
import . "martianoff/gala/collection_immutable"

// Function call with string argument
Println(s"result: ${strings.Repeat("ab", 3)}")         // result: ababab

// Multiple string arguments
Println(s"${strings.Replace("hello world", "world", "gala", 1)}")  // hello gala

// Collection methods with string separators
val nums = ArrayOf(1, 2, 3)
Println(s"nums: ${nums.MkString(", ")}")                // nums: 1, 2, 3

// Escaped quotes inside ${...} expressions
val sub = s"${r.CtxGet(\"jwt-sub\").GetOrElse(\"none\")}"
```

#### `Println` / `Print`

`Println` and `Print` are available without any import. They map directly to `fmt.Println` and `fmt.Print`:

```gala
// No import needed
Println("Hello, World!")
Println(s"Result: $x")
Print("no newline")
```

### Rune Literals
GALA supports Go-style character literals using single quotes:

```gala
val asterisk = '*'
val space = ' '
val newline = '\n'
val letterA = 'a'
val zero = '0'
```

Character literals produce `rune` values. Standard escape sequences are supported: `'\n'`, `'\t'`, `'\\'`, `'\''`.

Alternatively, you can use the `rune()` type conversion for integer codes:

```gala
val asterisk = rune(42)     // '*'
val letterA = rune(97)      // 'a'
```

### Type Conversions
GALA supports Go-style type conversions using function call syntax:

```gala
// Numeric conversions
val n = int64(42)
val f = float64(10)
val i = int(3.14)           // truncates to 3

// Rune/string conversions
val r = rune(65)            // int to rune: 'A'
val s = string(r)           // rune to string: "A"
val code = int(r)           // rune to int: 65

// String/byte conversions (use go_interop helpers — import . "martianoff/gala/go_interop")
val bytes = ToBytes("hello")  // string to byte slice
val str = ToString(bytes)     // byte slice to string
val runes = ToRunes("hello")  // string to rune slice
```

> **Important:** The Go syntax `[]byte(expr)` and `[]rune(expr)` are **not supported** by GALA's parser.
> Use `ToBytes()`, `ToString()`, and `ToRunes()` from the `go_interop` package instead.
> The reverse direction `string(bytes)` works natively since it looks like a function call.

**Note:** Type conversions are explicit in GALA — there is no implicit numeric widening or narrowing.

## 11. Go Built-in Functions

Since GALA transpiles to Go, Go's built-in functions are available. The following are commonly used:

### Available Built-in Functions

| Function | Description | Example |
|----------|-------------|---------|
| `len(x)` | Length of string, slice, array, or map | `len("hello")` → `5` |
| `cap(x)` | Capacity of slice | `cap(mySlice)` |
| `make(T, ...)` | Create slice, map, or channel | `make(map[string]int)` |
| `new(T)` | Allocate zeroed value, return pointer | `new(int)` |
| `append(s, v...)` | Append to slice | `append(items, 4)` |
| `delete(m, k)` | Delete map entry | `delete(myMap, "key")` |
| `close(ch)` | Close a channel | `close(done)` |
| `panic(v)` | Trigger a panic | `panic("error")` |
| `recover()` | Recover from panic (in defer) | `recover()` |

### GALA vs Go Built-ins

For most use cases, prefer GALA's collection types and their methods over Go built-ins:

| Operation | GALA (preferred) | Go built-in |
|-----------|-----------------|-------------|
| Collection length | `arr.Size()` / `list.Length()` | `len(slice)` |
| Add to collection | `arr.Append(x)` | `append(slice, x)` |
| Optional access | `arr.GetOption(i)` | `slice[i]` (may panic) |
| Map lookup | `map.Get(k)` → `Option[V]` | `v, ok := m[k]` |
| Create empty | `EmptyArray[int]()` | `make([]int, 0)` |

Use Go built-ins when:
- Working with Go slices (`[]T`) from interop
- Using `len()` on Go strings for byte length
- Using channels or other Go-specific features

```gala
import . "martianoff/gala/collection_immutable"

// PREFERRED: GALA collections
val nums = ArrayOf(1, 2, 3)
val size = nums.Size()         // 3
val first = nums.GetOption(0)  // Some(1)

// GO INTEROP: when you need Go built-ins
val goSlice = nums.ToSlice()
val byteLen = len("hello")     // 5 (byte length, not rune count)
```

## 12. Immutability Under the Hood

When GALA transpiles to Go, immutable values are wrapped in a `std.Immutable[T]` container to ensure safety and provide consistent semantics, especially for struct fields and global variables.

### Pointer Types and Immutability

GALA distinguishes between **pointer binding immutability** (whether the pointer itself can be reassigned) and **pointed-to data mutability** (whether the target data can be modified through the pointer).

#### val vs var Pointer Bindings

```gala
var data = 42

// Immutable pointer binding - pointer cannot be reassigned
val ptr1 = &data
*ptr1 = 100      // OK: can modify data through pointer
// ptr1 = &other // ERROR: cannot reassign immutable pointer

// Mutable pointer binding - pointer can be reassigned
var ptr2 = &data
*ptr2 = 200      // OK: can modify data
ptr2 = &other    // OK: can reassign pointer
```

#### Pointer Fields in Structs

For pointer fields in structs, you must consider whether the pointer needs to be reassigned after construction:

```gala
// WRONG: Immutable pointer field (default)
type BrokenNode struct {
    value int
    next *Node    // immutable pointer - cannot be reassigned!
}

// CORRECT: Mutable pointer field for linked structures
type Node struct {
    value int
    var next *Node  // mutable pointer - can traverse and modify
}
```

**Compile-time error:** GALA prevents assigning `nil` to an immutable pointer field, since it would be useless (forever nil):

```gala
type BadNode struct {
    next *Node  // immutable pointer
}

// ERROR: cannot assign nil to immutable pointer field 'next' - use 'var next' to make it mutable
val node = BadNode(next = nil)
```

#### Use Cases for Pointer Mutability

| Use Case | Pattern | Example |
|----------|---------|---------|
| Linked list/tree | `var next *Node` | Navigation requires reassigning pointers |
| Shared reference | `val ref *Data` | Multiple owners, pointer never changes |
| Optional data | `var data *T` | May be nil initially, assigned later |
| Cache/memo | `var cached *Result` | Lazily populated pointer |

#### Building Linked Structures with val Nodes

You can build linked structures using `val` node bindings:

```gala
// Node with immutable value but mutable pointer for navigation
type Node struct {
    value int
    var next *Node
}

// Build a linked list with immutable nodes
val node3 = Node(value = 30, next = nil)
val node2 = Node(value = 20, next = &node3)
val node1 = Node(value = 10, next = &node2)

// Traverse the list
var current *Node = &node1
for current != nil {
    fmt.Println(current.value)
    current = current.next
}
```

### ConstPtr - Read-Only Pointers

GALA provides `ConstPtr[T]` for read-only pointer semantics, similar to C++'s `const T*`. When you take the address of a `val` variable, you get a `ConstPtr[T]` instead of a raw `*T`. This prevents accidental modification of immutable data through pointers.

#### Basic Usage

```gala
val data = 42
val ptr = &data  // ptr is ConstPtr[int], not *int

// Read through ConstPtr using *ptr syntax
val value = *ptr  // Returns 42

// Compile error: cannot write through ConstPtr
// *ptr = 100  // ERROR: cannot assign through ConstPtr
```

#### Auto-Deref Field Access (Preferred Style)

When accessing fields through `ConstPtr`, use direct field access syntax. The transpiler automatically inserts `Deref()` calls:

```gala
val alice = Person(name = "Alice", age = 30)
val team = Team(leader = &alice, memberCount = 5)

// PREFERRED: Direct field access (auto-deref)
fmt.Println(team.leader.name)  // "Alice"
fmt.Println(team.leader.age)   // 30

// VERBOSE: Explicit Deref() - works but not recommended
fmt.Println(team.leader.Deref().name)  // "Alice"
```

This works with nested ConstPtr access too:

```gala
val node2 = ImmutableNode(value = 20, next = NewConstPtr[ImmutableNode](nil))
val node1 = ImmutableNode(value = 10, next = &node2)

// PREFERRED: Chain field access naturally
fmt.Println(node1.next.value)  // 20

// VERBOSE: Explicit Deref()
fmt.Println(node1.next.Deref().value)  // 20
```

#### Reading with *ptr Syntax (Preferred)

The `*` operator on `ConstPtr` works for reading, just like regular pointers:

```gala
val data = 42
val ptr = &data  // ConstPtr[int]

// PREFERRED: Use *ptr for primitives
val value = *ptr  // 42

val node = MutableNode(value = 100, next = nil)
val nodePtr = &node  // ConstPtr[MutableNode]

// PREFERRED styles for reading:
val v1 = *nodePtr            // Dereference to get the struct
val v2 = nodePtr.value       // Auto-deref field access
val v3 = (*nodePtr).value    // Explicit dereference then field

// VERBOSE (not recommended):
val v4 = nodePtr.Deref().value  // Explicit Deref() call

// Writing is blocked at compile time:
// *ptr = 100  // ERROR: cannot assign through ConstPtr
```

#### ConstPtr in Struct Fields

Use `ConstPtr[T]` for pointer fields when you want read-only access to pointed data:

```gala
type Person struct {
    name string
    var age int
}

type Team struct {
    leader ConstPtr[Person]  // Read-only pointer to leader
    var memberCount int
}

val alice = Person(name = "Alice", age = 30)
val team = Team(leader = &alice, memberCount = 5)

// Can read through ConstPtr using auto-deref
fmt.Println(team.leader.name)  // "Alice"
```

#### Passing ConstPtr to Functions

Functions can accept `ConstPtr[T]` parameters to guarantee they won't modify the pointed-to data:

```gala
// Function accepts read-only pointer - use auto-deref for field access
func getPersonName(p ConstPtr[Person]) string = p.name

// Function that explicitly requires mutable access
func incrementAge(p *Person) {
    p.age = p.age + 1
}

val alice = Person(name = "Alice", age = 30)
val name = getPersonName(&alice)  // OK: &val returns ConstPtr

// incrementAge(&alice)  // Would need explicit conversion or var binding
```

#### Building Immutable Data Structures

Use `ConstPtr` for truly immutable linked structures:

```gala
type ImmutableNode struct {
    value int
    var next ConstPtr[ImmutableNode]  // Read-only link to next
}

val node3 = ImmutableNode(value = 30, next = NewConstPtr[ImmutableNode](nil))
val node2 = ImmutableNode(value = 20, next = &node3)
val node1 = ImmutableNode(value = 10, next = &node2)

// Traverse using auto-deref field access
fmt.Println(node1.next.value)       // 20
fmt.Println(node1.next.next.value)  // 30
```

#### ConstPtr Methods

| Method | Description |
|--------|-------------|
| `Deref() T` | Read the pointed-to value (prefer `*ptr` syntax) |
| `IsNil() bool` | Check if pointer is nil |
| `NewConstPtr[T](p *T)` | Create ConstPtr from raw pointer |

#### Preferred Syntax Summary

| Operation | Preferred | Verbose (avoid) |
|-----------|-----------|-----------------|
| Dereference primitive | `*ptr` | `ptr.Deref()` |
| Access field | `ptr.field` | `ptr.Deref().field` |
| Explicit dereference | `(*ptr).field` | `ptr.Deref().field` |
| Nil check | `ptr.IsNil()` | - |

#### Nil Checking

```gala
val nilPtr = NewConstPtr[int](nil)
val validPtr = &someValue

fmt.Println(nilPtr.IsNil())    // true
fmt.Println(validPtr.IsNil())  // false
```

#### Comparison with C++

| C++ Concept | C++ Syntax | GALA Equivalent |
|-------------|------------|-----------------|
| Mutable pointer to mutable data | `T*` | `var ptr *T` |
| Const pointer to mutable data | `T* const` | `val ptr *T` (where target is `var`) |
| Mutable pointer to const data | `const T*` | `var ptr ConstPtr[T]` |
| Const pointer to const data | `const T* const` | `val ptr ConstPtr[T]` |

**Note:** Taking the address of a `val` variable (`&val`) automatically returns `ConstPtr[T]`, while taking the address of a `var` variable (`&var`) returns `*T`. This ensures that pointers to immutable data cannot be accidentally used to modify that data.

### val vs var with Go Functions

When calling Go functions that return values, `val` wraps the result in `std.Immutable[T]`. This is fine for values used only within GALA, but causes type mismatches when passing Go values back to other Go functions.

**Use `var` for Go multi-return values** when you need to pass the results back to Go APIs:

```gala
import "net/http"
import "io"

// CORRECT: Use var for Go multi-return values
var goReq, reqErr = http.NewRequest("GET", url, nil)
if reqErr != nil {
    return Left[ApiError, HttpResponse](NetworkError(Message = reqErr.Error()))
}
goReq.Header.Set("Content-Type", "application/json")  // goReq is raw *http.Request

var resp, doErr = http.DefaultClient.Do(goReq)  // Works: goReq is not wrapped
if doErr != nil {
    return Left[ApiError, HttpResponse](NetworkError(Message = doErr.Error()))
}
var bodyBytes, readErr = io.ReadAll(resp.Body)

// WRONG: val wraps in Immutable, breaking Go API calls
// val goReq, reqErr = http.NewRequest("GET", url, nil)
// http.DefaultClient.Do(goReq)  // ERROR: goReq is Immutable[*http.Request], not *http.Request
```

**When to use `val` vs `var` with Go functions:**

| Scenario | Use | Why |
|----------|-----|-----|
| Go value used only in GALA | `val` | Immutability safety, `.Get()` auto-unwrapped |
| Go value passed back to Go APIs | `var` | Avoids `Immutable[T]` wrapping, stays as raw Go type |
| Go `(value, error)` return | `var` | Both values stay as raw Go types |
| Go struct fields to mutate | `var` | Allows `goStruct.Field = newValue` |

## 13. GALA Packages

GALA supports importing other GALA packages. Since GALA transpiles to Go, a GALA package is essentially a Go package after transpilation. To import a GALA package, you use its Go import path.

### Multi-File Packages

A GALA package can consist of multiple `.gala` files. All files must declare the same package name. Types (structs, sealed types, interfaces) and functions defined in any file are visible to all other files in the package:

```gala
// types.gala
package shapes

struct Point(X int, Y int)

sealed type Shape {
    case Circle(Radius float64)
    case Rect(Width float64, Height float64)
}
```

```gala
// ops.gala
package shapes

import "fmt"

func (p Point) String() string = fmt.Sprintf("(%d, %d)", p.X, p.Y)

func Describe(s Shape) string = s match {
    case Circle(r) => fmt.Sprintf("Circle(%.1f)", r)
    case Rect(w, h) => fmt.Sprintf("Rect(%.0fx%.0f)", w, h)
}
```

In Bazel, the recommended way to generate and maintain the BUILD target is to
run `bazel run //:gazelle`. It discovers all `.gala` files in the package,
groups them by package name, and emits the `gala_library` target (`srcs`,
`importpath`, and `deps`) for you — including mixed GALA+GO packages, in the
same pass. GALA support ships from rules-gala as the `gala_gazelle` Bazel module
and uses the `gala imports` CLI subcommand as its parse-only import helper.

For reference, the generated target looks like:

```python
gala_library(
    name = "shapes",
    srcs = ["shapes/types.gala", "shapes/ops.gala"],
    importpath = "myapp/shapes",
)
```

Cross-file resolution supports: structs (with immutability flags), sealed types (with pattern matching), shorthand struct declarations, generic types, methods, and functions.

### Import Syntax

GALA uses Go-style import declarations. You can import multiple packages in a block or individually.

```gala
import "fmt"
import (
    "math"
    "martianoff/gala/examples/mathlib"
)
```

### Aliases and Dot Imports

You can use aliases to avoid name conflicts or to use a shorter name. Dot imports allow you to access symbols from a package without using the package prefix.

```gala
import m "math"
import . "martianoff/gala/examples/mathlib"

func main() {
    val res = m.Sqrt(16.0)
    val sum = Add(10, 20) // Add is from mathlib via dot import
}
```

> **Note:** If your package mixes `.gala` and `.go` files, dot imports may conflict with type aliases declared in `.go` files. Prefer GALA [type aliases](#type-aliases) over Go type aliases to avoid this. See the [Type Aliases](#type-aliases) section for details.

### Using Symbols from Other Packages

Types and functions from other packages are accessed using the package name (or alias) followed by a dot.

#### Generic Methods and Standalone Functions

GALA methods on generic structs are transpiled to standalone functions in Go (named `Struct_Method`). When calling these from another package, the transpiler handles the name resolution automatically. You can use the method call syntax, and it will be correctly transpiled.

```gala
package main

import (
    "fmt"
    "martianoff/gala/examples/lib"
)

func main() {
    val w = lib.Wrapper[int](V = 10)
    val w2 = w.Map((x int) => x + 1)
    fmt.Println(w2.V)
}
```

### Creating GALA Libraries

A GALA library is a reusable package that other GALA (or Go) projects can depend on.

#### Directory Structure

```
mylib/
  math_utils.gala     # package mathutils
  geom_utils.gala     # package mathutils (same package)
  BUILD.bazel
```

Each `.gala` file must declare the same package name. The package name does not need to match the directory name, but it is conventional.

#### Bazel Setup

Run `bazel run //:gazelle` to generate and maintain the `BUILD.bazel`. It emits
the `gala_library` target for the package; set the `# gazelle:gala_prefix`
directive to your module's import prefix so the generated `importpath` and
`deps` resolve correctly. GALA support is provided by the `gala_gazelle` Bazel
module from rules-gala, which uses the `gala imports` CLI subcommand as a
parse-only import helper. The directive reference (`gala_prefix`, `gala_helper`,
`gala_stdlib_prefix`, `gala_stdlib_repo`, `gala_implicit_dep`) lives in
rules-gala's `gazelle/README.md`.

For reference, the generated `gala_library` rule looks like:

```python
gala_library(
    name = "mathutils",
    srcs = ["math_utils.gala", "geom_utils.gala"],
    importpath = "github.com/user/project/mathutils",
    visibility = ["//visibility:public"],
)
```

- `importpath` — the Go import path consumers will use
- `visibility` — set to `["//visibility:public"]` to allow use from other packages

#### Consuming a Library

Add the import to your GALA source and re-run `bazel run //:gazelle`; it resolves
the import to the right target and adds it to `deps`. For reference, the
generated consumer target looks like:

```python
gala_binary(
    name = "app",
    srcs = ["main.gala"],
    deps = ["//mathutils"],
)
```

Then import it in GALA:

```gala
package main

import "github.com/user/project/mathutils"

func main() {
    Println(mathutils.Add(1, 2))
}
```

## 14. Embedding Files

GALA provides a native `embed val` declaration for embedding files into the compiled binary at compile time. This is GALA's alternative to Go's `//go:embed` directive, offering a type-safe, keyword-driven syntax.

### String Embed

Embed a single file as a `string`:

```gala
embed val readme = "README.md"
embed val config string = "config.json"
```

### EmbeddedFS

For directories, globs, or multiple patterns, use `EmbeddedFS` — a GALA wrapper around Go's `embed.FS` with a functional API:

```gala
embed val static EmbeddedFS = "static/*"
embed val assets EmbeddedFS = "templates/*.html", "static/*.css"
```

`EmbeddedFS` is defined in `std` and available without explicit import. Methods:

| Method | Signature | Description |
|--------|-----------|-------------|
| `ReadString` | `(path string) Try[string]` | Read file as string, returns `Try` |
| `ReadBytes` | `(path string) ([]byte, error)` | Read file as raw bytes (Go interop) |

Example usage:

```gala
embed val templates EmbeddedFS = "templates/*"

func loadTemplate(name string) string {
    val result = templates.ReadString(s"templates/$name")
    return result match {
        case Success(content) => content
        case Failure(err) => s"Error: $err"
    }
}
```

### Type Inference

| Pattern | Inferred Type |
|---------|---------------|
| Single file path (no glob) | `string` |
| Glob pattern (`*`, `?`) | `EmbeddedFS` |
| Multiple patterns | `EmbeddedFS` |

```gala
embed val readme = "README.md"          // string (single file)
embed val static = "static/*"           // EmbeddedFS (glob)
embed val all = "a/*", "b/*"            // EmbeddedFS (multiple patterns)
embed val icon EmbeddedFS = "icon.png"  // EmbeddedFS (explicit)
```

### Bazel Integration

In `BUILD.bazel`, pass embedded files via `embedsrcs`:

```bzl
gala_binary(
    name = "server",
    srcs = ["main.gala"],
    embedsrcs = glob(["templates/**", "static/**"]),
)
```

### Rules

- `embed val` is **top-level only** — cannot be used inside functions
- Embedded variables are **immutable** — cannot be reassigned
- The `embed` import is auto-injected by the transpiler (no explicit `import "embed"` needed)
- `EmbeddedFS` is in `std` — available everywhere without import

## 15. Testing

GALA provides a comprehensive test framework with 22 assertions, panic recovery, timing, table-driven test support, and benchmarking. Tests are collocated with source code and use familiar patterns.

### Test File Convention

Test files use the `_test.gala` suffix and are placed alongside source files:

```
mypackage/
  mycode.gala
  mycode_test.gala    # Test file (same package as source)
```

### Writing Tests

Test functions must:
- Declare the **same package** as their sibling `.gala` files in the directory (see *Package rule* below)
- Start with `Test` prefix (e.g., `TestAddition`)
- Take a single parameter of type `T` and return `T`
- Return the modified test context after assertions

#### Package rule

GALA enforces the same one-package-per-directory rule as Go. Every `.gala` file in a directory — including `_test.gala` files — must declare the same package. Mixing packages (for example, a `_test.gala` file declaring `package main` next to library files declaring `package mypackage`) is rejected with [GALA-E0010](errors/GALA-E0010.md), and the resulting Go output would not compile either.

This means there is exactly one valid in-directory test pattern: the **internal test**, where the test file declares the same package as the library it tests. Internal tests have full access to unexported identifiers, which is the same trade-off Go makes.

#### Internal Tests (Library Packages)

Internal tests use the same package as the library, allowing access to unexported identifiers:

```gala
package mypackage

import (
    . "martianoff/gala/test"
)

func TestInternalLogic(t T) T {
    // Can access unexported fields, functions, and types
    val result = internalHelper(42)
    return Eq(t, result, 84)
}
```

#### Standalone Tests (Separate Directory)

A standalone test program is a `package main` GALA file that imports the modules under test by their public API. Place it in its **own directory** (no library sources alongside it), so the package rule above is satisfied:

```
mymodule/
  mypackage/
    code.gala            # package mypackage
    code_test.gala       # package mypackage  (internal test)
  smoke_tests/
    smoke_test.gala      # package main       (standalone test, own directory)
```

```gala
// smoke_tests/smoke_test.gala
package main

import (
    . "martianoff/gala/test"
    . "mymodule/mypackage"
)

func TestAddition(t T) T {
    val x = 1 + 1
    return Eq(t, x, 2)
}
```

> **Migration note:** earlier versions of this guide showed `package main` test files living next to library sources. That layout was always invalid (it violates the per-directory package rule) and broke `gala test`; switch any such files to the internal pattern by renaming `package main` to the library's package name and dropping the now-redundant self-import.

### Panic Recovery

The test runner automatically recovers from panics in test functions and subtests. A panicking test is reported as failed with the panic message, but the runner continues executing remaining tests:

```
=== RUN   TestPanicking
    ERROR: PANIC: something went wrong
--- FAIL: TestPanicking (0.000s)
=== RUN   TestNextTest
--- PASS: TestNextTest (0.000s)
```

### Timing

Test output includes elapsed time for each test:
```
--- PASS: TestFast (0.001s)
--- FAIL: TestSlow (1.234s)
```

### Assertions

All assertions are free functions that return `T` for functional composition. Chain assertions by passing the result of one to the next:

```gala
func TestChained(t T) T {
    var t1 = Eq(t, 1 + 1, 2)
    var t2 = IsTrue(t1, true)
    return Contains(t2, "hello world", "hello")
}
```

#### Equality (5)

| Assertion | Description |
|-----------|-------------|
| `Eq(t, actual, expected)` | Deep equality via `std.Equal` |
| `NotEq(t, actual, expected)` | Not equal |
| `EqMsg(t, actual, expected, msg)` | Eq with custom message |
| `IsNil(t, value)` | Checks `value == nil` |
| `NotNil(t, value)` | Checks `value != nil` |

#### Boolean (2)

| Assertion | Description |
|-----------|-------------|
| `IsTrue(t, condition)` | Asserts true |
| `IsFalse(t, condition)` | Asserts false |

#### Comparison (4)

| Assertion | Description |
|-----------|-------------|
| `Greater(t, a, b)` | `a > b` via `std.CompareValues` |
| `GreaterOrEq(t, a, b)` | `a >= b` |
| `Less(t, a, b)` | `a < b` |
| `LessOrEq(t, a, b)` | `a <= b` |

```gala
func TestComparisons(t T) T {
    var t1 = Greater(t, 10, 5)
    var t2 = LessOrEq(t1, 5, 5)
    return Less(t2, "a", "b")
}
```

#### String (4)

| Assertion | Description |
|-----------|-------------|
| `Contains(t, haystack, needle)` | String contains substring |
| `NotContains(t, haystack, needle)` | String does not contain substring |
| `HasPrefix(t, s, prefix)` | String starts with prefix |
| `HasSuffix(t, s, suffix)` | String ends with suffix |

```gala
func TestStrings(t T) T {
    var t1 = Contains(t, "hello world", "world")
    return HasPrefix(t1, "hello world", "hello")
}
```

#### Option/Try (4)

| Assertion | Description |
|-----------|-------------|
| `IsSome(t, opt)` | Asserts Option is Some |
| `IsNone(t, opt)` | Asserts Option is None |
| `IsSuccess(t, try)` | Asserts Try is Success |
| `IsFailure(t, try)` | Asserts Try is Failure |

```gala
func TestOption(t T) T {
    var t1 = IsSome(t, Some(42))
    return IsNone(t1, None[int]())
}
```

#### Panic (2)

| Assertion | Description |
|-----------|-------------|
| `Panics(t, f)` | Asserts `f()` panics |
| `NotPanics(t, f)` | Asserts `f()` does not panic |

```gala
func doPanic() { panic("boom") }

func TestPanics(t T) T = Panics(t, doPanic)
```

#### Utility (1)

| Assertion | Description |
|-----------|-------------|
| `Fail(t, msg)` | Unconditional fail with message |

### Subtests

Use `t.Run()` to create subtests for better organization:

```gala
func TestMath(t T) T {
    var t1 = t.Run("addition", (sub T) => Eq(sub, 1 + 1, 2))
    return t1.Run("multiplication", (sub T) => Eq(sub, 2 * 3, 6))
}
```

### Table-Driven Tests

Use `Case[In, Out]` and `RunCases` for structured table-driven tests. Each case runs as a subtest with its own pass/fail reporting:

```gala
func TestDouble(t T) T {
    return RunCases[int, int](t,
        (sub T, input int, expected int) => Eq(sub, input * 2, expected),
        Case[int, int](Name = "zero", Input = 0, Expected = 0),
        Case[int, int](Name = "positive", Input = 5, Expected = 10),
        Case[int, int](Name = "negative", Input = -3, Expected = -6),
    )
}
```

Output:
```
=== RUN   TestDouble/zero
--- PASS: TestDouble/zero (0.000s)
=== RUN   TestDouble/positive
--- PASS: TestDouble/positive (0.000s)
=== RUN   TestDouble/negative
--- PASS: TestDouble/negative (0.000s)
--- PASS: TestDouble (0.000s)
```

### Benchmarking

GALA provides auto-calibrating benchmarks similar to Go's `testing.B`:

```gala
package main

import (
    . "martianoff/gala/test"
)

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

The benchmark runner auto-calibrates by doubling `N` until the elapsed time reaches 1 second, then reports ns/op:

```
=== STARTING BENCHMARKS ===
BenchmarkAdd                               1073741824              1 ns/op
=== BENCHMARKS DONE ===
```

### Test Context Methods

The `T` test context provides methods:
- `t.Run(name, func(T) T) T` - Run a subtest (with panic recovery)
- `t.Log(msg)` - Log a message
- `t.Error(msg) T` - Log error and mark test as failed
- `t.Fatal(msg)` - Log error and stop test execution (panics)
- `t.Fail() T` - Mark test as failed
- `t.Skip() T` - Skip the test
- `t.Name() string` - Get the test name
- `t.Failed() bool` - Check if test has failed
- `t.Skipped() bool` - Check if test was skipped

### BUILD.bazel Configuration

The recommended way to generate and maintain these `gala_test` targets is to run
`bazel run //:gazelle`, which discovers `*_test.gala` files and emits the
appropriate target (including for mixed GALA+GO packages) in one pass. The blocks
below show the rules it produces for reference and for cases you tune by hand.

#### Internal Tests (same package)

For tests collocated with the library — the standard pattern — use `pkg` to match the library package name and `lib_srcs` to include the library source files:

```python
load("@rules_gala//gala:defs.bzl", "gala_test")

_SRCS = glob(["*.gala"], exclude = ["*_test.gala"])

gala_test(
    name = "mypackage_test",
    srcs = glob(["*_test.gala"]),
    pkg = "mypackage",
    lib_srcs = _SRCS,
    deps = ["@gala//collection_immutable", "@gala//concurrent"],
)
```

The macro uses `go_test` with `embed` for internal tests, properly handling the package compilation that `go_binary` cannot support. Add any GALA stdlib packages your library imports to `deps` — the macro auto-adds `@gala//test` and `@gala//std`.

#### Standalone Tests (package main, separate directory)

For a `package main` test program that lives in its own directory and imports the library by its public path, omit `pkg` (it defaults to `"main"`):

```python
load("@rules_gala//gala:defs.bzl", "gala_test")

gala_test(
    name = "smoke_test",
    srcs = ["smoke_test.gala"],
    deps = ["//mypackage"],
)
```

Do not use this pattern for a `_test.gala` file that lives next to library sources — Bazel will still build it, but `gala test`, `go test`, and the GALA semantic checker all reject the mixed-package layout. See the *Package rule* note above.

#### Output Comparison Tests

For tests that compare program output against expected files, use `gala_exec_test`:

```python
load("@rules_gala//gala:defs.bzl", "gala_exec_test")

gala_exec_test(
    name = "output_test",
    src = "output_test.gala",
    expected = "output_test.out",
)
```

### Running Tests

#### Using `gala test` (recommended)

The `gala test` command automatically discovers `_test.gala` files, transpiles them, discovers `TestXxx` functions, generates a test runner, and executes tests — no manual wrappers or Bazel configuration needed.

```bash
gala test                     # Test current directory
gala test ./myproject         # Test specific directory
gala test -v                  # Verbose output
```

`gala test` handles both project types:
- **`package main` projects** (e.g. a single-binary CLI): source and test files are compiled together as `package main`.
- **Library packages and multi-package projects**: each directory's source and test files are compiled together in the **same package**, enabling internal tests that can access unexported identifiers. Under the hood, `gala test` generates a `_test.go` harness with `TestMain` per package and runs tests via `go test ./...`.

Test files must follow the *Package rule*: every `.gala` file in a directory declares the same package name. A `_test.gala` file declaring `package main` while its siblings declare a library package will be rejected with [GALA-E0010](errors/GALA-E0010.md). Standalone `package main` test programs belong in their own directory.

#### Using Bazel

For Bazel-based projects, use the `gala_test` and `gala_exec_test` macros (see [BUILD.bazel Configuration](#buildbazel-configuration) above).

##### Prerequisites

- **Bazel** (via [Bazelisk](https://github.com/bazelbuild/bazelisk)): manages Bazel versions automatically
- **Go SDK**: required for Go type inference (resolving function return types, struct fields, and method signatures from Go packages). Ensure `go` is on PATH or set the `GOROOT` environment variable.

##### Commands

Build the entire project:
```bash
bazel build //...
```

Run all tests:
```bash
bazel test //...
```

Run with verbose output (shows error details on failure):
```bash
bazel test //... --test_output=errors --verbose_failures
```

Run a specific test:
```bash
bazel test //mypackage:mycode_test
```

Run a specific example:
```bash
bazel test //examples:match_type_inference
```

#### Bazel Configuration

Transpilation genrules in `gala.bzl` use `tags = ["no-sandbox"]` to allow the transpiler filesystem access to the Go SDK for type inference via `go/importer`.

The transpiler needs `GOROOT` to find the Go SDK. Pass it explicitly when invoking Bazel:

```bash
GOROOT_PATH=$(go env GOROOT)
bazel build //... --action_env=GOROOT="$GOROOT_PATH" --action_env=PATH
```

> **Note**: The `.bazelrc` includes `build --action_env=GOROOT` which passes through the shell's GOROOT. This works when GOROOT is already set, but some environments (e.g., GitHub Actions `setup-go`) don't export it. Using `go env GOROOT` inline is always reliable. See [Go Type Inference Setup](#go-type-inference-setup-linuxci) for CI-specific guidance.

If the Go SDK is not found, transpilation still succeeds but Go type inference is disabled — a warning is printed to stderr. This only affects type resolution for Go stdlib and third-party packages; GALA's own type system works without it.

## 16. Best Practices

> **Quick reference:** [GALA_BEST_PRACTICES.MD](GALA_BEST_PRACTICES.MD) — the same rules in scannable form, with anchors back into this document.

### Immutability
- **Prefer `val` over `var`** - Use mutable variables only when necessary (accumulators, loop counters)
- **Use `Copy()` for updates** - Instead of mutating, create modified copies: `person.Copy(age = 31)`

### Optional Values in Immutable Fields
Use `Option[T]` instead of nil for fields that may have no value. This follows the Scala idiom where null is avoided entirely.

```gala
// BAD: Using nil with mutable field
type Node[T any] struct {
    value T
    var next func() Node[T]  // mutable just to allow nil
}
val end = Node[int](value = 0, next = nil)

// GOOD: Using Option with immutable field
type Node[T any] struct {
    value T
    next func() Option[Node[T]]  // immutable, returns None at end
}
val end = Node[int](value = 0, next = () => None[Node[T]]())
```

**Why this matters:**
- `val` (immutable) means "this value never changes" - it says nothing about whether the value can represent "nothing"
- `Option[T]` explicitly models "may or may not have a value" in the type system
- Avoids the billion-dollar mistake of null pointer errors
- Enables pattern matching: `case Some(node) =>` vs `case None =>`

**Common patterns:**
| Use Case | Pattern |
|----------|---------|
| Optional field | `val data Option[T]` |
| Lazy computation that may fail | `val compute func() Option[T]` |
| Optional callback | `val callback Option[func(T)]` |
| Linked list termination | `next func() Option[Node[T]]` |

### Functional Patterns
- **Use FoldLeft for accumulation** - Replace `var acc; for { acc = f(acc, x) }` with `FoldLeft(init, f)`. The accumulator type is inferred from the initial value
- **Keep loops for short-circuit ops** - `Exists`, `ForAll`, `Find`, `Contains` benefit from early exit
- **Chain operations** - `list.Filter((x) => x > 0).Map((x) => x * 2)` is clearer than nested loops

### Lambda Syntax
```gala
// Prefer implicit parameter types — the compiler infers them from context
list.Map((x) => x * 2)
list.Filter((x) => x > 0)
list.FoldLeft(0, (acc, x) => acc + x)
str.Exists((r) => r == 'a')

// Multiple statements - use block body with explicit return
list.Map((x) => {
    val doubled = x * 2
    return doubled + 1
})

// Explicit types only when inference is unavailable
val f = (x int) => x * 2
```

### Sealed Types
- **Prefer `sealed type` for fixed variant sets** - When a type has a known, closed set of variants, use sealed types instead of interfaces or manual discriminator fields. The standard library uses sealed types for `Option[T]`, `Either[A, B]`, and `Try[T]`
- **Exhaustive matching** - Match expressions covering all variants of a sealed type don't need `case _ =>`. The transpiler verifies exhaustiveness and reports missing variants
- **Use sealed types instead of iota enums** - `sealed type Color { case Red() case Green() case Blue() }` instead of `const ( Red = iota; Green; Blue )`

```gala
// BAD: Manual discriminator with iota
type Shape struct {
    kind int
    var radius float64
    var width float64
    var height float64
}

// GOOD: Sealed type with exhaustive matching
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}


val desc = shape match {
    case Circle(r) => f"radius=$r%.2f"
    case Rectangle(w, h) => s"${w}x${h}"
    case Point() => "point"
}
```

### Pattern Matching
- **Prefer `match` over if-else chains** for type/value dispatch
- **Use extractors** - `case Some(x) =>` not `if opt.IsDefined() { x := opt.Get() }`
- **Add guards for conditions** - `case n: int if n > 0 =>` not nested if inside case

### Type Inference
- **Omit type parameters when inferrable** - `Some(42)` not `Some[int](42)`, `ListOf(1, 2, 3)` not `ListOf[int](1, 2, 3)`
- **Omit lambda parameter types when inferrable** - `(x) => x * 2` not `(x int) => x * 2` when passed to a method with known parameter types
- **Omit variable types** - `val x = 42` not `val x int = 42`
- **Omit method type params** - `list.Map((x) => x * 2)` not `list.Map[int]((x int) => x * 2)`

### If-Expressions
- **Prefer if-expressions over var+mutation** - `val x = if (cond) a else b` instead of `var x = a; if (cond) { x = b }`
- **Block branches for complex logic** - `val url = if (q != "") { val e = encode(q); s"$base?$e" } else { base }` — last expression in each block is the result
- **Simple form for one-liners** - `val status = if (score > 50) "pass" else "fail"` — no braces needed

### Multi-Line Formatting
- **Use trailing commas** for multi-line parameter lists — `func f(\n    a string,\n    b int,\n) T`
- **Break long lines** (>100 chars) into multi-line with trailing commas for readability
- **Applies to functions, structs, and sealed type case fields**

### String Formatting
- **Prefer `s"..."` over `fmt.Sprintf`** - `s"Hello $name"` not `fmt.Sprintf("Hello %s", name)`. Format verbs are auto-inferred from types
- **Use `f"..."` for explicit format control** - `f"$price%.2f"` when you need specific formatting like padding or precision
- **Escaped quotes in `${...}`** - `s"${r.CtxGet(\"key\").GetOrElse(\"default\")}"` works — the interpolation parser handles escaped quotes inside expression blocks
- **Use `Println`/`Print`** - No `import "fmt"` needed for `Println(...)` and `Print(...)`
- **Only import `fmt` for specialized functions** - `fmt.Errorf`, `fmt.Fprintf`, `fmt.Stringer` still require the import

### Type Conversions
- **Use `go_interop` for byte/rune conversion** - `ToBytes("str")`, `ToString(bytes)`, `ToRunes("str")` instead of Go's `[]byte("str")` which is not supported in GALA syntax
- **FoldLeft/FoldRight — omit accumulator type** - `list.FoldLeft(0, (acc, x) => acc + x)` not `list.FoldLeft[int](0, (acc int, x int) => acc + x)`. The accumulator type is inferred from the zero value
- **Non-generic wrapper methods — omit types too** - `str.Filter((r) => r == 'a')` not `str.Filter((r rune) => r == 'a')`. Lambda params are inferred from concrete method signatures
- **Explicit types only when needed** - `None[int]()`, `Left[string, int]()`, empty collections, standalone lambdas not passed to a typed method, or ambiguous contexts
```gala
// Good - types inferred everywhere
val list = ListOf(1, 2, 3)
val doubled = list.Map((x) => x * 2)
val sum = list.FoldLeft(0, (acc, x) => acc + x)
val opt = Some("hello")
val length = opt.Map((s) => len(s))
val s = S("hello")
val upper = s.Exists((r) => r >= 'A' && r <= 'Z')

// Explicit types needed - no value to infer from
val empty = None[int]()
val fallback = Left[string, int]("error")
val f = (x int) => x * 2  // standalone lambda, no method context
```

### Library API Design

When designing library APIs, use concrete function types so that callers benefit from lambda parameter type inference:

```gala
// GOOD: Concrete param types — callers can write (req) => ...
type Handler func(Request) Response

func GET(path string, handler Handler) { ... }

// Usage — param type inferred from Handler
GET("/", (req) => Ok("hello"))
GET("/users", (req) => JsonResponse(getUsers()))
```

```gala
// BAD: 'any' params — callers must annotate lambda types
func GET(path string, handler func(any) any) { ... }

// Usage — explicit types required
GET("/", (req Request) => Ok("hello"))
```

Prefer function types with concrete types (not `any`) so that callers can omit type annotations on lambda parameters.

### Tuple Syntax
- **Prefer parenthesis syntax** - `(1, "hello")` not `Tuple[int, string](V1 = 1, V2 = "hello")`
- **Use for map entries** - `HashMapOf[K, V](("a", 1), ("b", 2))`
- **Unpack with pattern matching** - `case (a, b) =>` or `val (x, y) = tuple`

### Collections
- **Prefer GALA collections over Go slices** - Use `Array` or `List` from `collection_immutable` instead of Go `[]T` slices. GALA collections provide Map, Filter, FoldLeft, ForEach, ZipWithIndex, and other functional operations that Go slices lack
- **Use `go_interop` slices only for Go interop** - `SliceOf`, `SliceEmpty`, etc. live in the `go_interop` package and create Go `[]T` slices. Use them only when passing data to Go libraries or as variadic arguments
- **Convert between types** - Use `.ToGoSlice()` to convert GALA collections to Go slices, and `ArrayFromSlice()`/`ListFromSlice()` to go the other direction

| Operation | GALA Collections (preferred) | Go Slices (interop only) |
|-----------|------------------------------|--------------------------|
| Create | `ArrayOf(1,2,3)` / `ListOf(1,2,3)` | `SliceOf(1,2,3)` (from `go_interop`) |
| Tabulate | `ArrayTabulate(n, (i) => f(i))` | Manual `for` loop |
| Fill | `ArrayFill(n, value)` | Manual `for` loop |
| Empty | `EmptyArray[int]()` / `EmptyList[int]()` | `SliceEmpty[int]()` (from `go_interop`) |
| Transform | `arr.Map(f)`, `arr.Filter(p)` | Not available on `[]T` |
| Accumulate | `arr.FoldLeft(init, f)` | Manual `for` loop |
| Join | `arr.MkString(", ")` | Manual `for` loop |
| Iterate | `arr.ForEach(f)` | `for _, x := range slice` |
| Size | `arr.Size()` | `len(slice)` |
| Access | `arr.Get(i)` / `arr.GetOption(i)` | `slice[i]` (may panic) |

```gala
// GOOD: GALA collections with functional API
import . "martianoff/gala/collection_immutable"

val nums = ArrayOf(1, 2, 3, 4, 5)
val evens = nums.Filter((x) => x % 2 == 0)
val sum = nums.FoldLeft(0, (acc, x) => acc + x)

// AVOID: Go slices without functional API
import . "martianoff/gala/go_interop"

val nums = SliceOf(1, 2, 3, 4, 5)  // No .Map(), .Filter(), etc.
```

### Error Handling
- **Use `Option[T]`** for nullable values
- **Use `Try[T]`** for operations that may fail
- **Use `Either[E, T]`** when error type matters
- **Chain with `FlatMap`** for railway-oriented programming

### Naming
- **PascalCase** for types, exported functions/fields
- **camelCase** for local variables, unexported fields
- **Short lambda params** - `(x) => x * 2` in simple lambdas, descriptive names for complex ones

### Performance
- **Prefer `Array` over `List`** for random access (O(log32 n) vs O(n))
- **Prefer `List` for prepend-heavy** workloads (O(1) vs O(n))
- **Use `arrayBuilder`** when building arrays incrementally

## 17. Dependency Management

GALA provides a module system similar to Go modules for managing external dependencies.

### gala.mod vs go.mod

GALA projects use `gala.mod` instead of `go.mod` directly:

| Aspect | `gala.mod` | `go.mod` |
|--------|-----------|----------|
| Purpose | Declare GALA and Go dependencies together | Go-only dependency management |
| When to use | All GALA projects (recommended) | Pure Go projects or advanced overrides |
| Generates | `gala.sum`, and a clean `go.mod` automatically | N/A |

**Always prefer `gala.mod`** for GALA projects. It tracks which dependencies are GALA libraries vs plain Go libraries, and ensures `gala mod tidy` keeps everything in sync.

The workflow is: edit `gala.mod` (or use `gala mod add`) -> run `gala mod tidy` -> build with `gala build` or `bazel build`.

`gala mod tidy` generates a portable `go.mod` containing only pure Go dependencies. GALA stdlib dependencies (`martianoff/gala/std`, `collection_immutable`, etc.) are resolved automatically by `gala build` or by Bazel's bzlmod extension — they do not appear in `go.mod`.

> **Warning:** Do not run `go mod tidy` directly — it will remove dependencies that are only referenced by `.gala` files. Always use `gala mod tidy` instead.

### Quick Start

```bash
# Initialize a new module
gala mod init github.com/user/project

# Add a GALA dependency
gala mod add github.com/example/utils@v1.2.3

# Add a Go dependency
gala mod add github.com/google/uuid@v1.6.0 --go
```

### gala.mod File

The `gala.mod` file declares your module's dependencies:

```
module github.com/user/project

gala 1.0

require (
    github.com/example/utils v1.2.3
    github.com/google/uuid   v1.6.0 // go
)

replace github.com/example/utils => ../local-utils
```

### CLI Commands

| Command | Description |
|---------|-------------|
| `gala mod init` | Initialize a new gala.mod file |
| `gala mod add` | Add a dependency |
| `gala mod remove` | Remove a dependency |
| `gala mod update` | Update dependencies |
| `gala mod tidy` | Sync gala.mod with imports |
| `gala mod graph` | Print dependency tree |
| `gala mod verify` | Verify gala.sum hashes |

### Bazel Integration

Load dependencies from `gala.mod` in your WORKSPACE:

```python
load("@rules_gala//gala:repositories.bzl", "gala_dependencies")
gala_dependencies()
```

Then run `bazel run //:gazelle` to generate the BUILD targets and wire up `deps`
from the imports in your GALA sources. For reference, a generated target looks
like:

```python
gala_library(
    name = "mylib",
    src = "mylib.gala",
    importpath = "github.com/user/project/mylib",
    deps = ["@com_github_example_utils//:utils"],
)
```

For comprehensive documentation, see [DEPENDENCY_MANAGEMENT.MD](DEPENDENCY_MANAGEMENT.MD).

### Third-Party Go Dependencies

To add a third-party Go library (e.g., `github.com/google/uuid`):

```bash
# Add the dependency via gala CLI (handles gala.mod + go.mod + go.sum)
gala mod add github.com/google/uuid@v1.6.0 --go

# Sync all dependency files
gala mod tidy
```

Then in your Bazel config:
- `MODULE.bazel`: Add `use_repo(go_deps, "com_github_google_uuid")`
- `BUILD.bazel`: Add `@com_github_google_uuid//:uuid` to `deps`

Import in `.gala` files as you would in Go:
```gala
import "github.com/google/uuid"
```

> **Warning:** Always use `gala mod tidy` instead of `go mod tidy`. The Go tool removes
> dependencies that are only referenced by `.gala` files (not direct `.go` source),
> which will break your build.

---

## 18. Further Reading

- [Why GALA?](WHY_GALA.MD) - The feature-by-feature case for GALA, with side-by-side Go comparisons.
- [Design and Tradeoffs](DESIGN.MD) - Why GALA is built the way it is, and what each decision costs.
- [Examples](EXAMPLES.MD) - More examples of GALA code.
- [Concurrent](CONCURRENT.MD) - Future, Promise, and ExecutionContext for async programming.
- [Stream](STREAM.MD) - Lazy, potentially infinite sequences.
- [Strings](STRINGS.MD) - Free functions over `string`, the immutable `Str` wrapper, and `StringBuilder` — all in one package.
- [Time Utils](TIME_UTILS.MD) - Duration and Instant types for immutable time handling.
- [Immutable Collections](IMMUTABLE_COLLECTIONS.MD) - Array, List, HashMap, HashSet, TreeSet.
- [Mutable Collections](MUTABLE_COLLECTIONS.MD) - Mutable collection types.

## 19. IDE Support

GALA provides a full-featured GoLand/IntelliJ plugin and a Language Server Protocol (LSP) server for editor-agnostic IDE support. The plugin runs a local ANTLR parser for instant feedback, while the LSP server (`gala lsp`) connects to the transpiler for type-aware features.

### GoLand / IntelliJ IDEA

#### Installation

1. Install GALA CLI: Download from [releases](https://github.com/martianoff/gala/releases) and add to PATH
2. Install plugin: GoLand > Settings > Plugins > Install from Disk > select `gala-intellij-plugin.zip` from [releases](https://github.com/martianoff/gala/releases)
3. Restart GoLand — the LSP server starts automatically when a `.gala` file is opened

Alternatively, build the plugin from source:

```bash
bazel build //ide/intellij:plugin
# or: cd ide/intellij && gradle buildPlugin
```

The resulting ZIP file will be at `bazel-bin/ide/intellij/gala-intellij-plugin.zip`.

#### Plugin Features (local, no LSP needed)

The plugin includes a full ANTLR-based parser that builds a complete PSI tree, providing instant editor features without waiting for the LSP server:

- **Syntax highlighting** — keywords, types, strings, comments, operators, built-in functions, std types
- **Semantic annotator** — built-in types (`Option`, `Either`, `Try`, etc.), std types, built-in functions (`Println`, `SliceOf`, etc.), string interpolation variables
- **Code folding** — function/method blocks, sealed type bodies, import groups
- **Brace matching** — parentheses, brackets, braces
- **Structure view** — navigate functions, types, sealed types with their cases
- **Comment/uncomment** — toggle line comments with `Ctrl+/`
- **Live templates** — 12 templates for common patterns:

| Prefix | Expands to |
|--------|------------|
| `func` | Function declaration |
| `val` | Immutable variable |
| `var` | Mutable variable |
| `match` | Match expression |
| `if` | If expression |
| `for` | For loop |
| `sealed` | Sealed type declaration |
| `struct` | Struct declaration |
| `lambda` | Lambda expression |
| `main` | Main function |
| `println` | Println call |
| `sinterp` | String interpolation |

- **Color settings page** — customize all token colors via Settings > Editor > Color Scheme > GALA

#### LSP Features (via `gala lsp`)

The LSP server leverages the full transpiler pipeline for deep, type-aware analysis:

- **Real-time diagnostics** — parse errors, transpilation errors, unused variables, match exhaustiveness warnings
- **Hover** — type signatures with fields, methods, sealed cases, built-in function documentation
- **Go to Definition** — cross-file navigation via analyzer metadata, local declarations, pattern bindings, named arg fields
- **Find References** — find all occurrences in the same file
- **Completion** — type-aware dot completion (fields, methods), named argument fields, sealed case patterns with destructuring, keywords, built-in functions
- **Inlay hints** — displays inferred types from the transpiler's VarTypes for `val`, `var`, and other declarations
- **Document symbols** — types, functions, sealed variants in the document outline
- **Debounced analysis** — 500ms delay to prevent noise while typing; analysis runs on save or after idle

### VS Code

GALA works in VS Code via any LSP client plugin. Add to `.vscode/settings.json`:

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

This gives you diagnostics, hover, go-to-definition, completion, and inlay hints — all the LSP features listed above.

### Neovim

Using [nvim-lspconfig](https://github.com/neovim/nvim-lspconfig):

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

### Other Editors

Any editor with LSP support can use the GALA language server. Point your editor's LSP client at:

- **Command:** `gala lsp`
- **File types:** `gala`
- **Root markers:** `gala.mod`, `.git`

## 20. Performance Profiling

### Compilation Profiling

Enable detailed timing output for the transpilation pipeline by setting `GALA_PROFILE=1`:

```bash
GALA_PROFILE=1 gala transpile main.gala
```

This prints a per-phase breakdown to stderr:

```
=== GALA PROFILE: main.gala (total: 1.089s) ===
  parse          89ms    8.1%  ████
  analyze       983ms   90.2%  █████████████████████████████████████████████
  transform      17ms    1.6%
  generate        1ms    0.1%
```

The analyzer sub-phases are also shown:

```
  [analyze] sibling-discovery (6 files)   346ms
  [analyze] load-std                        3ms
  [analyze] scan-gala-imports               6ms
  [analyze] analyze-go-packages           619ms
  [analyze] scan-sibling-imports            8ms
  [analyze] collect-types                   0s
  [analyze] collect-methods-functions       1ms
  [analyze] extract-sibling-metadata        1ms
```

Cache hits/misses are reported when the disk cache is active:

```
    [cache] HIT   std                       1ms
    [cache] HIT   collection_immutable      2ms
    [cache] MISS  my_new_package            0s
```

### Analysis Cache

The transpiler caches analyzed package metadata on disk in `.gala/cache/`. The cache is keyed by a SHA-256 hash of all source files in the package, so it automatically invalidates when any file changes.

The cache stores the `RichAST` metadata (types, functions, companion objects, Go type info) but not the ANTLR parse tree. This avoids redundant re-analysis of imports like `std` and `collection_immutable` across separate transpilation processes.

**Cache location:** `.gala/cache/` in the project root (detected by walking up to `go.mod` or `gala.mod`).

**Clear the cache** if you suspect stale data:

```bash
rm -rf .gala/cache/
```

### Batch Transpilation

For packages with multiple source files, use `gala transpile-package` to transpile all files in a single process. This shares the analyzer's in-memory cache across files, avoiding redundant work:

```bash
gala transpile-package \
  --inputs types.gala,request.gala,server.gala \
  --outputs types.gen.go,request.gen.go,server.gen.go \
  --search /path/to/gala
```

The `gala_library`, `gala_binary`, and `gala_test` Bazel macros automatically use batch transpilation for multi-file packages via the `gala_transpile_package` rule.

#### Bazel Rule

```python
load("@rules_gala//gala:defs.bzl", "gala_transpile_package")

gala_transpile_package(
    name = "my_package_transpile",
    srcs = ["types.gala", "server.gala", "router.gala"],
    outs = ["types.gen.go", "server.gen.go", "router.gen.go"],
)
```

### Go Type Inference Setup (Linux/CI)

GALA's transpiler uses Go's `go/importer` to resolve types from Go stdlib and third-party packages. This requires the Go SDK to be discoverable via `GOROOT` (see [Bazel Configuration](#bazel-configuration)).

**Verify** Go type inference is working:

```bash
GALA_PROFILE=1 gala transpile myfile.gala 2>&1 | grep "Go SDK"
```

If you see "Go SDK not found", set GOROOT:

```bash
export GOROOT=$(go env GOROOT)
```

**GitHub Actions CI**: `setup-go` does not export GOROOT to subsequent steps. Resolve it inline with `go env GOROOT`:

```yaml
- name: Setup Go
  uses: actions/setup-go@v5
  with:
    go-version: '1.25'

- name: Build
  run: |
    GOROOT_PATH=$(go env GOROOT)
    bazel build //... --repo_env=GOROOT="$GOROOT_PATH" --action_env=GOROOT="$GOROOT_PATH" --action_env=PATH
```

Without Go type inference, GALA code that calls Go functions (e.g., `strconv.Atoi`, `bytes.NewBufferString`) may fail to infer return types. Pure GALA code and GALA stdlib types (`Option`, `Try`, etc.) work without it.

### Performance Tips

1. **Warm cache**: After the first build, subsequent builds of unchanged dependencies are ~10x faster.
2. **Batch transpilation**: Multi-file packages transpile ~7x faster in batch mode vs per-file.
3. **Minimize Go imports**: Each Go stdlib/third-party import triggers `go/importer` analysis (~100-600ms). Group Go interop in fewer files when possible.
4. **Profile first**: Always use `GALA_PROFILE=1` to identify the actual bottleneck before optimizing.
