# Concurrent Package

The `concurrent` package provides asynchronous programming primitives for GALA, including Futures, Promises, and ExecutionContexts. It enables functional concurrent programming with a monadic API similar to Scala's Future.

## Import

```gala
import . "martianoff/gala/concurrent"
```

---

## Future Monad

`Future[T]` represents an asynchronous computation that will eventually produce a value of type T or fail with an error. It provides a functional approach to concurrent programming.

### Creating Futures

```gala
// Already completed with value
val immediate = FutureOf[int](42)

// Already failed
val failed = FutureFailed[int](SomeError("oops"))

// Runs asynchronously
val async = FutureApply[int](() => expensiveComputation())
```

### Blocking Operations

```gala
val result = async.Await()           // Returns Try[int]
val value = async.Get()              // Returns int, panics on failure
val safe = async.GetOrElse(0)        // Returns int, default on failure
```

### Non-Blocking Callbacks

```gala
async.OnSuccess((v int) => fmt.Println("Got:", v))
async.OnFailure((e error) => fmt.Println("Error:", e))
async.OnComplete((r Try[int]) => fmt.Println("Result:", r))
```

### Monadic Operations

```gala
val doubled = async.Map[int]((v int) => v * 2)
val chained = async.FlatMap[string]((v int) => fetchName(v))
```

### Error Recovery

```gala
val recovered = failed.Recover((e error) => 0)
val recoveredWith = failed.RecoverWith((e error) => FutureOf[int](0))
```

### Combining Futures

```gala
val zipped = f1.Zip[string](f2)      // Future[Tuple[int, string]]
val first = f1.Fallback(f2)          // First success or last failure
```

---

## Pattern Matching on Futures

Futures support pattern matching using extractors. Type parameters can be inferred from the matched Future type:

```gala
val f = FutureOf[int](42)

// With inferred type parameters (preferred)
val msg = f match {
    case Succeeded(v) => fmt.Sprintf("Got: %d", v)
    case Failed(e) => fmt.Sprintf("Error: %s", e.Error())
    case _ => "Unknown"
}

// With explicit type parameters (when needed)
val msg2 = f match {
    case Succeeded[int](v) => fmt.Sprintf("Got: %d", v)
    case Failed[int](e) => fmt.Sprintf("Error: %s", e.Error())
    case _ => "Unknown"
}

// Using Completed extractor with nested Try matching
val msg3 = f match {
    case Completed(Success(v)) => fmt.Sprintf("Success: %d", v)
    case Completed(Failure(e)) => fmt.Sprintf("Failure: %s", e.Error())
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
    case AllSucceeded(values) => fmt.Sprintf("All: %v", values)
    case AnyFailed(e) => fmt.Sprintf("Failed: %s", e.Error())
    case _ => "Unknown"
}

// First completed
val first = FirstCompletedOf[int](futures)

// Traverse: apply async function to each element
val results = Traverse[int, string](items, (i int) => fetchAsync(i))

// Fold: reduce collection of Futures
val sum = Fold[int, int](futures, 0, (acc int, v int) => acc + v)
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
val f1 = FutureApply[int](() => compute())

// With custom ExecutionContext
val pool = NewFixedPoolEC(4)  // Worker pool with 4 goroutines
val f2 = FutureApplyWith[int](() => compute(), pool)

// Derived futures inherit EC from parent
val f3 = f2.Map[string]((n int) => fmt.Sprintf("%d", n))  // uses pool
val f4 = f3.FlatMap[int]((s string) => anotherFuture(s))  // uses pool

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
| `Sleep(duration)` | Pause the current goroutine for the given duration |

### Constructor Variants with Custom EC

| Default | With EC |
|---------|---------|
| `FutureApply[T](f)` | `FutureApplyWith[T](f, ec)` |
| `FutureOf[T](v)` | `FutureOfWith[T](v, ec)` |
| `FutureFailed[T](e)` | `FutureFailedWith[T](e, ec)` |
| `NewPromise[T]()` | `NewPromiseWith[T](ec)` |

---

## Method Reference

### Future Methods

| Method | Description |
|--------|-------------|
| `IsCompleted()` | Check if the Future has completed |
| `Value()` | Get current result as Option[Try[T]] |
| `Await()` | Block until completion, returns Try[T] |
| `AwaitFor(duration)` | Block with timeout, returns Option[Try[T]] |
| `Get()` | Block and get value, panics on failure |
| `GetOrElse(default)` | Block and get value or default |
| `ExecutionContext()` | Get the associated ExecutionContext |
| `Map[U](f)` | Transform successful value (inherits EC) |
| `FlatMap[U](f)` | Chain with function returning Future (inherits EC) |
| `Filter(predicate)` | Keep value if predicate holds (inherits EC) |
| `Recover(f)` | Recover from failure with value (inherits EC) |
| `RecoverWith(f)` | Recover from failure with new Future (inherits EC) |
| `Zip[U](other)` | Combine two Futures into tuple (inherits EC) |
| `ZipWith[U,V](other, f)` | Combine with function (inherits EC) |
| `Fallback(other)` | Use other Future if this fails (inherits EC) |
| `AndThen(callback)` | Execute callback, return same result (inherits EC) |
| `ToTry()` / `ToOption()` / `ToEither()` | Convert result |

### Promise Methods

| Method | Description |
|--------|-------------|
| `Future()` | Get the associated Future |
| `IsCompleted()` | Check if promise is completed |
| `Success(value)` | Complete with success |
| `Failure(error)` | Complete with failure |
| `Complete(tryResult)` | Complete with Try result |

### Sequence Functions

| Function | Description |
|----------|-------------|
| `Sequence[T](futures)` | Convert Array[Future[T]] to Future[Array[T]] |
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
