# GALA Language Specification

GALA (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It combines Go's efficiency and simplicity with features inspired by Scala and other functional languages, such as immutability by default, pattern matching, and concise expression syntax.

---

## Table of Contents

1. [Project Structure](#1-project-structure)
2. [Variable Declarations](#2-variable-declarations)
3. [Functions](#3-functions)
4. [Types and Structs](#4-types-and-structs)
   - [Sealed Types (ADTs)](#sealed-types-algebraic-data-types)
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
   - [Slices](#slices)
   - [Maps](#maps)
   - [HashMap](#hashmap)
10. [Literals and Type Conversions](#10-literals-and-type-conversions)
11. [Go Built-in Functions](#11-go-built-in-functions)
12. [Immutability Under the Hood](#12-immutability-under-the-hood)
    - [ConstPtr - Read-Only Pointers](#constptr---read-only-pointers)
13. [GALA Packages](#13-gala-packages)
14. [Testing](#14-testing)
15. [Best Practices](#15-best-practices)
16. [Dependency Management](#16-dependency-management)
17. [Further Reading](#17-further-reading)
18. [IDE Support](#18-ide-support)

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

### Parameters
Function parameters can be explicitly marked as `val` or `var`. By default, they are `val` (immutable).

```gala
func process(val data string, var count int) {
    // data = "new" // Error
    count = count + 1 // OK
}
```

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

This generates:
- Parent struct `Shape` with all variant fields merged + `_variant` discriminator
- Empty companion structs `Circle{}`, `Rectangle{}`, `Point{}`
- `Apply` methods on each companion for construction
- `Unapply` methods for pattern matching
- `IsCircle()`, `IsRectangle()`, `IsPoint()` methods on `Shape`

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
GALA supports `if` both as a statement and as an expression.

```gala
// If statement
if (x > 10) {
    fmt.Println("large")
} else {
    fmt.Println("small")
}

// If expression
val status = if (score > 50) "pass" else "fail"
```

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
    case Some(v) => fmt.Sprintf("Got: %d", v)
    case None()  => "Empty"
}

// Monadic operations
val result = x.Map((i int) => i * 2)
```

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
    case (a, b) => fmt.Sprintf("Got %d and %s", a, b)
    case _      => "Unknown"
}

// Tuple destructuring in val declarations
val (a, b) = t         // a = 1, b = "hello"
val (x, y, z) = triple // x = 1, y = "hello", z = true

// Accessing fields directly
val first = t.V1   // 1
val second = t.V2  // "hello"
```

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
    case Left(code) => fmt.Sprintf("Error code: %d", code)
    case Right(s)   => "Result: " + s
}

// Monadic operations are biased towards Right
val length = e.Map((s string) => len(s))

// FlatMap chains computations that return Either
val result = Right[string, int](5).FlatMap((x int) => Right[string, int](x + 100))

// Chaining Map and FlatMap
val chained = Right[string, int](3).Map((x int) => x * 10).FlatMap((x int) => Right[string, int](x + 1))
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

// Try: safely execute code that may panic
val result = Try[int](() => riskyDivide(10, 0))  // Failure (catches panic)
val safe = Try[int](() => riskyDivide(10, 2))    // Success(5)

// Pattern matching (exhaustive - no case _ needed)
val msg = success match {
    case Success(n) => fmt.Sprintf("Got: %d", n)
    case Failure(e) => fmt.Sprintf("Error: %s", e.Error())
}

// Monadic operations
val doubled = success.Map[int]((n int) => n * 2)
val result = success.FlatMap[int]((n int) => divide(n, 2))

// Recovery
val recovered = failure.Recover((e error) => 0)
val recoveredWith = failure.RecoverWith((e error) => Success(0))

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
| `IsSuccess()` / `IsFailure()` | Check the state |
| `Get()` | Get value or panic |
| `GetOrElse(default)` | Get value or return default |
| `OrElse(alternative)` | Return this if Success, otherwise alternative |
| `Map[U](f)` | Transform value if Success |
| `FlatMap[U](f)` | Chain operations returning Try |
| `Filter(predicate)` | Keep Success if predicate holds |
| `Recover(f)` | Recover from Failure with a value |
| `RecoverWith(f)` | Recover from Failure with a new Try |
| `Transform[U](onSuccess, onFailure)` | Handle both cases |
| `Fold[U](onFailure, onSuccess)` | Reduce to a single value |
| `ToOption()` | Convert to Option |
| `ToEither()` | Convert to Either[error, T] |

### Future Monad

`Future[T]` represents an asynchronous computation that will eventually produce a value of type T or fail with an error. It provides a functional approach to concurrent programming, similar to Scala's Future monad.

```gala
import . "martianoff/gala/concurrent"

// Create Futures
val immediate = FutureOf[int](42)                    // Already completed with value
val async = FutureApply[int](() => expensiveComputation())  // Runs asynchronously

// Blocking operations
val result = async.Await()           // Returns Try[int]
val value = async.GetOrElse(0)       // Returns int, default on failure

// Monadic operations
val doubled = async.Map[int]((v int) => v * 2)
val chained = async.FlatMap[string]((v int) => fetchName(v))

// Pattern matching
val msg = async match {
    case Succeeded(v) => fmt.Sprintf("Got: %d", v)
    case Failed(e) => fmt.Sprintf("Error: %s", e.Error())
    case _ => "Unknown"
}
```

For comprehensive documentation including Promise, ExecutionContext, sequence operations, and all methods, see [Concurrent](CONCURRENT.MD).

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

// String/byte conversions
val bytes = []byte("hello") // string to byte slice
val str = string(bytes)     // byte slice to string
```

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

In Bazel, specify all source files with `srcs`:

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

## 14. Testing

GALA provides a comprehensive test framework with 22 assertions, panic recovery, timing, table-driven test support, and benchmarking. Tests are collocated with source code and use familiar patterns.

### Test File Convention

Test files should be named with the `_test.gala` suffix or placed in a `tests/` subdirectory:

```
mypackage/
  mycode.gala
  tests/
    main.gala    # Test file with package main
```

### Writing Tests

Test functions must:
- Use `package main` and import the modules being tested
- Start with `Test` prefix (e.g., `TestAddition`)
- Take a single parameter of type `T` and return `T`
- Return the modified test context after assertions

```gala
package main

import (
    . "martianoff/gala/test"
)

func TestAddition(t T) T {
    val x = 1 + 1
    return Eq(t, x, 2)
}

func TestMultiplication(t T) T {
    val x = 2 * 3
    return Eq(t, x, 6)
}
```

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

Use the `gala_go_test` macro to define tests:

```python
load("//:gala.bzl", "gala_go_test")

gala_go_test(
    name = "mycode_test",
    srcs = ["main.gala"],
    deps = [":mypackage"],
)
```

### Output Comparison Tests

For tests that compare program output against expected files, use `gala_test`:

```python
load("//:gala.bzl", "gala_test")

gala_test(
    name = "output_test",
    src = "output_test.gala",
    expected = "output_test.out",
)
```

### Running Tests

Run all tests:
```bash
bazel test //...
```

Run specific tests:
```bash
bazel test //mypackage:mycode_test
```

## 15. Best Practices

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
    case Circle(r) => fmt.Sprintf("radius=%.2f", r)
    case Rectangle(w, h) => fmt.Sprintf("%fx%f", w, h)
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

## 16. Dependency Management

GALA provides a module system similar to Go modules for managing external dependencies.

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
load("@gala//:gala_deps.bzl", "gala_dependencies")
gala_dependencies()
```

Then reference them in BUILD files:

```python
gala_library(
    name = "mylib",
    src = "mylib.gala",
    importpath = "github.com/user/project/mylib",
    deps = ["@com_github_example_utils//:utils"],
)
```

For comprehensive documentation, see [DEPENDENCY_MANAGEMENT.MD](DEPENDENCY_MANAGEMENT.MD).

---

## 17. Further Reading

- [Examples](EXAMPLES.MD) - More examples of GALA code.
- [Concurrent](CONCURRENT.MD) - Future, Promise, and ExecutionContext for async programming.
- [Stream](STREAM.MD) - Lazy, potentially infinite sequences.
- [String Utils](STRING_UTILS.MD) - Rich, immutable string operations with functional programming support.
- [Time Utils](TIME_UTILS.MD) - Duration and Instant types for immutable time handling.
- [Immutable Collections](IMMUTABLE_COLLECTIONS.MD) - Array, List, HashMap, HashSet, TreeSet.
- [Mutable Collections](MUTABLE_COLLECTIONS.MD) - Mutable collection types.

## 18. IDE Support

### IntelliJ IDEA
A basic IntelliJ IDEA plugin is available in `ide/intellij`.

#### How to Build and Install
1. Build the plugin using Bazel from the project root:
   ```bash
   bazel build //ide/intellij:plugin
   ```
   The resulting ZIP file will be at `bazel-bin/ide/intellij/gala-intellij-plugin.zip`.
2. Alternatively, build using Gradle in `ide/intellij`:
   ```bash
   gradle buildPlugin
   ```
3. Install the resulting ZIP file into IntelliJ via `Settings > Plugins > Install Plugin from Disk...`.

The plugin provides:
- Syntax highlighting
- File type recognition (`.gala`)
- Basic code structure support
