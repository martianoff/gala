---
layout: default
title: "GALA vs Go — Sum Types, Pattern Matching, JSON Codec, and Default Parameters Compared"
description: "Side-by-side comparison of GALA and Go. See how sum types replace type switches, pattern matching replaces switch, Option replaces nil checks, zero-reflection JSON replaces struct tags, default parameters replace functional options — same Go binary."
keywords: "gala vs go, golang sum types comparison, go pattern matching vs switch, golang option vs nil, go error handling comparison, transpile to go, golang functional vs imperative, golang missing features, golang json without reflection, go json alternative, golang default parameters, golang named arguments, go functional options alternative"
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
<p><strong>GALA</strong></p>
<pre><code>sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

val msg = shape match {
    case Circle(r)       =&gt; f"r=$r%.1f"
    case Rectangle(w, h) =&gt; f"$w%.0fx$h%.0f"
    case Point()         =&gt; "point"
}</code></pre>
</div>
<div>
<p><strong>Go</strong></p>
<pre><code>var msg string
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
}</code></pre>
</div>
</div>

GALA destructures values directly into named bindings (`r`, `w`, `h`) and produces a compile-time error if you forget a case. Go's `switch` does not enforce exhaustiveness and requires explicit field access through `.Get()` calls.

---

## Option Handling vs nil Checks

GALA's `Option[T]` type makes the presence or absence of a value explicit. You chain operations with `Map`, `FlatMap`, and `GetOrElse` instead of checking for `nil`.

<div class="comparison">
<div>
<p><strong>GALA</strong></p>
<pre><code>val name = user.Name
    .Map((n) =&gt; strings.ToUpper(n))
    .GetOrElse("ANONYMOUS")</code></pre>
</div>
<div>
<p><strong>Go</strong></p>
<pre><code>name := "ANONYMOUS"
if user.Name != nil {
    name = strings.ToUpper(*user.Name)
}</code></pre>
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
<p><strong>GALA</strong></p>
<pre><code>struct Config(Host string, Port int)
val updated = config.Copy(Port = 8080)</code></pre>
</div>
<div>
<p><strong>Go</strong></p>
<pre><code>type Config struct {
    Host string
    Port int
}
updated := Config{
    Host: config.Host,
    Port: 8080,
}</code></pre>
</div>
</div>

In Go, you must manually copy every field you want to keep. With GALA's `Copy()`, you only specify the fields that change. As your struct grows, the GALA version stays the same size while the Go version grows with every field.

---

## Error Handling: Try Chain vs if-err

GALA's `Try[T]` type wraps computations that can fail. Instead of checking `if err != nil` after every call, you chain operations with `Map`, `FlatMap`, and `Recover`.

<div class="comparison">
<div>
<p><strong>GALA</strong></p>
<pre><code>val result = divide(10, 2)
    .Map((x) =&gt; x * 2)
    .FlatMap((x) =&gt; divide(x, 3))
    .Recover((e) =&gt; 0)</code></pre>
</div>
<div>
<p><strong>Go</strong></p>
<pre><code>result, err := divide(10, 2)
if err != nil {
    result = 0
} else {
    result = result * 2
    result, err = divide(result, 3)
    if err != nil {
        result = 0
    }
}</code></pre>
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
<p><strong>GALA</strong></p>
<pre><code>val nums = ArrayOf(1, 2, 3, 4, 5)
val result = nums
    .Filter((x) =&gt; x % 2 == 0)
    .Map((x) =&gt; x * 2)</code></pre>
</div>
<div>
<p><strong>Go</strong></p>
<pre><code>nums := []int{1, 2, 3, 4, 5}
var result []int
for _, x := range nums {
    if x%2 == 0 {
        result = append(result, x*2)
    }
}</code></pre>
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
<p><strong>GALA</strong></p>
<pre><code>val name = "Alice"
val age = 30
Println(s"$name is $age years old")
Println(f"Pi = ${3.14159}%.2f")</code></pre>
</div>
<div>
<p><strong>Go</strong></p>
<pre><code>name := "Alice"
age := 30
fmt.Printf("%s is %d years old\n", name, age)
fmt.Printf("Pi = %.2f\n", 3.14159)</code></pre>
</div>
</div>

GALA also provides `Println` and `Print` as built-in functions -- no `fmt` import required.

---

## JSON Serialization: Codec vs Struct Tags

GALA's `Codec[T]` uses the compiler-generated `StructMeta[T]` intrinsic for fully typed JSON serialization — no reflection, no struct tags, and pattern matching support out of the box.

<div class="comparison">
<div>
<p><strong>GALA</strong></p>
<pre><code>struct Person(
    FirstName string,
    LastName string,
    Age int,
)

val codec = Codec[Person](SnakeCase())
val jsonStr = codec.Encode(person).Get()
// {"first_name":"Alice",...}

val decoded = codec.Decode(jsonStr)
// Try[Person] — fully typed

// Builder: rename, omit
val custom = codec
    .Rename("FirstName", "given_name")
    .Omit("Age")</code></pre>
</div>
<div>
<p><strong>Go</strong></p>
<pre><code>type Person struct {
    FirstName string `json:"first_name"`
    LastName  string `json:"last_name"`
    Age       int    `json:"age"`
}

data, err := json.Marshal(person)
if err != nil {
    // handle error
}

var decoded Person
err = json.Unmarshal(data, &amp;decoded)
if err != nil {
    // handle error
}

// Rename or omit? New struct +
// custom MarshalJSON method</code></pre>
</div>
</div>

GALA's codec is resolved entirely at compile time — zero reflection overhead at runtime. The builder pattern lets you rename or omit fields without defining a new struct or writing custom marshal methods. You can even pattern match on JSON strings directly using `codec(p)` as an extractor.

---

## 7. Default Parameter Values

Go has no default parameters. The common workaround is the "functional options" pattern, which requires a struct, variadic functions, and option types. GALA adds defaults directly to the function signature.

<div class="comparison">
<div>
<p><strong>GALA</strong></p>
<pre><code>func connect(
    host string,
    port int = 8080,
    tls bool = true,
) Connection {
    // ...
}

connect("localhost")
connect("db", tls = false)</code></pre>
</div>
<div>
<p><strong>Go</strong></p>
<pre><code>type ConnectOption func(*connectOpts)
type connectOpts struct {
    port int; tls bool
}
func WithPort(p int) ConnectOption { ... }
func WithTLS(t bool) ConnectOption { ... }

func Connect(host string,
    opts ...ConnectOption,
) Connection { ... }

Connect("localhost")
Connect("db", WithTLS(false))</code></pre>
</div>
</div>

Named arguments let callers skip parameters with defaults. The compiler validates default types at compile time and gives clear errors for mismatches.

---

## When to Choose GALA

GALA is a strong fit when you want:

- **Exhaustive pattern matching** -- sealed types with compile-time completeness checks
- **Monadic error handling** -- `Option[T]`, `Either[A,B]`, `Try[T]` instead of `if err != nil`
- **Immutability by default** -- `val` bindings and auto-generated `Copy()` / `Equal()`
- **Functional collection pipelines** -- `Map`, `Filter`, `FoldLeft`, `Collect` on immutable data structures
- **Default parameter values** -- named arguments, call-site defaults, no "functional options" boilerplate
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
