---
layout: default
title: "Golang Option Type — Option, Either, and Try for Go Error Handling"
description: "Replace Go's if-err-nil with GALA's Option[T], Either[A,B], and Try[T]. Language-level monadic error handling with Map, FlatMap, Recover, and pattern matching — cleaner than fp-go or manual nil checks."
keywords: "golang option type, go option monad, go either type, golang error handling alternative, go try monad, golang nil alternative, go monadic error handling, golang result type, go error handling verbose, gala error handling"
permalink: /features/error-handling/
last_modified_at: 2026-07-10
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/">Features</a> / Error Handling</p>

# Golang Option Type — Option, Either, and Try for Go

Go's `if err != nil` pattern is explicit, but it is also verbose. A function that chains three fallible operations needs three separate error checks, each with its own early return. The happy path drowns in boilerplate.

Libraries like [IBM/fp-go](https://github.com/IBM/fp-go) bring Option and Either to Go, but they're constrained by Go's syntax — resulting in deeply nested generic calls and verbose type annotations. GALA solves this at the **language level**: `Option[T]`, `Either[A, B]`, and `Try[T]` are first-class [sealed types](/features/sealed-types/) with clean syntax, `Map`/`FlatMap` chaining, [pattern matching](/features/pattern-matching/), and full type inference. Errors propagate automatically through the chain; you only handle them when you are ready.

---

## Option[T]: Some and None

`Option[T]` represents a value that may or may not exist. It is a sealed type with two variants:

```gala
sealed type Option[T any] {
    case Some(Value T)
    case None()
}
```

Use `Option` anywhere you would reach for `nil` in Go. It forces the caller to acknowledge the absence case.

### Creating Options

```gala
val present = Some(42)          // Option[int] containing 42
val absent = None[int]()        // Option[int] with no value
```

### Map and FlatMap

Transform the contained value without unwrapping. If the Option is `None`, the operation is skipped:

```gala
val opt = Some(10)
val doubled = opt.Map((x) => x * 2)         // Some(20)
val none = None[int]()
val still_none = none.Map((x) => x * 2)     // None[int]

// FlatMap chains operations that return Option
val result = opt.FlatMap((x) => if (x > 5) Some(x) else None[int]())
```

### GetOrElse and OrElse

Extract the value with a fallback:

```gala
val value = opt.GetOrElse(0)                 // 42
val fallback = None[int]().GetOrElse(0)      // 0

// OrElse provides a fallback Option
val primary = None[string]()
val backup = Some("default")
val result = primary.OrElse(backup)          // Some("default")
```

### Filter

Keep the value only if a predicate holds:

```gala
val positive = Some(42).Filter((x) => x > 0)    // Some(42)
val negative = Some(-1).Filter((x) => x > 0)    // None[int]
```

### Side Effects

`OnSome` and `OnNone` run side effects and return the original Option for chaining:

```gala
val opt = Some(42)
opt.OnSome((v) => { Println(s"Found: $v") })
   .OnNone(() => { Println("Not found") })
   .Map((i) => i * 2)
```

### Pattern Matching on Option

Because `Option` is a sealed type, matching is exhaustive — no `case _` needed:

```gala
val msg = opt match {
    case Some(v) => s"Got: $v"
    case None()  => "Empty"
}
```

### Key Methods

| Method | Description |
|--------|-------------|
| `IsDefined()` / `IsEmpty()` | Check the state |
| `Get()` | Get value or panic |
| `GetOrElse(default)` | Get value or return default |
| `OrElse(alternative)` | Return this if Some, otherwise alternative |
| `OnSome(f)` / `OnNone(f)` | Execute side-effect, return original |
| `Map(f)` | Transform value if Some |
| `FlatMap(f)` | Chain operations returning Option |
| `Filter(predicate)` | Keep Some if predicate holds |
| `ForEach(f)` | Apply procedure if nonempty |

---

## Either[A, B]: Left and Right

`Either[A, B]` represents a value that is one of two types. By convention, `Left` carries the error and `Right` carries the success value:

```gala
sealed type Either[A any, B any] {
    case Left(LeftValue A)
    case Right(RightValue B)
}
```

### Creating Either Values

```gala
val success = Right[string, int](42)
val failure = Left[string, int]("not found")
```

### Map and FlatMap on Right

Operations are biased toward `Right`. If the value is `Left`, the operation is skipped and the error propagates:

```gala
val result = Right[string, int](5)
    .Map((x) => x * 10)                                    // Right(50)
    .FlatMap((x) => Right[string, int](x + 1))             // Right(51)

val error = Left[string, int]("oops")
    .Map((x) => x * 10)                                    // Left("oops") — skipped
```

### Chaining

Chain multiple operations. The first `Left` short-circuits the rest:

```gala
val chained = Right[string, int](3)
    .Map((x) => x * 10)
    .FlatMap((x) => Right[string, int](x + 1))
// Right(31)
```

### Side Effects

`OnRight` and `OnLeft` run side effects and return the original Either:

```gala
val logged = Right[string, int](42)
    .OnRight((v) => { Println(s"Success: $v") })
    .OnLeft((e) => { Println(s"Error: $e") })
    .Map((x) => x * 2)
```

### Pattern Matching on Either

Exhaustive matching — no default case needed:

```gala
val msg = result match {
    case Left(code)  => s"Error code: $code"
    case Right(s)    => "Result: " + s
}
```

---

## Try[T]: Success and Failure

`Try[T]` wraps a computation that may succeed or fail with an error. It catches panics and turns them into `Failure` values:

```gala
sealed type Try[T any] {
    case Success(Value T)
    case Failure(Err error)
}
```

### Creating Try Values

```gala
// Direct construction
val success = Success(42)
val failure = Failure[int](fmt.Errorf("oops"))

// Wrapping a failable computation — catches panics
val result = Try(riskyDivide(10, 0))     // Failure
val safe = Try(riskyDivide(10, 2))       // Success(5)

// Function reference sugar — no lambda needed for zero-arg functions
val dir = Try(os.TempDir)
```

### Map and FlatMap

Transform success values. Failures propagate untouched:

```gala
val doubled = Success(21).Map((n) => n * 2)            // Success(42)
val chained = Success(10).FlatMap((n) => divide(n, 2)) // Success(5) or Failure
```

### Recover and RecoverWith

Handle failures gracefully:

```gala
val recovered = Failure[int](fmt.Errorf("oops")).Recover((e) => 0)
// Success(0)

val recoveredWith = Failure[int](fmt.Errorf("oops")).RecoverWith((e) => Success(0))
// Success(0)
```

### Side Effects

`OnSuccess` and `OnFailure` run side effects and return the original Try:

```gala
val logged = Success(42)
    .OnSuccess((n) => { Println(s"Got: $n") })
    .OnFailure((e) => { Println(s"Error: ${e.Error()}") })
    .Map((x) => x * 2)
```

### Safe Extraction

```gala
val value = failure.GetOrElse(0)              // 0
val alternative = failure.OrElse(Success(100)) // Success(100)
```

### Conversion

```gala
val opt = Success(42).ToOption()       // Some(42)
val either = Success(42).ToEither()    // Right(42)
```

### Pattern Matching on Try

Exhaustive — no default case needed:

```gala
val msg = result match {
    case Success(n) => s"Got: $n"
    case Failure(e) => s"Error: ${e.Error()}"
}
```

### Railway-Oriented Programming

Try enables elegant pipelines where errors short-circuit the chain:

```gala
func processOrder(id int) Try[Receipt] =
    fetchOrder(id)
        .FlatMap((o) => validateOrder(o))
        .FlatMap((o) => chargePayment(o))
        .FlatMap((o) => createReceipt(o))
        .RecoverWith((e) => {
            logError(e)
            return Failure[Receipt](e)
        })
```

### Key Methods

| Method | Description |
|--------|-------------|
| `Try(f)` | Execute f, catch panics as Failure |
| `IsSuccess()` / `IsFailure()` | Check the state |
| `Get()` | Get value or panic |
| `GetOrElse(default)` | Get value or return default |
| `OrElse(alternative)` | Return this if Success, otherwise alternative |
| `OnSuccess(f)` / `OnFailure(f)` | Execute side-effect, return original |
| `Map(f)` | Transform value if Success |
| `FlatMap(f)` | Chain operations returning Try |
| `Filter(predicate)` | Keep Success if predicate holds |
| `Recover(f)` | Recover from Failure with a value |
| `RecoverWith(f)` | Recover from Failure with a new Try |
| `ToOption()` | Convert to Option |
| `ToEither()` | Convert to Either[error, T] |

---

## Json: Type-Safe JSON with Try

GALA's `json` package integrates with `Try[T]` for safe JSON handling. A `Codec[T]` serializes and parses with zero reflection; every operation returns `Try` — no unchecked errors. The naming strategy (`AsIs`, `SnakeCase`, `CamelCase`, `KebabCase`) controls how field names map to keys.

### Serialization

```gala
import . "martianoff/gala/json"

type Config struct {
    var Host string
    var Port int
}

val codec = Codec[Config](AsIs())
val config = Config{Host: "localhost", Port: 8080}
val jsonStr = codec.Encode(config).Get()
// => {"Host":"localhost","Port":8080}

val pretty = codec.EncodePretty(config).Get()
// => {
//   "Host": "localhost",
//   "Port": 8080
// }
```

### Deserialization

```gala
val parsed = codec.Decode(jsonStr)
// parsed: Try[Config]

val host = parsed.Map((c) => c.Host).GetOrElse("unknown")
```

### Json Pattern Matching

A codec doubles as a pattern-matching extractor — it parses JSON inside `match` expressions:

```gala
val result = inputStr match {
    case codec(c) => s"Host: ${c.Host}, Port: ${c.Port}"
    case _ => "invalid config"
}
```

This combines `Try`-based safety with pattern matching — the codec's extractor returns `None` on parse failure, so the `case _` branch handles malformed input.

---

## Chaining: Composing Operations into Pipelines

The real power of monadic error handling is composition. Instead of checking errors at every step, you build a pipeline and handle errors at the end:

```gala
val name = user.Name
    .Map((n) => strings.ToUpper(n))
    .GetOrElse("ANONYMOUS")
```

Compare this to the Go equivalent:

<table>
<tr><th>GALA</th><th>Go</th></tr>
<tr>
<td>

<pre><code class="language-gala">val result = divide(10, 2)
    .Map((x) => x * 2)
    .FlatMap((x) => divide(x, 3))
    .Recover((e) => 0)</code></pre>

</td>
<td>

<pre><code class="language-go">result, err := divide(10, 2)
if err == nil {
    result, err = divide(result*2, 3)
}
if err != nil {
    result = 0
}</code></pre>

</td>
</tr>
</table>

The GALA version reads top-to-bottom. The happy path is the main path. Error handling is a single `Recover` at the end.

---

## Monadic Binding: `bind` and `also`

`Map`/`FlatMap` chains are great for a single linear pipeline. `bind`/`also` do-notation earns its keep on two shapes they handle badly: reusing an earlier value several steps later (a **graph**), and combining **independent** steps — where `also` unlocks error *accumulation* and *concurrency* that a `FlatMap` chain cannot express at all. Inside a function whose result is a monad, `bind name = expr` unwraps and names each step; the block lowers to the same `FlatMap` chain.

### The "graph" case — reuse an earlier value later

The final `Receipt` needs both the original order **and** the payment, so a `FlatMap` chain must nest — each value is trapped in a closure, the accumulator type `[Receipt]` is repeated at every link, and the block ends in a pile of closing parens:

**Before — nested `FlatMap`** (`o` survives only via the deepening indentation):

```gala
func processOrder(id int) Try[Receipt] =
    fetchOrder(id).FlatMap[Receipt]((o) =>
    validateOrder(o).FlatMap[Receipt]((valid) =>
    chargePayment(valid).FlatMap[Receipt]((payment) =>
    Success(Receipt(o.Id, payment)))))
```

**After — a flat `bind` block** (every value stays in scope, reads top-to-bottom):

```gala
func processOrder(id int) Try[Receipt] {
    bind o = fetchOrder(id)
    bind valid = validateOrder(o)
    bind payment = chargePayment(valid)
    Success(Receipt(o.Id, payment))   // `o` still in scope; first Failure short-circuits
}
```

### Error accumulation — report ALL invalid fields

`bind` is fail-fast: over `Try`/`Option`/`Either` the first failure wins. `also` marks **independent** clauses, and over `Validated` (in the `validation` package) it accumulates *every* error instead of stopping at the first:

```gala
import . "martianoff/gala/validation"

func makePerson(name string, email string, age int) Validated[string, Person] {
    bind n = vName(name)
    also e = vEmail(email)
    also a = vAge(age)
    Valid(Person(n, e, a))
}
```

`makePerson("", "", -1).GetErrors().Size()` returns **3** — all three failures at once, not just the first. A `FlatMap` chain cannot do this: it is fail-fast, so it stops at the first error. Swap the `also`s for `bind`s and you'd get `1`.

### Concurrency — run independent clauses in parallel

Over `Future`, an `also` group runs its clauses **concurrently** rather than threading each through the next:

```gala
func total() Future[int] {
    bind a = compute(2)
    also b = compute(3)   // independent — all three run at once
    also c = compute(4)
    Future[int](a + b + c)
}
```

Bound names are immutable `val`s, and `bind`/`also` work over any user-defined monad that provides a `FlatMap` method. See the [language reference](/docs/language-reference/) for the full specification.

---

## When Go's Error Handling Is Fine

GALA's monadic types are not always the right tool. Honest trade-offs:

- **Simple one-shot errors** — If a function calls one fallible operation and returns, `if err != nil` is perfectly clear. Wrapping it in `Try` adds indirection without benefit.
- **Performance-critical paths** — `Option`, `Either`, and `Try` allocate wrapper structs. In hot loops processing millions of items, Go's zero-cost error returns may be preferable.
- **Go library interop** — When calling Go functions that return `(T, error)`, you are already in Go's error model. Converting to `Try` at the boundary is useful; converting back and forth repeatedly is not.
- **Team familiarity** — If your team knows Go idioms well and the codebase is small, the learning curve of monadic patterns may not pay off.

The sweet spot for monadic error handling is **multi-step pipelines** where several operations can fail, and you want to keep the code linear and composable.

---

## Further Reading

- [Pattern Matching](/features/pattern-matching/) — all match expression features, including guards and nested patterns
- [Sealed Types](/features/sealed-types/) — how Option, Either, and Try are defined as algebraic data types
- [Immutability](/features/immutability/) — safe data by default
