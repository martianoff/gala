# GALA Language Specification

GALA (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It combines Go's efficiency and simplicity with features inspired by Scala and other functional languages, such as immutability by default, pattern matching, and concise expression syntax.

## 1. Project Structure

GALA files use the `.gala` extension. Every file must start with a package declaration, followed by an empty line.

GALA supports Go-style imports, including aliases and dot imports. Import declarations must also be followed by an empty line.

```gala
package main

import (
    "fmt"
    m "math"
    . "net/http"
)
```

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

### Methods
GALA supports methods on structs. Methods are declared by providing a receiver before the function name. GALA methods can have their own type parameters, a feature not natively supported by Go.

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

## 5. Control Flow

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
The `match` expression provides powerful pattern matching, supporting literals, variable bindings, and extractors. GALA follows Scala semantics for pattern matching, where the pattern (or an extractor) is responsible for matching against the object. It currently requires a default case (`_`).

```gala
val result = x match {
    case 1 => "one"
    case 2 => "two"
    case n => "Value is " + fmt.Sprintf("%d", n) // Binding: n is bound to x
    case _ => "other"
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

You can also define custom extractors by implementing an `Unapply` method that returns an `Option[T]`, a `bool`, or multiple values followed by a `bool`.

```gala
type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None[int]()

val description = number match {
    case Even(n) => fmt.Sprintf("%d is even", n)
    case _       => "odd"
}
```

Extractors can be nested: `case Some(Even(n)) => ...`. You can use the underscore `_` to skip variable bindings in any extractor: `case Some(_) => "Got something"`.

### For Statement
*Note: The `for` statement is currently under development and not yet fully supported in the transpiler.*

```gala
for i := 0; i < 10; i = i + 1 {
    fmt.Println(i)
}
```

## 6. Functional Features

### Lambda Expressions
Lambdas use the `=>` syntax.

```gala
val f = (x int) => x * x
val g = (a int, b int) => {
    val sum = a + b
    return sum * sum
}
```

## 7. Generics

GALA supports generics using square brackets `[]`.

```gala
func identity[T any](x T) T = x

type Box[T any] struct {
    Value T
}
```

## 8. Option Monad

GALA provides a built-in `Option[T]` monad for handling optional values safely, similar to Scala.

### Creating Options
Use `Some(v)` to create an option with a value, and `None()` for an empty option.

```gala
val x Option[int] = Some(10)
val y Option[int] = None()
```

### Monadic Operations
`Option` provides common monadic operations like `Map`, `FlatMap`, and `Filter`.

```gala
val result = Some(10).Map((x int) => x * 2) // Some(20)
val empty = None().Map((x int) => x * 2)   // None()
```

### Accessing Values
Use `GetOrElse` to safely access the value with a default.

```gala
val value = x.GetOrElse(0)
```

## 9. Immutability Under the Hood

When GALA transpiles to Go, immutable values are wrapped in a `std.Immutable[T]` container to ensure safety and provide consistent semantics, especially for struct fields and global variables.

## 10. GALA Packages

GALA supports importing other GALA packages. Since GALA transpiles to Go, a GALA package is essentially a Go package after transpilation. To import a GALA package, you use its Go import path.

**Example:**

`mathlib/math.gala`
```gala
package mathlib

func Add(a int, b int) int = a + b
```

`main.gala`
```gala
package main

import (
    "fmt"
    "martianoff/gala/examples/mathlib"
)

func main() {
    val sum = mathlib.Add(10, 20)
    fmt.Println("Sum is", sum)
}
```

## 11. Testing

GALA supports Bazel for building and testing. You can use the `gala_test` macro in your `BUILD.bazel` files to define tests that compare the execution output of a GALA program against an expected output file.

```python
load("//:gala.bzl", "gala_test")

gala_test(
    name = "my_test",
    src = "my_test.gala",
    expected = "my_test.out",
)
```

To run all tests:
```bash
bazel test //...
```

## Further Reading

- [Examples](examples.md) - More examples of GALA code.
