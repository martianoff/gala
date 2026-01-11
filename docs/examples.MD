# GALA Examples

This page contains examples demonstrating various features of the GALA language.

## Complete Example

The following example demonstrates many of GALA's features, including structs, immutability, expression functions, and control flow.

```gala
package main

import "fmt"

struct Point(X, Y int)

func moveX(p Point, delta int) Point = p.Copy(X = p.X + delta)

func main() {
    val p1 = Point(10, 20)
    val p2 = moveX(p1, 5)
    
    val msg = if (p2.X > 10) "moved" else "static"
    fmt.Println(msg, p2)
}
```

## Option Monad Example

```gala
package main

import "fmt"

func main() {
    val x = Some(10)
    val y = x.Map((v int) => v * 2)
    val z = None().GetOrElse(42)

    val res = y match {
        case Some(v) => v
        case _       => 0
    }
    
    fmt.Println(res, z)
}
```

## Generic Methods Example

```gala
package main

import "fmt"

type Box[T any] struct { Value T }

func (b Box[T]) Transform[U any](f func(T) U) Box[U] = Box[U](Value = f(b.Value))

func main() {
    val b = Box[int](Value = 10)
    val s = b.Transform((i int) => "Value is " + fmt.Sprintf("%d", i))
    fmt.Println(s.Value)
}
```

## Pattern Matching with Filters (Guards) Example

```gala
package main

import "fmt"

struct Person(Name string, Age int)

func main() {
    val people = []Person{
        Person("Alice", 25),
        Person("Bob", 15),
        Person("Charlie", 70),
    }

    for _, p := range people {
        val status = p match {
            case Person(name, age) if age < 18 => name + " is a minor"
            case Person(name, age) if age > 65 => name + " is a senior"
            case Person(name, _)               => name + " is an adult"
            case _                             => "Unknown"
        }
        fmt.Println(status)
    }
}
```

## Pattern Matching with Extractors Example

```gala
package main

import "fmt"

type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None[int]()

func main() {
    val number = 42
    
    val description = number match {
        case Even(n) => fmt.Sprintf("%d is an even number", n)
        case _       => fmt.Sprintf("%d is odd", number)
    }
    
    fmt.Println(description)
    
    // Nested patterns
    val opt = Some(10)
    opt match {
        case Some(Even(n)) => fmt.Println("Found some even number", n)
        case Some(n)       => fmt.Println("Found some odd number", n)
        case None()        => fmt.Println("Nothing found")
        case _             => fmt.Println("Other")
    }
}
```

## Type-Based Pattern Matching Example

```gala
package main

import "fmt"

func main() {
    val x any = "hello"
    
    val res = x match {
        case s: string => "string: " + s
        case i: int    => fmt.Sprintf("int: %d", i)
        case _         => "unknown"
    }
    
    fmt.Println(res)
}
```

## Generic Wildcard Pattern Matching Example

```gala
package main

import "fmt"

type Wrap[T any] struct { Value T }
func (w Wrap[T]) GetValue() any = w.Value

func main() {
    val w = Wrap[string](Value = "hello")
    val res = w match {
        case w1: Wrap[_] => fmt.Sprintf("Matched Wrap[_]: %v", w1.GetValue())
        case _ => "Other"
    }
    fmt.Println(res)
}
```

## Generic Type Pattern Matching Example

```gala
package main

import "fmt"

// Define a generic type
type Wrap[T any] struct {
    Value T
}

// Define an extractor object
type Wrapper struct {}
func (w Wrapper) Apply[T any](v T) Wrap[T] = Wrap[T](Value = v)
func (w Wrapper) Unapply(o any) Option[any] = o match {
    case wi: Wrap[_] => Some[any](wi.Value)
    case _           => None[any]()
}

func main() {
    // 1. Matching specific generic type
    val w1 = Wrap[int](Value = 42)
    val res1 = w1 match {
        case w: Wrap[int] => fmt.Sprintf("Matched Wrap[int]: %d", w.Value)
        case w: Wrap[string] => "Matched Wrap[string]: " + w.Value
        case _ => "Other"
    }
    fmt.Println(res1)

    // 2. Matching wildcard generic type
    val w2 = Wrap[string](Value = "hello")
    val res2 = w2 match {
        case w: Wrap[_] => "Matched Wrap[_]: " + fmt.Sprintf("%v", w.Value)
        case _          => "Other"
    }
    fmt.Println(res2)

    // 3. Using the generic extractor
    val w3 = Wrapper("GALA")
    val res3 = w3 match {
        case Wrapper(s: string) => "Extracted string: " + s
        case Wrapper(i: int) => fmt.Sprintf("Extracted int: %d", i)
        case _ => "Other"
    }
    fmt.Println(res3)
}
```

## Interface Example

```gala
package main

import "fmt"

type Shaper interface {
    Area() float64
}

struct Rect(width float64, height float64)
func (r Rect) Area() float64 = r.width * r.height

struct Circle(radius float64)
func (c Circle) Area() float64 = 3.14159 * c.radius * c.radius

func main() {
    val r = Rect(10, 5)
    val c = Circle(10)
    
    val s1 Shaper = r
    val s2 Shaper = c
    
    fmt.Println("Area 1:", s1.Area())
    fmt.Println("Area 2:", s2.Area())
}
```

## Apply Method Example

```gala
package main

import "fmt"

type Adder struct { Delta int }
func (a Adder) Apply(x int) int = x + a.Delta

type Multiply struct {}
func (m Multiply) Apply(x int, y int) int = x * y

func main() {
    val add5 = Adder(5)
    val res1 = add5(10) // 15
    
    val res2 = Multiply(3, 4) // 12
    
    fmt.Println(res1, res2)
}
```

## Using External Libraries

GALA allows you to organize your code into multiple packages and import them as needed.

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

You can also use dot imports to bring symbols into the current namespace:

```gala
import . "martianoff/gala/examples/mathlib"

func main() {
    val sum = Add(10, 20)
    fmt.Println("Sum is", sum)
}
```

## Advanced Type Inference Example

GALA uses the Hindley-Milner algorithm to infer types in complex scenarios, such as generic function calls. This enables features like automatic unwrapping of `Immutable` values even when they are returned from generic functions.

```gala
package main

import "fmt"

func identity[T any](x T) T = x
func getImm[T any](x T) Immutable[T] = NewImmutable(x)

func main() {
    // HM infers Immutable[int], which GALA then automatically unwraps to int
    var x = identity(getImm(42)) 
    fmt.Printf("x: %d\n", x)
}
```

## More Examples

You can find more examples in the `examples/` directory of the project:
- `complex.gala`: A more complex example showing pattern matching and `init()` function.
- `option_complex.gala`: Demonstrates advanced usage of the `Option` monad (`Map`, `FlatMap`, `Filter`).
- `hello.gala`: A simple "Hello, World!" example.
- `with_main.gala`: An example with a `main` function.
- `imports.gala`: Demonstrates importing standard Go packages with aliases and dot imports.
- `use_lib.gala`: Demonstrates importing another GALA package (`mathlib`).
- `tuple.gala`: Demonstrates using `Tuple[A, B]` with pattern matching.
- `either.gala`: Demonstrates using `Either[A, B]` with monadic operations and pattern matching.
