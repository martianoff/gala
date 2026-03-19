---
layout: default
title: "GALA vs Go — Sum Types, Pattern Matching, and Option Types Compared"
description: "Side-by-side comparison of GALA and Go. See how sum types replace type switches, pattern matching replaces switch, Option replaces nil checks, and functional collections replace manual loops — same Go binary."
keywords: "gala vs go, golang sum types comparison, go pattern matching vs switch, golang option vs nil, go error handling comparison, transpile to go, golang functional vs imperative, golang missing features"
permalink: /vs-go/
---

<div class="breadcrumb">
  <a href="{{ '/' | relative_url }}">Home</a> &raquo; GALA vs Go
</div>

# GALA vs Go -- Side-by-Side Comparison

GALA is not replacing Go -- it is adding expressiveness on top of it. GALA transpiles to standard Go code, so you get the same runtime, the same libraries, the same single-binary deployments, and the same performance. The difference is what you write to get there.

This page shows real code comparisons between GALA and the equivalent idiomatic Go. Every GALA snippet on this page uses only syntax from the language specification.

---

## Pattern Matching vs Switch

GALA sealed types define closed hierarchies. The compiler enforces exhaustive matching -- if you forget a case, it will not compile. Go requires manual variant tracking with constants and field accessors.

<div class="comparison">
<div>
<p>GALA</p>

```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

val msg = shape match {
    case Circle(r)       => f"r=$r%.1f"
    case Rectangle(w, h) => f"$w%.0fx$h%.0f"
    case Point()         => "point"
}
```

</div>
<div>
<p>Go</p>

```go
var msg string
switch shape._variant {
case Shape_Circle:
    msg = fmt.Sprintf("r=%.1f",
        shape.Radius.Get())
case Shape_Rectangle:
    msg = fmt.Sprintf("%fx%f",
        shape.Width.Get(),
        shape.Height.Get())
case Shape_Point:
    msg = "point"
}
```

</div>
</div>

GALA destructures values directly into named bindings (`r`, `w`, `h`) and produces a compile-time error if you forget a case. Go's `switch` does not enforce exhaustiveness and requires explicit field access through `.Get()` calls.

---

## Option Handling vs nil Checks

GALA's `Option[T]` type makes the presence or absence of a value explicit. You chain operations with `Map`, `FlatMap`, and `GetOrElse` instead of checking for `nil`.

<div class="comparison">
<div>
<p>GALA</p>

```gala
val name = user.Name
    .Map((n) => strings.ToUpper(n))
    .GetOrElse("ANONYMOUS")
```

</div>
<div>
<p>Go</p>

```go
name := "ANONYMOUS"
if user.Name != nil {
    name = strings.ToUpper(*user.Name)
}
```

</div>
</div>

With `Option[T]`, you never forget a nil check. The type system enforces it. You can also pattern match on options directly:

```gala
val res = opt match {
    case Some(v) => s"got $v"
    case None()  => "nothing"
}
```

---

## Immutable Structs vs Manual Copying

GALA structs are immutable by default. Fields cannot be reassigned after construction. The compiler auto-generates a `Copy()` method for creating modified copies, and an `Equal()` method for structural comparison.

<div class="comparison">
<div>
<p>GALA</p>

```gala
struct Config(Host string, Port int)
val updated = config.Copy(Port = 8080)
```

</div>
<div>
<p>Go</p>

```go
type Config struct {
    Host string
    Port int
}
updated := Config{
    Host: config.Host,
    Port: 8080,
}
```

</div>
</div>

In Go, you must manually copy every field you want to keep. With GALA's `Copy()`, you only specify the fields that change. As your struct grows, the GALA version stays the same size while the Go version grows with every field.

---

## Error Handling: Try Chain vs if-err

GALA's `Try[T]` type wraps computations that can fail. Instead of checking `if err != nil` after every call, you chain operations with `Map`, `FlatMap`, and `Recover`.

<div class="comparison">
<div>
<p>GALA</p>

```gala
val result = divide(10, 2)
    .Map((x) => x * 2)
    .FlatMap((x) => divide(x, 3))
    .Recover((e) => 0)
```

</div>
<div>
<p>Go</p>

```go
result, err := divide(10, 2)
if err != nil {
    result = 0
} else {
    result = result * 2
    result, err = divide(result, 3)
    if err != nil {
        result = 0
    }
}
```

</div>
</div>

