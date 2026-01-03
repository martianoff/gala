# GALA Language Specification

GALA (Go Alternative LAnguage) is a modern programming language that transpiles to Go. It combines Go's efficiency and simplicity with features inspired by Scala and other functional languages, such as immutability by default, pattern matching, and concise expression syntax.

## 1. Project Structure

GALA files use the `.gala` extension. Every file must start with a package declaration.

GALA supports Go-style imports, including aliases and dot imports.

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
Struct fields are **immutable by default**. To make a field mutable, use the `var` keyword.

```gala
type Person struct {
    Name string    // Immutable by default
    var Age int    // Mutable
}

func main() {
    p := Person{Name: "Alice", Age: 30}
    // p.Name = "Bob" // Compile error
    p.Age = 31        // OK
}
```

You can also explicitly use `val` for fields, though it's the default.

```gala
type Config struct {
    val ID string
    var Timeout int
}
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
