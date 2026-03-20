# GALA Examples

This page contains examples demonstrating various features of the GALA language.

## Complete Example

The following example demonstrates many of GALA's features, including structs, immutability, expression functions, and control flow.

```gala
package main


struct Point(X, Y int)

func moveX(p Point, delta int) Point = p.Copy(X = p.X + delta)

func main() {
    val p1 = Point(10, 20)
    val p2 = moveX(p1, 5)
    
    val msg = if (p2.X > 10) "moved" else "static"
    Println(msg, p2)
}
```

## Named Arguments

Function calls support named arguments in any order. The compiler reorders them to match the function signature.

```gala
package main

func describe(name string, age int, role string) string =
    s"$name ($role, age $age)"

func main() {
    Println(describe("Alice", 30, "engineer"))                   // positional
    Println(describe(role = "designer", name = "Bob", age = 25)) // named, any order
}
```

## Default Parameter Values

Functions can have default parameter values. Callers can omit trailing defaults or use named arguments to skip specific parameters.

```gala
package main

func greet(name string, greeting string = "Hello", punctuation string = "!") string =
    s"$greeting, $name$punctuation"

func add(a int, b int = 10) int = a + b

func main() {
    Println(greet("World"))                     // Hello, World!
    Println(greet("World", "Hi"))               // Hi, World!
    Println(greet("World", "Hey", "..."))       // Hey, World...
    Println(greet("World", punctuation = "?"))  // Hello, World?
    Println(greet(name = "World", greeting = "Yo")) // Yo, World!
    Println(add(5))                             // 15
    Println(add(5, 20))                         // 25
}
```

Default expressions are evaluated at each call site:

```gala
package main

var counter = 0

func nextId() int {
    counter = counter + 1
    return counter
}

func createItem(name string, id int = nextId()) string =
    s"$name#$id"

func main() {
    Println(createItem("A"))      // A#1
    Println(createItem("B"))      // B#2
    Println(createItem("C", 99))  // C#99
}
```

## Option Monad Example

```gala
package main


func main() {
    val x = Some(10)
    val y = x.Map((v) => v * 2)         // parameter type inferred as int
    val z = None().GetOrElse(42)

    val res = y match {
        case Some(v) => v
        case _       => 0
    }
    
    Println(res, z)
}
```

## Generic Methods Example

```gala
package main


type Box[T any] struct { Value T }

func (b Box[T]) Transform[U any](f func(T) U) Box[U] = Box[U](Value = f(b.Value))

func main() {
    val b = Box[int](Value = 10)
    val s = b.Transform((i int) => s"Value is $i")
    Println(s.Value)
}
```

## Pattern Matching with Filters (Guards) Example

```gala
package main


struct Person(Name string, Age int)

func main() {
    val people = SliceOf(
        Person("Alice", 25),
        Person("Bob", 15),
        Person("Charlie", 70),
    )

    for _, p := range people {
        val status = p match {
            case Person(name, age) if age < 18 => name + " is a minor"
            case Person(name, age) if age > 65 => name + " is a senior"
            case Person(name, _)               => name + " is an adult"
            case _                             => "Unknown"
        }
        Println(status)
    }
}
```

## Pattern Matching with Extractors Example

```gala
package main


type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None[int]()

func main() {
    val number = 42
    
    val description = number match {
        case Even(n) => s"$n is an even number"
        case _       => s"$number is odd"
    }
    
    Println(description)
    
    // Nested patterns
    val opt = Some(10)
    opt match {
        case Some(Even(n)) => Println("Found some even number", n)
        case Some(n)       => Println("Found some odd number", n)
        case None()        => Println("Nothing found")
        case _             => Println("Other")
    }
}
```

## Type-Based Pattern Matching Example

```gala
package main


func main() {
    val x any = "hello"
    
    val res = x match {
        case s: string => s"string: $s"
        case i: int    => s"int: $i"
        case _         => "unknown"
    }
    
    Println(res)
}
```

## Generic Wildcard Pattern Matching Example

```gala
package main


type Wrap[T any] struct { Value T }
func (w Wrap[T]) GetValue() any = w.Value

func main() {
    val w = Wrap[string](Value = "hello")
    val res = w match {
        case w1: Wrap[_] => s"Matched Wrap[_]: ${w1.GetValue()}"
        case _ => "Other"
    }
    Println(res)
}
```

## Generic Type Pattern Matching Example