The GALA version reads as a linear pipeline: divide, double, divide again, recover on failure. The Go version nests deeper with each operation. Both compile to equivalent logic, but GALA expresses the intent more clearly.

You can also pattern match on `Try[T]`:

```gala
val msg = result match {
    case Success(v) => s"got $v"
    case Failure(e) => s"error: $e"
}
```

---

## Collection Pipelines vs Manual Loops

GALA provides immutable functional collections with `Map`, `Filter`, `FoldLeft`, `Collect`, and more. Go requires manual loops with `append`.

<div class="comparison">
<div>
<p>GALA</p>

```gala
val nums = ArrayOf(1, 2, 3, 4, 5)
val result = nums
    .Filter((x) => x % 2 == 0)
    .Map((x) => x * 2)
```

</div>
<div>
<p>Go</p>

```go
nums := []int{1, 2, 3, 4, 5}
var result []int
for _, x := range nums {
    if x%2 == 0 {
        result = append(result, x*2)
    }
}
```

</div>
</div>

GALA's `Collect` combines filter and transform in a single pass using partial functions:

```gala
val evenDoubled = nums.Collect({ case n if n % 2 == 0 => n * 2 })
```

Other collection operations:

```gala
val sum = nums.FoldLeft(0, (acc, x) => acc + x)
val sorted = nums.SortWith((a, b) => a > b)
val csv = nums.MkString(", ")
```

---

## String Interpolation vs fmt.Sprintf

GALA has built-in string interpolation with `s"..."` for auto-inferred format verbs and `f"..."` for explicit format specs. No imports needed.

<div class="comparison">
<div>
<p>GALA</p>

```gala
val name = "Alice"
val age = 30
Println(s"$name is $age years old")
Println(f"Pi = ${3.14159}%.2f")
```

</div>
<div>
<p>Go</p>

```go
name := "Alice"
age := 30
fmt.Printf("%s is %d years old\n", name, age)
fmt.Printf("Pi = %.2f\n", 3.14159)
```

</div>
</div>

GALA also provides `Println` and `Print` as built-in functions -- no `fmt` import required.

---

## When to Choose GALA

GALA is a strong fit when you want:

- **Exhaustive pattern matching** -- sealed types with compile-time completeness checks
- **Monadic error handling** -- `Option[T]`, `Either[A,B]`, `Try[T]` instead of `if err != nil`
- **Immutability by default** -- `val` bindings and auto-generated `Copy()` / `Equal()`
- **Functional collection pipelines** -- `Map`, `Filter`, `FoldLeft`, `Collect` on immutable data structures
- **Concise syntax** -- expression functions, lambda type inference, string interpolation
- **Full Go compatibility** -- every Go library works, no wrappers needed

## When to Stick with Go

Go is the better choice when you need:

- **Maximum ecosystem familiarity** -- your team already knows Go and prefers its explicit style
- **Direct low-level control** -- manual memory layout, unsafe operations, CGo interop
- **Minimal toolchain** -- no transpilation step, just `go build`
- **Established hiring pipeline** -- Go developers are easier to recruit than GALA developers

Both are valid choices. GALA and Go produce the same binaries and use the same runtime -- the difference is in the source language you write.

## Interoperability

GALA uses Go libraries directly. There are no wrappers, no bindings, and no FFI layer. If a Go package exists, you can import it and call it from GALA:

```gala
import "strings"
import "os"

val upper = strings.ToUpper("hello")
val dir = Try(os.TempDir)
```

Go types, interfaces, and functions are all available. GALA adds its own type system on top -- sealed types, `Option[T]`, immutable structs -- but the underlying Go interop is seamless.

---

## Dive Deeper

- [Getting Started]({{ '/getting-started/' | relative_url }}) -- Install GALA and write your first program
- [Sealed Types]({{ '/features/sealed-types/' | relative_url }}) -- Algebraic data types and closed hierarchies
- [Pattern Matching]({{ '/features/pattern-matching/' | relative_url }}) -- Exhaustive matching, destructuring, and guards
- [Error Handling]({{ '/features/error-handling/' | relative_url }}) -- `Option[T]`, `Either[A,B]`, `Try[T]` monads
- [Collections]({{ '/features/collections/' | relative_url }}) -- Immutable and mutable functional collections
- [Playground]({{ '/playground/' | relative_url }}) -- Try GALA in your browser without installing anything
