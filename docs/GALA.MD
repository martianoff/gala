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
The `match` expression provides powerful pattern matching. It currently requires a default case (`_`).

```gala
val result = x match {
    case 1 => "one"
    case 2 => "two"
    case _ => "other"
}
```

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

## 8. Immutability Under the Hood

When GALA transpiles to Go, immutable values are wrapped in a `std.Immutable[T]` container to ensure safety and provide consistent semantics, especially for struct fields and global variables.

## 9. GALA Packages

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

## Further Reading

- [Examples](examples.md) - More examples of GALA code.
