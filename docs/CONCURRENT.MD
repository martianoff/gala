# Concurrent Package

The `concurrent` package provides asynchronous programming primitives for GALA, including Futures, Promises, and ExecutionContexts. It enables functional concurrent programming with a monadic API similar to Scala's Future.

## Import

```gala
import . "martianoff/gala/concurrent"
```

---

## Future Monad

`Future[T]` represents an asynchronous computation that will eventually produce a value of type T or fail with an error. It provides a functional approach to concurrent programming.

`Future[T]` is a value type (handle pattern) — it wraps a pointer to shared internal state, so it can be passed by value without copying mutable state. You never need `*Future[T]`.

### Creating Futures

```gala
// Already completed with value
val immediate = FutureOf[int](42)

// Already failed
val failed = FutureFailed[int](SomeError("oops"))

// Runs asynchronously
val async = Future[int](expensiveComputation())
```

### Blocking Operations

```gala
val result = async.Await()           // Returns Try[int]
val value = async.Get()              // Returns int, panics on failure
val safe = async.GetOrElse(0)        // Returns int, default on failure
```

### Non-Blocking Callbacks

```gala
async.OnSuccess((v) => Println(s"Got: $v"))
async.OnFailure((e) => Println(s"Error: $e"))
async.OnComplete((r) => Println(s"Result: $r"))
```

### Monadic Operations

```gala
val doubled = async.Map((v) => v * 2)
val chained = async.FlatMap((v) => fetchName(v))
```

### Error Recovery

```gala
val recovered = failed.Recover((e) => 0)
val recoveredWith = failed.RecoverWith((e) => FutureOf[int](0))
```

### Timeouts

Two complementary primitives:

- `AwaitFor(duration) Option[Try[T]]` — blocks the caller; `None` on timeout.
- `WithTimeout(duration) Future[T]` — monadic; the returned Future fails with `TimeoutError` if the original doesn't complete in time, otherwise mirrors its result.

```gala
val slow = Future[int](() => { Sleep(Seconds(2)); 42 })

// Monadic: stays a Future, composes with Map/Recover/etc.
val bounded = slow.WithTimeout(Milliseconds(500))
    .Recover((e) => 0)                            // 0 on timeout

// Blocking: returns Option[Try[T]] inline.
val maybe = slow.AwaitFor(Milliseconds(500))      // None on timeout
```

