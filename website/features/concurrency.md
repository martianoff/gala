---
layout: default
title: "Golang Futures — Composable Async Programming for Go"
description: "GALA's Future[T] brings composable async programming to Go — Map, FlatMap, Zip, Recover, and Await built on goroutines, plus structured concurrency: cancellation, timeouts, and Race. Functional concurrency on top of Go's runtime."
keywords: "golang future, go future monad, golang async await, go promise, golang composable concurrency, go functional concurrency, golang goroutine future, go async pattern, golang future cancellation, go structured concurrency, gala future"
permalink: /features/concurrency/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/">Features</a> / Concurrency</p>

# Concurrency — Futures and Promises for Go

GALA brings **composable, functional concurrency** to Go. The `concurrent` package provides `Future[T]` and `Promise[T]` types that run on Go's goroutine runtime but expose a monadic API — `Map`, `FlatMap`, `Zip`, `Recover` — so you can compose asynchronous operations without callback nesting or manual channel management.

Every Future runs on an `ExecutionContext` that controls goroutine scheduling. The default spawns one goroutine per task. Worker pools and single-thread executors are available for fine-grained control.

Two things make Futures more than a callback wrapper: [structured concurrency](#structured-concurrency--cancellation-timeouts-and-race) — cancellation, timeouts, and `Race` that short-circuit the stages they own — and [compile-time data-race safety]({{ '/features/concurrency-safety/' | relative_url }}), which statically checks that an async body only captures values that are safe to share.

```gala
import . "martianoff/gala/concurrent"

val f1 = Future[int](expensiveComputation())
val f2 = Future[string](fetchName())

val combined = f1.Zip(f2)
    .Map((pair) => s"Result: ${pair.V1} from ${pair.V2}")

Println(combined.Get())
```

---

## Creating Futures

`Future[T]` represents an asynchronous computation that will eventually produce a value of type T or fail with an error.

```gala
import . "martianoff/gala/concurrent"
import "errors"

// Run a computation asynchronously in a goroutine
val async = Future[int](expensiveComputation())

// Already completed with a known value
val immediate = FutureOf[int](42)

// Already failed
val failed = FutureFailed[int](errors.New("oops"))
```

`Future[T]` is a value type (handle pattern) — pass it by value, never as `*Future[T]`.

---

## Map and FlatMap — Transforming Async Results

`Map` transforms a successful result. `FlatMap` chains a function that returns another Future. Both propagate errors automatically:

```gala
val userId = Future[int](lookupUserId("alice"))

// Map: transform the result
val greeting = userId.Map((id) => s"User #$id")

// FlatMap: chain another async operation
val profile = userId.FlatMap((id) => Future[string](fetchProfile(id)))
```

If the original Future fails, Map and FlatMap short-circuit — the error propagates through the chain without executing the transform function.

---

## Zip — Combining Parallel Futures

`Zip` runs two Futures concurrently and combines their results into a Tuple when both complete:

```gala
val f1 = Future[int](fetchCount())
val f2 = Future[string](fetchLabel())

val combined = f1.Zip(f2)  // Future[Tuple[int, string]]
val pair = combined.Get()
Println(s"${pair.V2}: ${pair.V1}")
```

Use `ZipWith` to combine results with a custom function:

```gala
val total = f1.ZipWith(f2, (count, label) => s"$label = $count")
```

---

## Recover — Handling Async Errors

`Recover` provides a fallback value when a Future fails. `RecoverWith` provides a fallback Future:

```gala
val risky = Future[int](riskyOperation())

// Recover with a default value
val safe = risky.Recover((e) => 0)

// Recover with another Future
val retried = risky.RecoverWith((e) => Future[int](fallbackOperation()))

// Fallback: use another Future if this one fails
val withFallback = risky.Fallback(FutureOf[int](0))
```

---

## Await — Waiting with Timeouts

Block the current goroutine until a Future completes:

```gala
val f = Future[int](compute())

// Block indefinitely, get Try[T]
val result = f.Await()           // Try[int]

// Block and get the value directly (panics on failure)
val value = f.Get()              // int

// Block with a safe default
val safe = f.GetOrElse(0)       // int — returns 0 on failure

// Block with a deadline — None on timeout
val maybe = f.AwaitFor(Milliseconds(500))   // Option[Try[int]]
```

---

## Structured Concurrency — Cancellation, Timeouts, and Race

Async work that cannot be called off is a leak waiting to happen. `Future[T]` carries an opaque cancellation token internally, and three constructs use it to bound a computation's lifetime. **There is no token in user code** — cancellation is API-level.

### Cancel a chain

```gala
val chain = source.Map((v) => step1(v)).FlatMap((v) => step2(v))
chain.Cancel()   // pending stages that haven't started fail with CancellationError
```

Three properties define the semantics:

- **Checked at combinator boundaries.** Each derived stage — `Map`, `FlatMap`, `Filter`, `Recover`, `RecoverWith`, `Transform`, `TransformWith`, `AndThen`, and the `Zip*`/`Fallback` built on them — checks the token *before* it runs. Cancelling short-circuits any stage that has not started, failing it with a `CancellationError`.
- **Graph-level and coarse.** A derived chain shares one token, so cancelling any node cancels the whole shared computation. Linear combinators inherit the parent's token; the aggregation constructors (`Race`, `Sequence`, `FirstCompletedOf`, `WithTimeout`) deliberately open a fresh token scope.
- **Never on success.** Successful completion does not cancel the token, so `.Cancel()` after a Future has completed has no effect on its stored result.

`.Cancel()` is always safe: on a chain that is already complete, or one with no pending stages, it does nothing observable. The whole feature is additive — code that never calls `.Cancel()` behaves exactly as before.

**The honest limit:** an already-running body cannot be preempted. Go has no goroutine interruption, so cancellation never aborts work that is in flight — it only prevents downstream stages from starting. True preemption is out of scope.

### Bound a Future with a timeout

Two complementary primitives: `WithTimeout` stays monadic, `AwaitFor` blocks.

```gala
val slow = Future[int](() => { Sleep(Seconds(2)); 42 })

// Monadic: stays a Future, composes with Map/Recover/etc.
val bounded = slow.WithTimeout(Milliseconds(500))
    .Recover((e) => 0)                            // 0 on timeout

// Blocking: returns Option[Try[T]] inline.
val maybe = slow.AwaitFor(Milliseconds(500))      // None on timeout
```

When the timeout fires, `WithTimeout` also cancels the underlying Future's token, so pending downstream stages short-circuit instead of running past the deadline. The bounded Future is failed with `TimeoutError` first, so that result wins even though cancellation also unblocks the chain.

### Race — first result wins, losers are cancelled

```gala
val winner = Race[int](ArrayOf[Future[int]](a, b))  // first result, cancels losers
```

`Race` is the structured-concurrency form of `FirstCompletedOf`: it completes with the first Future to finish, then cancels the losing Futures' shared tokens so their pending downstream stages short-circuit.

---

## Non-Blocking Callbacks

Register callbacks that fire when a Future completes, without blocking:

```gala
val f = Future[int](compute())

f.OnSuccess((v) => Println(s"Got: $v"))
f.OnFailure((e) => Println(s"Error: $e"))
f.OnComplete((r) => Println(s"Result: $r"))
```

---

## Pattern Matching on Futures

Futures support extractors for pattern matching. Type parameters are inferred from the Future type:

```gala
val f = FutureOf[int](42)

val msg = f match {
    case Succeeded(v) => s"Got: $v"
    case Failed(e) => s"Error: ${e.Error()}"
    case _ => "Unknown"
}
```

Nested matching with the `Completed` extractor and `Try`:

```gala
val msg = f match {
    case Completed(Success(v)) => s"Success: $v"
    case Completed(Failure(e)) => s"Failure: ${e.Error()}"
    case _ => "Unknown"
}
```

---

## Sequence Operations — Working with Multiple Futures

Combine arrays of Futures into a single Future:

```gala
val futures = ArrayOf(FutureOf(1), FutureOf(2), FutureOf(3))

// Sequence: Array[Future[T]] -> Future[Array[T]]
val all = Sequence[int](futures)     // Future[Array[int]]

// First completed
val first = FirstCompletedOf[int](futures)

// Traverse: apply async function to each element
val results = Traverse[int, string](items, (i) => fetchAsync(i))

// Fold: reduce Futures with a binary function
val sum = Fold[int, int](futures, 0, (acc, v) => acc + v)
```

Pattern matching on Future arrays:

```gala
val msg = futures match {
    case AllSucceeded(values) => s"All: $values"
    case AnyFailed(e) => s"Failed: ${e.Error()}"
    case _ => "Unknown"
}
```

---

## ExecutionContext

Each Future has an associated `ExecutionContext` that determines where callbacks and derived futures execute.

### Available Implementations

| Type | Description |
|------|-------------|
| `UnboundedExecutionContext` | Default — spawns a new goroutine per task |
| `FixedPoolExecutionContext` | Worker pool with N goroutines |
| `SingleThreadExecutionContext` | Sequential execution (useful for testing) |

### Using a Custom ExecutionContext

```gala
import . "martianoff/gala/concurrent"

// Create a worker pool with 4 goroutines
val pool = NewFixedPoolEC(4)

// Run futures on the pool
val f1 = FutureOn[int](compute(), pool)

// Derived futures inherit the parent's EC
val f2 = f1.Map((n) => s"$n")          // also runs on pool
val f3 = f2.FlatMap((s) => fetch(s))   // also runs on pool

// Clean up
pool.Shutdown()
```

---

## Promise[T] — Manual Completion

`Promise[T]` is a writable, single-assignment container that completes a Future. Use it when you need to complete a Future from external code — for example, bridging callback-based APIs:

```gala
import . "martianoff/gala/concurrent"

val promise = NewPromise[int]()
val future = promise.Future()

// Complete the promise from another goroutine (can only be done once)
Spawn(() => {
    val result = expensiveWork()
    promise.Success(result)
})

// Or complete with failure
// promise.Failure(someError)

Println(future.Get())
```

`Success`, `Failure`, and `Complete` each return a `bool` — `true` if that call
completed the Promise, `false` if it was already completed. When several
producers may race to complete the same Promise, the return value tells you which
one won; the losers are safe no-ops.

Note that `Spawn` is the **unchecked** zone: unlike a `Future` body, a closure
passed to `Spawn` is not a `Sendable` boundary, so its captures are not validated
and synchronization is your responsibility. See
[data-race safety]({{ '/features/concurrency-safety/#what-the-check-does-not-cover' | relative_url }}).

---

## When to Use Futures vs Channels

| Scenario | Recommendation |
|----------|----------------|
| One-shot async computation | Future |
| Transform/chain async results | Future (Map, FlatMap) |
| Combine parallel results | Future (Zip, Sequence) |
| Error recovery pipelines | Future (Recover, RecoverWith) |
| Streaming data between goroutines | Go channels |
| Fan-out / fan-in patterns | Go channels or Future + Sequence |
| Select across multiple sources | Go channels with `select` |

Futures and channels are complementary. Use Futures for composable one-shot operations and Go channels for streaming communication.

---

## Example: Parallel Data Fetch with Composition

```gala
package main

import . "martianoff/gala/concurrent"

func main() {
    // Launch three independent async operations
    val userF    = Future[string](fetchUser())
    val ordersF  = Future[int](fetchOrderCount())
    val balanceF = Future[float64](fetchBalance())

    // Combine user and orders in parallel
    val summary = userF.ZipWith(ordersF, (user, orders) =>
        s"$user has $orders orders")

    // Chain balance lookup after summary
    val full = summary.FlatMap((s) =>
        balanceF.Map((b) => f"$s, balance: $$$b%.2f"))

    // Recover from any failure
    val safe = full.Recover((e) => s"Error: ${e.Error()}")

    Println(safe.Get())
}
```

---

## Further Reading

- [Compile-Time Data-Race Safety]({{ '/features/concurrency-safety/' | relative_url }}) — why a `Future` body may only capture deeply-immutable values, and what GALA-E0037 tells you
- [Subprocess]({{ '/docs/subprocess/' | relative_url }}) — drive child processes off-thread with Future-returning async methods
- [Error Handling with Try and Either]({{ '/features/error-handling/' | relative_url }}) — `Try[T]` connects directly with Future results
- [Functional Collections]({{ '/features/collections/' | relative_url }}) — use `Sequence` and `Traverse` with collection types
- [Type Inference]({{ '/features/type-inference/' | relative_url }}) — lambda parameter inference in async pipelines