```gala
package main


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
        case w: Wrap[int] => s"Matched Wrap[int]: ${w.Value}"
        case w: Wrap[string] => "Matched Wrap[string]: " + w.Value
        case _ => "Other"
    }
    Println(res1)

    // 2. Matching wildcard generic type
    val w2 = Wrap[string](Value = "hello")
    val res2 = w2 match {
        case w: Wrap[_] => s"Matched Wrap[_]: ${w.Value}"
        case _          => "Other"
    }
    Println(res2)

    // 3. Using the generic extractor
    val w3 = Wrapper("GALA")
    val res3 = w3 match {
        case Wrapper(s: string) => "Extracted string: " + s
        case Wrapper(i: int) => s"Extracted int: $i"
        case _ => "Other"
    }
    Println(res3)
}
```

## Interface Example

```gala
package main


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
    
    Println("Area 1:", s1.Area())
    Println("Area 2:", s2.Area())
}
```

## Apply Method Example

```gala
package main


type Adder struct { Delta int }
func (a Adder) Apply(x int) int = x + a.Delta

type Multiply struct {}
func (m Multiply) Apply(x int, y int) int = x * y

func main() {
    val add5 = Adder(5)
    val res1 = add5(10) // 15
    
    val res2 = Multiply(3, 4) // 12
    
    Println(res1, res2)
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

import "martianoff/gala/examples/mathlib"

func main() {
    val sum = mathlib.Add(10, 20)
    Println("Sum is", sum)
}
```

You can also use dot imports to bring symbols into the current namespace:

```gala
import . "martianoff/gala/examples/mathlib"

func main() {
    val sum = Add(10, 20)
    Println("Sum is", sum)
}
```

## Advanced Type Inference Example

GALA uses the Hindley-Milner algorithm to infer types in complex scenarios, such as generic function calls. This enables features like automatic unwrapping of `Immutable` values even when they are returned from generic functions.

```gala
package main


func identity[T any](x T) T = x
func getImm[T any](x T) Immutable[T] = NewImmutable(x)

func main() {
    // HM infers Immutable[int], which GALA then automatically unwraps to int
    var x = identity(getImm(42)) 
    Println(s"x: $x")
}
```

## Boolean Exhaustive Match

Boolean pattern matching is exhaustive when both `true` and `false` cases are covered:

```gala
val desc = flag match {
    case true  => "enabled"
    case false => "disabled"
    // No case _ needed — true/false is exhaustive
}
```

## Void Closures and Lambda Parameter Inference

Functions can accept void closures (no return value), and lambda parameter types can be inferred from context. Prefer omitting parameter types in lambdas — the compiler infers them from the method signature:

```gala
val opt = Some(42)

// ForEach takes func(T) — void, no return needed
opt.ForEach((x) => {
    Println(x)
})

// Parameter type inferred from Option[int].Filter's signature
val positive = opt.Filter((x) => x > 0)

// Method type param and lambda param both inferred
val doubled = opt.Map((x) => x * 2)

// FoldLeft accumulator type inferred from zero value
val list = ArrayOf(1, 2, 3)
val sum = list.FoldLeft(0, (acc, x) => acc + x)

// Non-generic wrapper methods also infer lambda param types
val s = S("hello")
val hasVowel = s.Exists((r) => r == 'a' || r == 'e' || r == 'i' || r == 'o' || r == 'u')
```

## Collect - Filter and Transform in One Pass

`Collect` combines `Filter` and `Map` into a single operation using partial functions:

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val nums = ArrayOf(1, 2, 3, 4, 5, 6)

    // Collect: filter and transform in one pass
    val evenDoubled = nums.Collect({ case n if n % 2 == 0 => n * 2 })
    Println(evenDoubled)  // Array(4, 8, 12)

    // With sealed type extractors
    val options = ArrayOf(Some(1), None[int](), Some(2), None[int](), Some(3))
    val values = options.Collect({ case Some(v) => v * 10 })
    Println(values)  // Array(10, 20, 30)
}
```

## Option.OrElse - Fallback Options

```gala
package main

func main() {
    val primary = None[string]()
    val fallback = Some("default")

    val result = primary.OrElse(fallback)   // Some("default")
    Println(result.Get())                    // "default"

    val present = Some("actual")
    val result2 = present.OrElse(fallback)  // Some("actual")
    Println(result2.Get())                   // "actual"
}
```

## Try with Function References

When wrapping a zero-argument function, pass the function reference directly instead of wrapping in a lambda:

```gala
package main

