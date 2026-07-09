# GALA Examples

This page contains examples demonstrating various features of the GALA language.

## Complete Example

The following example demonstrates many of GALA's features, including structs, immutability, expression functions, and control flow.

```gala
package main


struct Point(X int, Y int)

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
    val z = None[int]().GetOrElse(42)

    val res = y match {
        case Some(v) => v
        case _       => 0
    }
    
    Println(res, z)
}
```

## Sealed Types (Algebraic Data Types)

A `sealed type` defines a closed set of variants (an ADT). Because the compiler
knows every case, a `match` over a sealed type is checked for exhaustiveness — no
`case _` default is needed once all variants are covered. Variants may carry
fields or be empty.

```gala
package main

import . "martianoff/gala/collection_immutable"

sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

// Exhaustive match — every variant is covered, so no default case is required.
func area(s Shape) float64 = s match {
    case Circle(r)       => 3.14159 * r * r
    case Rectangle(w, h) => w * h
    case Point()         => 0.0
}

func main() {
    val shapes = ArrayOf[Shape](Circle(2.0), Rectangle(3.0, 4.0), Point())
    shapes.ForEach((s) => Println(s, "=> area", area(s)))
}
```

Output:
```
Circle(2) => area 12.56636
Rectangle(3, 4) => area 12
Point() => area 0
```

## Either Monad

`Either[L, R]` holds one of two values — conventionally `Left` for failure and
`Right` for success. Monadic operations (`Map`, `FlatMap`) act on the `Right`
side and pass `Left` through untouched.

```gala
package main

func parseEven(n int) Either[string, int] =
    if (n % 2 == 0) Right[string, int](n) else Left[string, int](s"$n is odd")

func main() {
    val a = parseEven(10)
    val b = parseEven(7)

    // Map transforms the Right side; a Left passes through unchanged.
    val doubled = a.Map((v) => v * 2)
    Println(doubled)   // Right(20)

    val r = b match {
        case Right(v) => s"got $v"
        case Left(e)  => s"error: $e"
    }
    Println(r)         // error: 7 is odd
}
```

Output:
```
Right(20)
error: 7 is odd
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

import . "martianoff/gala/collection_immutable"

struct Person(Name string, Age int)

func main() {
    val people = ArrayOf(
        Person("Alice", 25),
        Person("Bob", 15),
        Person("Charlie", 70),
    )

    people.ForEach((p) => {
        val status = p match {
            case Person(name, age) if age < 18 => name + " is a minor"
            case Person(name, age) if age > 65 => name + " is a senior"
            case Person(name, _)               => name + " is an adult"
            case _                             => "Unknown"
        }
        Println(status)
    })
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
    val r = Rect(10.0, 5.0)
    val c = Circle(10.0)
    
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
package main

func main() {
    val flag = true
    val desc = flag match {
        case true  => "enabled"
        case false => "disabled"
        // No case _ needed — true/false is exhaustive
    }
    Println(desc)
}
```

## For Loops

GALA supports Go-style `for` in its familiar forms: a three-clause counting loop
and a condition-only (while-style) loop. `break` and `continue` work as usual.
For iterating collections, prefer the functional methods (`ForEach`, `Map`, …)
shown elsewhere on this page.

```gala
package main

func main() {
    // Three-clause counting loop
    for i := 0; i < 3; i++ {
        Println(s"i = $i")
    }

    // Condition-only (while-style) loop
    var n = 3
    for n > 0 {
        Println(s"countdown $n")
        n = n - 1
    }
}
```

Output:
```
i = 0
i = 1
i = 2
countdown 3
countdown 2
countdown 1
```

## String Interpolation

GALA has two interpolated string forms. `s"..."` inserts values with `$var` or
`${expr}`; `f"..."` adds explicit printf-style format specs. Neither needs an
`fmt` import.

```gala
package main

func main() {
    val name = "Alice"
    val age = 30
    val pi = 3.14159

    // s-string: value interpolation with $var and ${expr}
    Println(s"$name is $age years old")
    Println(s"Next year: ${age + 1}")
    Println(s"Price: $$99")          // literal dollar sign

    // f-string: explicit printf-style format specs
    Println(f"Padded: $age%05d")
    Println(f"Fixed point: $pi%.2f")
}
```

Output:
```
Alice is 30 years old
Next year: 31
Price: $99
Padded: 00030
Fixed point: 3.14
```

