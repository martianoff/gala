---
layout: default
title: "IO Effect in GALA — Lazy Composable Side Effects for Go"
description: "GALA's IO[T] is a lazy, composable effect type that separates pure and impure code. Build effect pipelines with Map, FlatMap, Recover, and AndThen — execute only when you call Run."
keywords: "gala io effect, golang io monad, go side effect management, gala lazy effects, go functional effects, gala io type, golang effect system"
permalink: /docs/io/
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / IO Effect</p>

# IO[T] — Lazy Composable Effects

`IO[T]` is a lazy, composable effect type for separating pure and impure code. An `IO[T]` value **describes** a computation that produces `T` or fails — but does not execute it. Effects only run when you call `.Run()`. This makes side-effecting code referentially transparent: you can pass IO values around, compose them, and reason about them without triggering any effects.

```gala
import "martianoff/gala/io"
```

---

## Creating IO Values

```gala
// Pure value — no side effects
val pure = io.Of(42)

// Lazy thunk — panics are caught as Failure
val suspended = io.Suspend(() => expensiveComputation())

// Failing IO
val failing = io.Fail[int](errors.New("boom"))

// Side-effecting void operation
val logging = io.Effect(() => { Println("log message") })

// From an existing Try
val fromTry = io.FromTry(Success(42))
```

---

## Running IO

Nothing happens until you call `.Run()`:

```gala
Println("Before IO creation")
val lazy = io.Suspend(() => {
    Println("Computing...")
    return 99
})
Println("IO created but not run")

// Now execute:
val result = lazy.Run()        // Try[int] — prints "Computing..."
val value = lazy.UnsafeRun()   // int — 99 (panics on failure)
```

Unlike `Lazy[T]`, which caches its result, `IO[T]` re-executes on every `.Run()` call. This is intentional — side effects should be repeatable.

---

## Composing Effects

### Map — Transform the Result

```gala
val doubled = io.Map[int, int](io.Of(21), (x int) => x * 2)
doubled.Run()  // Success(42)
```

### FlatMap — Chain Dependent Computations

```gala
val chained = io.FlatMap[int, int](io.Of(10), (x int) => io.Of(x + 32))
chained.Run()  // Success(42)
```

### AndThen — Sequence, Discard First Result

```gala
val program = io.AndThen[bool, string](
    io.Effect(() => { Println("setup") }),
    io.Of("done"),
)
program.Run()  // prints "setup", returns Success("done")
```

### ForEach — Side Effect on Success

```gala
io.ForEach(io.Of(42), (v) => { Println(v) }).Run()
// prints 42
```

---

## Error Handling

Failures propagate through the chain automatically:

```gala
val failChain = io.FlatMap[int, int](
    io.Fail[int](errors.New("initial error")),
    (x int) => io.Of(x * 2),
)
failChain.Run().IsFailure()  // true — second step never runs
```

### Recover — Handle Errors with a Value

```gala
val safe = io.Recover(
    io.Fail[int](errors.New("oops")),
    (err) => -1,
)
safe.Run()  // Success(-1)
```

### RecoverWith — Handle Errors with a New IO

```gala
val retried = io.RecoverWith(
    io.Fail[int](errors.New("oops")),
    (err) => io.Suspend(() => fallbackComputation()),
)
```

---

## Building Programs

IO shines when composing multi-step programs where each step may have side effects:

```gala
import "martianoff/gala/io"
import "errors"

func fetchData() int = 42
func process(n int) int = n * 2
func save(n int) bool {
    Println(s"Saving $n")
    return true
}

val program = io.FlatMap[int, bool](
    io.Suspend(fetchData),
    (data int) => io.FlatMap[int, bool](
        io.Of(process(data)),
        (result int) => io.Suspend(() => save(result)),
    ),
)

// Nothing has happened yet — program is just a description.
// Now run it:
program.Run()  // prints "Saving 84", returns Success(true)
```

---

## IO vs Lazy

| | <code>Lazy[T]</code> | <code>IO[T]</code> |
|---|---|---|
| Re-execution | Cached — <code>.Get()</code> returns same result | Fresh — every <code>.Run()</code> re-executes |
| Thread safety | Yes (via <code>sync.Once</code>) | No memoization |
| Use case | Expensive pure computations | Side effects (HTTP, file I/O, logging) |

---

## API Reference

| Function | Description |
|----------|-------------|
| <code>io.Of(value)</code> | Pure value, no effects |
| <code>io.Suspend(f)</code> | Lazy thunk, panics caught |
| <code>io.Fail[T](err)</code> | Failing IO |
| <code>io.Effect(f)</code> | Void side effect, returns <code>IO[bool]</code> |
| <code>io.FromTry(t)</code> | Wrap existing Try |
| <code>io.Unit()</code> | No-op IO, returns <code>IO[bool]</code> |
| <code>io.Map[T, U](io, f)</code> | Transform success value |
| <code>io.FlatMap[T, U](io, f)</code> | Chain dependent computations |
| <code>io.AndThen[T, U](first, second)</code> | Sequence, discard first result |
| <code>io.Recover(io, f)</code> | Handle error with fallback value |
| <code>io.RecoverWith(io, f)</code> | Handle error with fallback IO |
| <code>io.ForEach(io, f)</code> | Side effect on success |
| <code>.Run()</code> | Execute, returns <code>Try[T]</code> |
| <code>.UnsafeRun()</code> | Execute, returns T or panics |

---

## Further Reading

- [Error Handling](/features/error-handling/) — Option, Either, and Try monads
- [Concurrency](/features/concurrency/) — Future and Promise for async computation
- [Collections](/features/collections/) — functional collections with Map, FlatMap, FoldLeft