import "os"

func main() {
    // Function reference (preferred for zero-arg functions)
    val dir = Try(os.TempDir)

    // Lambda form (use when args needed)
    val result = Try(() => os.MkdirAll("/tmp/test", 0755))

    dir.OnSuccess((d) => Println(s"Temp dir: $d"))
}
```

## MkString - Joining Collection Elements

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val nums = ArrayOf(1, 2, 3, 4, 5)
    Println(nums.MkString(", "))     // "1, 2, 3, 4, 5"
    Println(nums.MkString(" | "))    // "1 | 2 | 3 | 4 | 5"

    val words = ListOf("hello", "world")
    Println(words.MkString(" "))     // "hello world"

    val empty = EmptyArray[int]()
    Println(empty.MkString(", "))    // ""
}
```

## Sorted API - Sorting Collections

All collection types support `Sorted()`, `SortWith()`, and `SortBy()` for flexible sorting:

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    // Array.Sorted - natural ordering
    val arr = ArrayOf(3, 1, 4, 1, 5, 9)
    Println(arr.Sorted())                    // Array(1, 1, 3, 4, 5, 9)

    // Array.SortWith - custom comparator (descending)
    Println(arr.SortWith((a, b) => a > b))   // Array(9, 5, 4, 3, 1, 1)

    // Array.SortBy - sort by key function
    val arr2 = ArrayOf(1, 9, 3, 7, 5)
    Println(arr2.SortBy((x) => x))           // Array(1, 3, 5, 7, 9)

    // List.Sorted
    val list = ListOf("banana", "apple", "cherry")
    Println(list.Sorted())                   // List(apple, banana, cherry)

    // TreeSet.Sorted (already sorted)
    val tree = TreeSetOf(30, 10, 20)
    Println(tree.Sorted())                   // Array(10, 20, 30)

    // HashSet.Sorted
    val set = HashSetOf(5, 3, 1)
    Println(set.Sorted())                    // Array(1, 3, 5)

    // Empty collection
    Println(EmptyArray[int]().Sorted())      // Array()
}
```

## Type Aliases

Type aliases create alternative names for existing types, useful for re-exporting or shortening names:

```gala
package main


type MyString string

func main() {
    val s MyString = "hello"
    Println(s)
}
```

Output:
```
hello
```

## JSON Serialization and Pattern Matching

```gala
package main

import . "martianoff/gala/std"

type Person struct {
    var Name string
    var Age  int
}

func main() {
    val person = Person{Name: "Alice", Age: 30}
    val jsonStr = JsonStringify(person).Get()
    Println(jsonStr)

    // Pattern matching with Json extractor
    val result = jsonStr match {
        case Json[Person](p) => s"Matched: ${p.Name}, age ${p.Age}"
        case _ => "no match"
    }
    Println(result)
}
```

Output:
```
{"Name":"Alice","Age":30}
Matched: Alice, age 30
```

## Regex Pattern Matching

```gala
package main

import (
    . "martianoff/gala/std"
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/regex"
)

func main() {
    val dateRegex = regex.MustCompile("(\\d{4})-(\\d{2})-(\\d{2})")
    val result = "2024-01-15" match {
        case dateRegex(Array(year, month, day)) => s"Year: $year, Month: $month, Day: $day"
        case _ => "not a date"
    }
    Println(result)
}
```

Output:
```
Year: 2024, Month: 01, Day: 15
```

## IO Effect

```gala
package main

import (
    "errors"
    "martianoff/gala/io"
)

func main() {
    val pure = io.Of(42)
    Println(s"Pure: ${pure.Run().Get()}")

    val failing = io.Fail[int](errors.New("oops"))
    val recovered = io.Recover(failing, (err) => -1)
    Println(s"Recovered: ${recovered.Run().Get()}")

    val effect = io.Effect(() => { Println("Side effect!") })
    effect.Run()
}
```

Output:
```
Pure: 42
Recovered: -1
Side effect!
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
- `either_map_flatmap.gala`: Demonstrates `Either.Map` and `Either.FlatMap` chaining.
- `unary_minus.gala`: Demonstrates unary operators (`-`, `!`).
- `sealed_wildcard.gala`: Demonstrates wildcard `case _ =>` catch-all in sealed type matching.
- `sorted.gala`: Demonstrates `Sorted()`, `SortWith()`, and `SortBy()` on collections.