## Variadic Functions and the Spread Operator

A trailing `...T` parameter accepts any number of arguments. At a call site, an
existing collection can be spread into such a parameter with the postfix `...`
operator.

```gala
package main

import . "martianoff/gala/collection_immutable"

func sum(nums ...int) int = ArrayOf(nums...).FoldLeft(0, (acc, n) => acc + n)

func main() {
    Println(sum(1, 2, 3))              // 6
    Println(sum())                     // 0

    // Spread a collection into the variadic parameter.
    val more = ArrayOf(4, 5, 6)
    Println(sum(more.ToGoSlice()...))  // 15
}
```

Output:
```
6
0
15
```

## Void Closures and Lambda Parameter Inference

Functions can accept void closures (no return value), and lambda parameter types can be inferred from context. Prefer omitting parameter types in lambdas — the compiler infers them from the method signature:

```gala
package main

import (
    . "martianoff/gala/collection_immutable"
    . "martianoff/gala/strings"
)

func main() {
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

    Println(positive, doubled, sum, hasVowel)
}
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

    // Lambda form (use when args needed) — a bare Try(...) statement discards
    // the result; bind it to a val only when you go on to use it.
    Try(os.MkdirAll("/tmp/test", 0755))

    dir.OnSuccess((d) => Println(s"Temp dir: $d"))
}
```

## By-Name Arguments (Thunk Sugar)

When a parameter's expected type is a zero-arg function (`func() T`), you can pass
a bare expression — it is lifted into `() => expr` and evaluated lazily on each
call. This is what lets the async `Future` constructor read like a direct call:

```gala
package main

import . "martianoff/gala/concurrent"

func double(n int) int = n * 2

// A plain function whose parameter is a zero-arg function.
func runTwice(body func() int) int = body() + body()

func main() {
    // Bare expression lifted to a thunk: runTwice(double(21)).
    Println(runTwice(double(21))) // 84 (double(21) evaluated on each body() call)

    // Headline case: Future(expr) == Future(() => expr); T inferred from the body.
    val f = Future(double(50))
    Println(f.Get()) // 100

    // The explicit lambda form remains valid and equivalent.
    val g = Future(() => double(50))
    Println(g.Get()) // 100
}
```

Output:
```
84
100
100
```

An argument that is already a function value is passed through untouched (never
double-wrapped), and multi-argument function parameters still require an explicit
lambda.

## Future - Asynchronous Computations

`Future[T]` runs a computation asynchronously. Thanks to by-name arguments,
`Future(expr)` schedules `expr` on a background executor. Results compose with
`Map`/`FlatMap` without blocking; `Get()` blocks until the value is available.
`Succeeded`/`Failed` are extractor patterns, so a `case _` default is required.

```gala
package main

import . "martianoff/gala/concurrent"

func main() {
    // Future(expr) runs the body asynchronously.
    val f = Future(21 * 2)

    // Transform the eventual result without blocking.
    val g = f.Map((x) => x + 1)

    Println(g.Get())   // 43 — Get() blocks for the result

    val result = g match {
        case Succeeded(v) => s"ok: $v"
        case Failed(e)    => s"err: ${e.Error()}"
        case _            => "pending"
    }
    Println(result)    // ok: 43
}
```

Output:
```
43
ok: 43
```

## Try with Go Multi-Return Functions

`Try` handles Go functions returning `(T, error)` and multi-return `(A, B, error)` automatically:

```gala
package main

import "net"
import "strconv"

func main() {
    // (int, error) -> Try[int]
    val intResult = Try(strconv.Atoi("42")) match {
        case Success(n) => s"Parsed: $n"
        case Failure(err) => s"Error: ${err.Error()}"
    }
    Println(intResult) // Parsed: 42

    // (string, string, error) -> Try[Tuple[string, string]]
    val hostPort = Try(net.SplitHostPort("localhost:8080")) match {
        case Success(hp) => s"host=${hp.V1} port=${hp.V2}"
        case Failure(err) => s"Error: ${err.Error()}"
    }
    Println(hostPort) // host=localhost port=8080

    // Error case is caught as Failure
    val bad = Try(net.SplitHostPort("invalid")) match {
        case Success(hp) => s"host=${hp.V1} port=${hp.V2}"
        case Failure(_) => "split error caught"
    }
    Println(bad) // split error caught
}
```

