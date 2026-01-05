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

    val res = if (y.IsDefined()) y.Get() else 0
    
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
