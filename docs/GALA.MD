# GALA Language Specification

GALA (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It combines Go's efficiency and simplicity with features inspired by Scala and other functional languages, such as immutability by default, pattern matching, and concise expression syntax.

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
val items = []int{1, 2, 3, 4}
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

val res = Implode([]string{"a", "b"}) // expanded to Implode{}.Apply([]string{"a", "b"})
```

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
The `match` expression provides powerful pattern matching, supporting literals, variable bindings, and extractors. GALA follows Scala semantics for pattern matching, where the pattern (or an extractor) is responsible for matching against the object. It currently requires a default case (`_`).

```gala
val result = x match {
    case 1 => "one"
    case 2 => "two"
    case n => "Value is " + fmt.Sprintf("%d", n) // Binding: n is bound to x
    case _ => "other"
}
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
val items = []int{1, 2, 3}
for i, v := range items {
    fmt.Printf("index %d: %d\n", i, v)
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

## 7. Functional Features

### Lambda Expressions
Lambdas use the `=>` syntax.

```gala
val f = (x int) => x * x
val g = (a int, b int) => {
    val sum = a + b
    return sum * sum
}
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
`Option[T]` is used for handling optional values safely.

```gala
val x Option[int] = Some(10)
val y Option[int] = None()

val result = x.Map((i int) => i * 2)
```

### Tuple
`Tuple[A, B]` represents a pair of values.

```gala
val t = Tuple[int, string](V1 = 1, V2 = "hello")
val msg = t match {
    case Tuple(a, b) => fmt.Sprintf("Got %d and %s", a, b)
    case _           => "Unknown"
}
```

### Either
`Either[A, B]` represents a value that can be one of two types. It is often used for error handling where `Left` is the error and `Right` is the success value.

```gala
val e: Either[int, string] = Right("success")
val msg = e match {
    case Left(code) => fmt.Sprintf("Error code: %d", code)
    case Right(s)   => "Result: " + s
    case _          => "Unknown"
}

// Monadic operations are biased towards Right
val length = e.Map((s string) => len(s))
```

### Try Monad
`Try[T]` represents a computation that may either succeed with a value of type T or fail with an error. It provides a functional approach to error handling, similar to Scala's Try monad.

```gala
import . "std"

// Create Try values
val success Try[int] = Success(42)
val failure Try[int] = Failure(NoSuchElementError(Message = "not found"))

// Pattern matching
val msg = success match {
    case Success(n) => fmt.Sprintf("Got: %d", n)
    case Failure(e) => fmt.Sprintf("Error: %s", e.Error())
    case _          => "Unknown"
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

## 10. Immutability Under the Hood

When GALA transpiles to Go, immutable values are wrapped in a `std.Immutable[T]` container to ensure safety and provide consistent semantics, especially for struct fields and global variables.

## 11. GALA Packages

GALA supports importing other GALA packages. Since GALA transpiles to Go, a GALA package is essentially a Go package after transpilation. To import a GALA package, you use its Go import path.

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
    val w2 = w.Map((x int) => x + 1) // Transpiles to lib.Wrapper_Map(w.Get(), ...)
    fmt.Println(w2.V)
}
```

Alternatively, you can call the transpiled function directly using its qualified name:

```gala
val w2 = lib.Wrapper_Map(w, (x int) => x + 1)
```

## 12. Testing

GALA provides a test framework following Go conventions. Tests are collocated with source code and use familiar patterns.

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
    . "martianoff/gala/std"  // Import the package being tested
)

func TestAddition(t T) T {
    val x = 1 + 1
    return Eq[int](t, x, 2)
}

func TestMultiplication(t T) T {
    val x = 2 * 3
    return Eq[int](t, x, 6)
}
```

### Assertions

The following assertions are available in the `test` package. Each assertion returns the modified test context `T`:
- `Eq[V](t, actual, expected) T` - Assert values are equal
- `NotEq[V](t, actual, expected) T` - Assert values are not equal
- `IsTrue(t, condition) T` - Assert condition is true
- `IsFalse(t, condition) T` - Assert condition is false

Chain assertions by passing the result of one to the next:

```gala
func TestChained(t T) T {
    var t1 = Eq[int](t, 1 + 1, 2)
    var t2 = IsTrue(t1, true)
    return IsFalse(t2, false)
}
```

### Subtests

Use `t.Run()` to create subtests for better organization:

```gala
func TestMath(t T) T {
    var t1 = t.Run("addition", (t T) => Eq[int](t, 1 + 1, 2))
    return t1.Run("multiplication", (t T) => Eq[int](t, 2 * 3, 6))
}
```

### Table-Driven Tests

GALA supports table-driven tests using subtests:

```gala
func TestDouble(t T) T {
    var t1 = t.Run("zero", (t T) => Eq[int](t, 0 * 2, 0))
    var t2 = t1.Run("positive", (t T) => Eq[int](t, 5 * 2, 10))
    return t2.Run("negative", (t T) => Eq[int](t, -3 * 2, -6))
}
```

### Test Context Methods

The `T` test context provides methods:
- `t.Run(name, func(T) T) T` - Run a subtest
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

## 13. Further Reading

- [Examples](examples.md) - More examples of GALA code.

## IDE Support

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