When the timeout fires, `WithTimeout` also cancels the underlying Future's
cancellation token, so pending downstream stages short-circuit instead of running
after the deadline (see [Cancellation](#cancellation)). The bounded Future is
failed with `TimeoutError` first, so that result wins even though cancellation
also unblocks the chain.

### Combining Futures

```gala
val zipped = f1.Zip(f2)               // Future[Tuple[int, string]]
val first = f1.Fallback(f2)          // First success or last failure
val winner = Race[int](ArrayOf[Future[int]](a, b))  // first result, cancels losers
```

`Race` is the structured-concurrency form of `FirstCompletedOf`: it completes
with the first Future to finish and then cancels the losing Futures' shared
tokens, so their pending downstream stages short-circuit.

### Cancellation

Cancellation is **API-level**: you cancel a Future with `.Cancel()` (or let
`.WithTimeout()` / `Race` cancel it for you). There is no token in user code — a
Future carries an opaque cancellation token internally.

```gala
val chain = source.Map((v) => step1(v)).FlatMap((v) => step2(v))
chain.Cancel()   // pending stages that haven't started fail with CancellationError
```

Three properties define the semantics:

- **Checked at combinator boundaries.** Each derived stage (`Map`, `FlatMap`,
  `Filter`, `Recover`, `RecoverWith`, `Transform`, `TransformWith`, `AndThen`,
  and the `Zip*`/`Fallback` built on them) checks the token *before* it runs.
  Cancelling short-circuits any stage that has not started yet, failing it with a
  `CancellationError`. A single already-running body cannot be preempted — Go has
  no goroutine interruption — so cancellation does not abort work that is
  in-flight; it only prevents downstream stages from running.
- **Graph-level and coarse.** A derived chain shares one token, so cancelling any
  node cancels the whole shared computation. Per-node / sub-tree scoping is future
  work. Scope rule: linear combinators inherit the parent's token, while the
  aggregation / scope constructors (`Race`, `Sequence`, `FirstCompletedOf`,
  `WithTimeout`) intentionally open a fresh token scope.
- **Never on success.** Successful completion does not cancel the token, so
  `.Cancel()` after a Future has completed has no effect on its stored result.

`.Cancel()` is always safe: on a chain that is already complete, or one with no
pending stages, it simply does nothing observable. This makes the whole feature
additive — existing code that never calls `.Cancel()` behaves exactly as before.

Interrupting a long in-flight body (true preemption) is intentionally out of
scope here and would be achieved by other means later.

---

## Pattern Matching on Futures

Futures support pattern matching using extractors. Type parameters can be inferred from the matched Future type:

```gala
val f = FutureOf[int](42)

// With inferred type parameters (preferred)
val msg = f match {
    case Succeeded(v) => s"Got: $v"
    case Failed(e) => s"Error: ${e.Error()}"
    case _ => "Unknown"
}

// With explicit type parameters (when needed)
val msg2 = f match {
    case Succeeded[int](v) => s"Got: $v"
    case Failed[int](e) => s"Error: ${e.Error()}"
    case _ => "Unknown"
}

// Using Completed extractor with nested Try matching
val msg3 = f match {
    case Completed(Success(v)) => s"Success: $v"
    case Completed(Failure(e)) => s"Failure: ${e.Error()}"
    case _ => "Unknown"
}
```

---

## Sequence Operations

```gala
val futures = ArrayOf(FutureOf(1), FutureOf(2), FutureOf(3))

// Sequence: Array[Future[T]] -> Future[Array[T]]
val all = Sequence[int](futures)     // Future[Array[int]]

// Pattern matching on sequences
val msg = futures match {
    case AllSucceeded(values) => s"All: $values"
    case AnyFailed(e) => s"Failed: ${e.Error()}"
    case _ => "Unknown"
}

// First completed
val first = FirstCompletedOf[int](futures)

// Traverse: apply async function to each element
val results = Traverse[int, string](items, (i) => fetchAsync(i))

// Fold: reduce collection of Futures
val sum = Fold[int, int](futures, 0, (acc, v) => acc + v)
```

---

## Promise

`Promise[T]` is a writable, single-assignment container that completes a Future. Use it when you need to complete a Future from external code:

```gala
val promise = NewPromise[int]()
val future = promise.Future()

// Complete the promise (can only be done once)
promise.Success(42)           // or
promise.Failure(someError)    // or
promise.Complete(tryResult)

// Check if completed
val done = promise.IsCompleted()
```

---

## ExecutionContext

Each Future has an associated `ExecutionContext` that determines where callbacks and derived futures execute. By default, futures use `GlobalEC()` which spawns a new goroutine per task.

```gala
// Default: uses GlobalEC (UnboundedExecutionContext)
val f1 = Future[int](compute())

// With custom ExecutionContext
val pool = NewFixedPoolEC(4)  // Worker pool with 4 goroutines
val f2 = FutureOn[int](compute(), pool)

// Derived futures inherit EC from parent
val f3 = f2.Map((n) => s"$n")                             // uses pool
val f4 = f3.FlatMap((s) => anotherFuture(s))              // uses pool

// Cleanup
pool.Shutdown()
```

### Available ExecutionContext Implementations

| Type | Description |
|------|-------------|
| `UnboundedExecutionContext` | Default - spawns new goroutine per task |
| `FixedPoolExecutionContext` | Worker pool with N goroutines |
| `SingleThreadExecutionContext` | Sequential execution (useful for testing) |

### Factory Functions

| Function | Description |
|----------|-------------|
| `GlobalEC()` | Returns the global default EC |
| `SetGlobalEC(ec)` | Sets the global default EC |
| `NewFixedPoolEC(n)` | Creates a pool with n workers |
| `NewSingleThreadEC()` | Creates a single-thread executor |

### Async Utilities

| Function | Description |
|----------|-------------|
| `Spawn(f)` | Start a new goroutine executing function f |

> **Note:** For sleeping, use `time_utils.Sleep(Duration)` (GALA-idiomatic) or `time.Sleep(time.Duration)` (Go native).

### Constructor Variants with Custom EC

The async constructor `Future[T](body)` always runs on `GlobalEC()`; use
`FutureOn` for a custom EC. The eager constructors and `NewPromise` take an
optional trailing `ec` argument (default `GlobalEC()`).

| Default EC (GlobalEC) | Custom EC |
|-----------------------|-----------|
| `Future[T](f)` | `FutureOn[T](f, ec)` |
| `FutureOf[T](v)` | `FutureOf[T](v, ec)` |
| `FutureFailed[T](e)` | `FutureFailed[T](e, ec)` |
| `NewPromise[T]()` | `NewPromise[T](ec)` |

---

## Method Reference

### Future Methods

| Method | Description |
|--------|-------------|
| `IsCompleted()` | Check if the Future has completed |
| `Value()` | Get current result as Option[Try[T]] |
| `Await()` | Block until completion, returns Try[T] |
| `AwaitFor(duration)` | Block with timeout, returns Option[Try[T]] |
| `WithTimeout(duration)` | Return Future that fails with TimeoutError if not done in time (inherits EC) |
| `Get()` | Block and get value, panics on failure |
| `GetOrElse(default)` | Block and get value or default |
| `Cancel()` | Request cancellation; a pending computation fails with `CancellationError` (see [Cancellation](#cancellation)) |
| `ExecutionContext()` | Get the associated ExecutionContext |
| `OnComplete(callback)` | Run `callback` with the `Try[T]` when the Future completes (inherits EC) |
| `OnSuccess(callback)` | Run `callback` with the value on success (inherits EC) |
| `OnFailure(callback)` | Run `callback` with the error on failure (inherits EC) |
| `Map[U](f)` | Transform successful value (inherits EC) |
| `FlatMap[U](f)` | Chain with function returning Future (inherits EC) |
| `Filter(predicate)` | Keep value if predicate holds (inherits EC) |
| `Recover(f)` | Recover from failure with value (inherits EC) |
| `RecoverWith(f)` | Recover from failure with new Future (inherits EC) |
| `Transform[U](s, f)` | Map success via `s` and failure via `f`, each returning `Try[U]` (inherits EC) |
| `TransformWith[U](s, f)` | Map success/failure to a new `Future[U]` (inherits EC) |
| `Zip[U](other)` | Combine two Futures into tuple (inherits EC) |
| `ZipWith[U,V](other, f)` | Combine with function (inherits EC) |
| `Zip2[U](other)` … `Zip10[...]` | Combine 2–10 Futures into a `TupleN`; the highest that completes fails the whole if any fails (inherits EC) |
| `Fallback(other)` | Use other Future if this fails (inherits EC) |
| `AndThen(callback)` | Execute callback, return same result (inherits EC) |
| `ToTry()` / `ToOption()` / `ToEither()` | Convert result |

### Promise Methods

A Promise completes exactly once. `Success`, `Failure`, and `Complete` return a
`bool`: `true` if this call completed the Promise, `false` if it was already
completed (the later call is a no-op). Use the return value when a Promise may be
raced by multiple producers and you need to know which one won.

| Method | Description |
|--------|-------------|
| `Future()` | Get the associated Future |
| `IsCompleted()` | Check if promise is completed |
| `Success(value)` | Complete with success; returns `bool` (`true` if this call completed it) |
| `Failure(error)` | Complete with failure; returns `bool` (`true` if this call completed it) |
| `Complete(tryResult)` | Complete with a `Try` result; returns `bool` (`true` if this call completed it) |

### Sequence Functions

| Function | Description |
|----------|-------------|
| `Sequence[T](futures)` | Convert `Array[Future[T]]` to `Future[Array[T]]` |
| `FirstCompletedOf[T](futures)` | First Future to complete |
| `Traverse[T, U](items, f)` | Apply async function to each item |
| `Fold[T, U](futures, zero, f)` | Reduce Futures with binary function |

### Pattern Matching Extractors

| Extractor | Description |
|-----------|-------------|
| `Succeeded[T]` | Matches completed Future with success, extracts value |
| `Failed[T]` | Matches completed Future with failure, extracts error |
| `Completed[T]` | Matches any completed Future, extracts Try[T] |
| `AllSucceeded` | Matches Array of Futures where all succeeded |
| `AnyFailed` | Matches Array of Futures where any failed |
