# Why GALA?

Go is a remarkable language. It compiles fast, deploys as a single binary, and its concurrency model is genuinely elegant. But if you have spent time in languages like Scala, Kotlin, or Rust, you have felt the friction: the verbose error handling, the absence of sum types, the way a simple "transform this list" operation turns into a five-line for loop with an accumulator variable. You know these patterns are solved problems. You just wish Go solved them too.

GALA is an attempt to bridge that gap. It is a language that transpiles to Go, giving you functional programming constructs -- sealed types, pattern matching, immutability by default, monadic error handling -- while keeping everything that makes Go great: native binaries, goroutines, the entire Go ecosystem, and a shallow learning curve.

This is not about replacing Go. It is about writing Go-targeted code with fewer sharp edges.

---

## The Value Proposition

GALA sits at a specific intersection: you want the expressiveness of a language like Scala, but you need the operational characteristics of Go -- fast startup, small binaries, simple deployment, no JVM. You want to write `list.Map((x) => x * 2)` instead of manually iterating with index variables. You want the compiler to tell you when you forgot to handle a case in your sum type. You want `val` to mean immutable, not "I promise I won't reassign this."

GALA delivers these things with a concise, readable syntax that is fully Go-compatible. You write expressive GALA code, and it compiles to native Go binaries with full access to Go's tooling and ecosystem.

---

## Feature 1: Sealed Types Replace Stringly-Typed Dispatch

### The problem

Go has no algebraic data types. When you need a value that can be one of several shapes, you reach for interfaces with type switches, or structs with discriminator fields and iota constants. Both approaches have the same flaw: the compiler cannot tell you when you forgot to handle a variant.

### GALA

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

func describe(s Shape) string = s match {
    case Circle(r)      => fmt.Sprintf("circle with radius %.2f", r)
    case Rectangle(w, h) => fmt.Sprintf("rectangle %0.fx%.0f", w, h)
    case Point()        => "a point"
}
```

If you add a new variant -- say `case Triangle(Base float64, Height float64)` -- every match expression that does not handle `Triangle` becomes a compile error. No silent fallthrough, no forgotten cases.

### Equivalent Go

```go
type Shape struct {
    _variant  uint8
    Radius    float64
    Width     float64
    Height    float64
}

const (
    Shape_Circle    uint8 = 0
    Shape_Rectangle uint8 = 1
    Shape_Point     uint8 = 2
)

func describe(s Shape) string {
    switch s._variant {
    case Shape_Circle:
        return fmt.Sprintf("circle with radius %.2f", s.Radius)
    case Shape_Rectangle:
        return fmt.Sprintf("rectangle %0.fx%.0f", s.Width, s.Height)
    case Shape_Point:
        return "a point"
    default:
        panic("unhandled variant")
    }
}
```

The Go version is more than twice the length, carries fields that are meaningless for most variants, and relies on a runtime panic for exhaustiveness that the compiler cannot verify.

### Why this matters

Sealed types encode domain constraints directly in the type system. They are the right tool for representing states (Loading/Loaded/Error), results (Ok/Err), protocol messages, AST nodes -- anywhere you have a closed set of possibilities. GALA gives you these with zero ceremony and full compiler-enforced exhaustiveness.

---

## Feature 2: Pattern Matching That Actually Extracts Data

### The problem

Go's `switch` statement matches on values or types, but it does not destructure. If you want to pull fields out of a struct inside a conditional, you write the match and the extraction as separate steps. When conditions are nested -- "if it is a Some containing an even number" -- the code becomes a staircase of `if` blocks.

### GALA

```gala
type Even struct {}
func (e Even) Unapply(i int) Option[int] = if (i % 2 == 0) Some(i) else None[int]()