Output:
```
Parsed: 42
host=localhost port=8080
split error caught
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

## HashMap - Immutable Key/Value Maps

`HashMap[K, V]` is an immutable map: `Put`/`Remove` return a new map rather than
mutating in place. Lookups return an `Option`, and the usual functional
operations (`MapValues`, `Filter`, `FoldLeftKV`, …) are available.

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val scores = HashMapOf(("Alice", 85), ("Bob", 92))
    val updated = scores.Put("Carol", 78)   // returns a new map

    Println(updated.GetOrElse("Bob", 0))     // 92
    Println(updated.Get("Dave"))             // None() — lookups return Option

    // Transform every value; the key set is unchanged.
    val grades = updated.MapValues((v) => v match {
        case n if n >= 90 => "A"
        case _            => "B"
    })
    Println(grades.GetOrElse("Bob", "?"))    // A
}
```

Output:
```
92
None()
A
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

import (
    . "martianoff/gala/std"
    . "martianoff/gala/json"
    . "martianoff/gala/collection_immutable"
)

struct Tag(Key string, Color string)
struct User(Name string, Tags Array[Tag])

func main() {
    val codec = Codec[User](SnakeCase())

    val tags = EmptyArray[Tag]().Append(Tag("urgent", "red"))
    val user = User("alice", tags)

    val jsonStr = codec.Encode(user).Get()
    Println(jsonStr)

    // Pattern matching extractor: case fails (no panic) if decode fails.
    val msg = jsonStr match {
        case codec(u) => s"Matched: ${u.Name}, ${u.Tags.Get(0).Key}"
        case _        => "no match"
    }
    Println(msg)
}
```

Output:
```
{"name":"alice","tags":[{"key":"urgent","color":"red"}]}
Matched: alice, urgent
```

## YAML Serialization

```gala
package main

import (
    . "martianoff/gala/std"
    . "martianoff/gala/yaml"
    . "martianoff/gala/collection_immutable"
)

struct Tag(Key string, Color string)
struct User(Name string, Tags Array[Tag])

func main() {
    val codec = Codec[User](SnakeCase())

    val tags = EmptyArray[Tag]().Append(Tag("urgent", "red"))
    val user = User("alice", tags)

    val yamlStr = codec.Encode(user).Get()
    Println(yamlStr)

    val decoded = codec.Decode(yamlStr).Get()
    Println(s"first tag: ${decoded.Tags.Get(0).Key}/${decoded.Tags.Get(0).Color}")
}
```

Output:
```
name: alice
tags:
  - key: urgent
    color: red
first tag: urgent/red
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

## Multiple Return Values (Using Tuples)

GALA uses Tuple types instead of Go-style multi-value returns. Tuple destructuring provides clean syntax at the call site.

```gala
package main

import . "martianoff/gala/std"

// Return two values using Tuple
func minMax(a int, b int) Tuple[int, int] {
    if (a < b) {
        return (a, b)
    }
    return (b, a)
}

// Return three values using Tuple3
func divmod(a int, b int) Tuple3[int, int, string] {
    if (b == 0) {
        return (0, 0, "division by zero")
    }
    return (a / b, a % b, "ok")
}

func main() {
    // Tuple destructuring (note parens around variable names)
    val (lo, hi) = minMax(7, 3)
    Println(s"min=$lo max=$hi")

    val (q, r, status) = divmod(17, 5)
    Println(s"$q remainder $r ($status)")
}
```

Output:
```
min=3 max=7
3 remainder 2 (ok)
```

## Monadic Binding with `bind` / `also`

`bind`/`also` flatten multi-step monadic code. See [`bind` / `also` notation](BIND_NOTATION.MD) for the full spec.

### The "graph" case — a value reused several steps later (`Try`)

A `FlatMap` chain has to *nest* whenever a later step needs a value from an earlier one, because each intermediate is trapped in a closure. The final `Receipt` needs the original `o` (bound first) **and** the `payment` (bound last), so `o` must survive to the bottom — and the accumulator type `[Receipt]` is repeated at every link because inference can't flow it through the lambdas:

**Before — the nested `FlatMap` pyramid** (`o` survives three levels of nesting, `[Receipt]` repeated at each link, ending in a `))))` pile):

```gala
func processOrder(id int) Try[Receipt] =
    fetchOrder(id).FlatMap[Receipt]((o) =>
    validateOrder(o).FlatMap[Receipt]((valid) =>
    chargePayment(valid).FlatMap[Receipt]((payment) =>
    Success(Receipt(o.Id, payment)))))