val opt = Some(42)
val result = opt match {
    case Some(Even(n)) => fmt.Sprintf("%d is even", n)
    case Some(n)       => fmt.Sprintf("%d is odd", n)
    case None()        => "nothing"
}
```

Extractors compose. Guards add conditions. Sequence patterns destructure lists. The match expression is itself an expression -- it returns a value, no intermediate variable needed.

```gala
val list = ListOf(1, 2, 3, 4, 5)
val msg = list match {
    case List(head, tail...) => fmt.Sprintf("head=%d, rest has %d items", head, tail.Size())
    case List()              => "empty"
}
```

### Equivalent Go

```go
var result string
if opt.IsSome() {
    n := opt.Value
    if n%2 == 0 {
        result = fmt.Sprintf("%d is even", n)
    } else {
        result = fmt.Sprintf("%d is odd", n)
    }
} else {
    result = "nothing"
}
```

### Why this matters

Pattern matching collapses condition checking and data extraction into one operation. It eliminates an entire category of bugs -- the ones where you check for a condition in one line and accidentally use the wrong variable three lines later. With guards (`case n if n > 0 =>`), you can express complex conditions without nesting. With custom extractors, you can teach the pattern matcher new domain-specific rules.

---

## Feature 3: Immutability by Default

### The problem

Go variables are mutable by default. There is no language-level way to declare a struct field as read-only after construction. `const` only works for compile-time constants, not runtime values. This means every function that receives a struct could, in theory, modify it. Defensive copying is manual and easy to forget.

### GALA

```gala
struct Config(Host string, Port int, var RetryCount int)

val cfg = Config("localhost", 8080, 3)
// cfg.Host = "other"       // compile error: Host is immutable
// cfg.Port = 9090          // compile error: Port is immutable
cfg.RetryCount = 5          // OK: RetryCount is explicitly var

val updated = cfg.Copy(Port = 9090)  // new Config, original unchanged
```

Variables declared with `val` or `:=` are immutable. Struct fields are immutable unless marked `var`. The `Copy` method -- auto-generated for every struct -- produces a new instance with selected fields overridden.

### Equivalent Go

```go
type Config struct {
    Host       string
    Port       int
    RetryCount int
}

// No compile-time enforcement of field immutability.
// Anyone with a reference can modify any field.
// Copy requires manually listing every field:
updated := Config{Host: cfg.Host, Port: 9090, RetryCount: cfg.RetryCount}
```

### Why this matters

Immutability by default shifts the burden. Instead of hoping no one mutates your data, you opt in to mutation where you need it. This makes concurrent code safer, reduces the surface area for bugs, and makes it trivially easy to reason about what a function can and cannot do. The auto-generated `Copy` method removes the last practical objection: "but immutability means I have to write boilerplate to create modified copies."

---

## Feature 4: Monadic Error Handling

### The problem

Go's `if err != nil` pattern is explicit and clear. It is also repetitive. When you have a chain of operations that can each fail, the error-checking boilerplate dominates the business logic. The actual data transformation -- the thing you care about -- gets buried.

### GALA

```gala
// Option: safe absence handling
val name = user.FindName()
    .Map((n) => strings.ToUpper(n))
    .GetOrElse("ANONYMOUS")

// Try: railway-oriented error handling
val result = Try[int](() => riskyComputation())
    .Map((v) => v * 2)
    .FlatMap((v) => anotherRiskyOp(v))
    .Recover((e) => 0)

// Either: typed errors
val output = parseInput(raw) match {
    case Right(data) => process(data)
    case Left(err)   => handleError(err)
}
```

`Option[T]`, `Try[T]`, and `Either[A, B]` are all sealed types with `Map`, `FlatMap`, `Filter`, and other combinators. They compose: a `Try` converts to an `Option` or `Either`. A failed `FlatMap` short-circuits the chain. Pattern matching on the result is exhaustive.

### Equivalent Go

```go
name := "ANONYMOUS"
n, err := user.FindName()
if err == nil {
    name = strings.ToUpper(n)
}

result, err := riskyComputation()
if err != nil {
    result = 0
} else {
    result = result * 2
    result, err = anotherRiskyOp(result)
    if err != nil {
        result = 0
    }
}
```

### Why this matters

Monadic error handling is not about avoiding `if err != nil`. It is about making the happy path readable. When you chain `.Map` and `.FlatMap`, the business logic reads top to bottom as a pipeline. Error handling is not interleaved -- it is declared once at the end with `.Recover` or `.GetOrElse`. The compiler ensures you handle both cases when you pattern match.

---

## Feature 5: Functional Collections

### The problem

Go slices are bare arrays. To filter a slice, you write a for loop. To transform each element, another for loop. To accumulate a result, another for loop with a mutable accumulator. These operations are so common that the boilerplate becomes invisible -- but it is still boilerplate, and each loop is a place where an off-by-one error or a forgotten append can hide.

### GALA

```gala
import . "martianoff/gala/collection_immutable"

val people = ArrayOf(
    Person("Alice", 30),
    Person("Bob", 17),
    Person("Charlie", 65)
)

val adultNames = people
    .Filter((p) => p.Age >= 18)
    .Map((p) => p.Name)
    .MkString(", ")
// "Alice, Charlie"

val totalAge = people.FoldLeft(0, (acc, p) => acc + p.Age)
// 112

val sorted = people.SortBy((p) => p.Age)
```

GALA provides `Array`, `List`, `HashMap`, `HashSet`, and `TreeSet` -- all immutable, all with `Map`, `Filter`, `FoldLeft`, `ForEach`, `Find`, `Exists`, `Collect`, `Sorted`, `SortBy`, `SortWith`, `ZipWithIndex`, `MkString`, and more. Mutable variants exist in `collection_mutable` for performance-sensitive paths.

### Equivalent Go

```go
var adultNames []string
for _, p := range people {
    if p.Age >= 18 {
        adultNames = append(adultNames, p.Name)
    }
}
result := strings.Join(adultNames, ", ")

totalAge := 0
for _, p := range people {
    totalAge += p.Age
}

sort.Slice(people, func(i, j int) bool {
    return people[i].Age < people[j].Age
})
```

### Why this matters

Functional collection operations are not syntactic sugar -- they are semantic compression. `Filter` says "keep elements matching this predicate" without exposing the loop mechanics. `FoldLeft` says "reduce this collection to a single value" without requiring a mutable accumulator. The code reads as a description of *what* you want, not *how* to iterate. And because the collections are immutable, you never accidentally mutate a shared reference.

---

## When to Use GALA

GALA is a good fit when:

- **You are building Go-targeted services** but want stronger type-level guarantees, especially around sum types and exhaustive matching.
- **Your domain has complex state machines** or protocol types that benefit from sealed types and pattern matching.
- **You value immutability** and want the compiler to enforce it, not a code review checklist.
- **You come from Scala, Kotlin, or Rust** and want similar expressiveness with Go's operational characteristics: fast startup, small binaries, simple deployment.
- **You are writing data transformation pipelines** where functional collection operations dramatically reduce boilerplate.

---

## When NOT to Use GALA

Be honest with yourself about these trade-offs:

- **Performance-critical inner loops.** GALA's immutable wrappers add a layer of indirection. For tight numerical computation, raw Go may be faster. Profile before assuming this matters -- for most application code, it does not.
- **Large existing Go codebases.** GALA interoperates with Go libraries, but rewriting an existing Go project in GALA is rarely justified. Use GALA for new modules where the expressiveness pays off.
- **Teams unfamiliar with functional programming.** If your team does not know what `FlatMap` does, GALA will slow them down before it speeds them up. The concepts are learnable, but they require investment.
- **Ecosystem maturity.** GALA's tooling (IDE support, debugger integration, error messages) is less mature than Go's. You will occasionally hit rough edges that a mainstream language has long since smoothed out.
- **You need the absolute latest Go features immediately.** Since GALA transpiles to Go, new Go features appear in GALA only after the transpiler is updated.

---

## Getting Started

Install a pre-built binary from [Releases](https://github.com/martianoff/gala/releases), or build from source:

```bash
git clone https://github.com/martianoff/gala.git && cd gala
bazel build //cmd/gala:gala
```

Write your first program:

```gala
package main

import "fmt"

struct Person(Name string, Age int)

func greet(p Person) string = p match {
    case Person(name, age) if age < 18 => "Hey, " + name + "!"
    case Person(name, _)               => "Hello, " + name
}

func main() {
    val alice = Person("Alice", 25)
    fmt.Println(greet(alice))
}
```

Run it:

```bash
gala run main.gala
```

For the full language reference, see [GALA.MD](GALA.MD). For more code examples, see [EXAMPLES.MD](EXAMPLES.MD).