```

**After — a flat `bind` block** (every value in scope, top-to-bottom, no repeated type args, no paren pile):

```gala
func processOrder(id int) Try[Receipt] {
    bind o = fetchOrder(id)
    bind valid = validateOrder(o)
    bind payment = chargePayment(valid)
    Success(Receipt(o.Id, payment))   // `o` still in scope; first Failure short-circuits
}
```

Both forms are equivalent — the `bind` block *lowers* to exactly that pyramid (see [`examples/bind_notation_desugared.gala`](../examples/bind_notation_desugared.gala)) — but the `bind` form stays flat as the graph deepens, drops the repeated `[Receipt]`, and reads top-to-bottom. Full program: [`examples/bind_notation.gala`](../examples/bind_notation.gala).

### Error accumulation — report ALL invalid fields (`Validated`)

Validating several independent fields with a fail-fast monad only ever surfaces the **first** error: `vName(name).FlatMap((n) => vEmail(email).FlatMap((e) => vAge(age).Map((a) => Person(n, e, a))))` stops at the first failure. `Validated` (in the `validation` package) accumulates instead, and `also` opts into that accumulation:

```gala
import . "martianoff/gala/validation"

func makePerson(name string, email string, age int) Validated[string, Person] {
    bind n = vName(name)
    also e = vEmail(email)
    also a = vAge(age)
    Valid(Person(n, e, a))
}

func main() {
    val ok = makePerson("Ada", "ada@example.com", 36)
    Println(s"ok valid? ${ok.IsValid()}")              // all clauses valid

    val bad = makePerson("", "", -1)
    Println(s"bad errors: ${bad.GetErrors().Size()}")  // ALL three accumulated
}
```

Output:
```
ok valid? true
bad errors: 3
```

Swap the three `also`s for `bind`s (or use `Try`) and `bad errors` would be `1`. Full program: [`examples/bind_notation_validated.gala`](../examples/bind_notation_validated.gala).

### Concurrency — run independent clauses in parallel (`Future`)

Over `Future`, an `also` group runs its clauses **concurrently** instead of threading each through the next:

```gala
import . "martianoff/gala/concurrent"

func total() Future[int] {
    bind a = compute(2)
    also b = compute(3)   // b and c do not depend on a — run all three at once
    also c = compute(4)
    Future[int](a + b + c)
}
```

Full program: [`examples/bind_notation_future.gala`](../examples/bind_notation_future.gala).

## Scoped Resource Cleanup with `use`

GALA has no `defer` (a bare `defer`/`go` statement is a hard error, **GALA-E0036**). Cleanup is a `use` binding: `use x = acquire` binds `x` for the rest of the block and guarantees `x.Close()` runs when the function returns, on every path. Multiple `use` bindings release **LIFO** (last acquired closes first):

```gala
type Handle struct {
    name string
}

func (h Handle) Close() error {
    Println(s"close ${h.name}")
    return nil
}

func open(name string) Handle {
    Println(s"open ${name}")
    return Handle(name = name)
}

func work() {
    use a = open("a")   // closes last
    use b = open("b")   // closes first (LIFO)
    Println(s"body sees ${a.name} and ${b.name}")
}
// Output: open a / open b / body sees a and b / close b / close a
```

For resources without `Close()` (a mutex, a temp dir), reach for the `resource` combinators — `Bracket(res, release, body)` or `WithLock(mu, () => …)`. A goroutine is `go_interop.Spawn(() => f())`, not a bare `go`.

Full programs: [`examples/use_binding.gala`](../examples/use_binding.gala), [`examples/resource_combinators.gala`](../examples/resource_combinators.gala).

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
- `bind_notation.gala`: Sequential `bind` over `Try` (the "graph" case).
- `bind_notation_also.gala`: `also` over `Option` (fail-fast fallback).
- `bind_notation_validated.gala`: `also` over `Validated`, accumulating all errors.
- `bind_notation_future.gala`: `also` over `Future`, running clauses concurrently.
- `bind_notation_user.gala`: `bind` over a user-defined monad (`Step`).
- `unary_minus.gala`: Demonstrates unary operators (`-`, `!`).
- `sealed_wildcard.gala`: Demonstrates wildcard `case _ =>` catch-all in sealed type matching.
- `sorted.gala`: Demonstrates `Sorted()`, `SortWith()`, and `SortBy()` on collections.
