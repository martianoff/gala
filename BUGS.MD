# GALA Transpiler — Known Bugs

## BUG-050: `match` on method call chain with lambda fails to parse

**Severity**: Medium
**Status**: Open
**Date**: 2026-03-28

### Description

When calling `match` directly on the result of a method call chain that contains a lambda argument, the transpiler produces a `SyntaxError: no viable alternative at input '('`.

### Repro

```gala
package example

import . "martianoff/gala/collection_immutable"

func findAndApply(items Array[Tuple[string, string]], path string) Option[string] =
    items.Find((item) => item.V1 == path) match {
        case Some(item) => Some(item.V2)
        case None() => None[string]()
    }
```

**Expected**: Transpiles correctly — `match` on the result of `items.Find(...)`.

**Actual**:
```
[SyntaxError] line 6:XX no viable alternative at input '('
```

### Workaround

Assign the result to an intermediate `val`, then `match` on the val:

```gala
func findAndApply(items Array[Tuple[string, string]], path string) Option[string] {
    val matched = items.Find((item) => item.V1 == path)
    return matched match {
        case Some(item) => Some(item.V2)
        case None() => None[string]()
    }
}
```

---

## BUG-051: `return` inside `match` arms in expression-bodied function fails

**Severity**: Medium
**Status**: Open
**Date**: 2026-03-28

### Description

Using `return` inside `match` case arms within an expression-bodied function (`= ...`) causes a parse error. The transpiler rejects `return` as an unexpected token inside match arms.

### Repro

```gala
package example

import . "martianoff/gala/collection_immutable"

func process(opt Option[string]) string =
    opt match {
        case Some(v) => {
            Println(v)
            return v
        }
        case None() => return "default"
    }
```

**Expected**: Transpiles correctly — `return` should be allowed inside block match arms.

**Actual**:
```
[SyntaxError] line XX:XX extraneous input 'return' expecting {...}
```

### Workaround

Use block-bodied function (`{ ... }`) instead of expression-bodied (`= ...`):

```gala
func process(opt Option[string]) string {
    return opt match {
        case Some(v) => {
            Println(v)
            v
        }
        case None() => "default"
    }
}
```

---

## BUG-052: If-expression with generic return type transpiles to `any`

**Severity**: Medium
**Status**: Open
**Date**: 2026-03-28

### Description

When using an if-expression (`return if (cond) Some(v) else None[T]()`) where `T` is a concrete type like `time.Time` or `int`, the transpiled Go code wraps the expression in `func() any { ... }()` instead of preserving the concrete `Option[T]` type. This causes a type mismatch error in Go compilation.

### Repro

```gala
package example

import "time"

func getDeadline(t time.Time, ok bool) Option[time.Time] =
    if (ok) Some(t) else None[time.Time]()
```

**Expected**: Transpiles to Go code that returns `std.Option[time.Time]`.

**Actual**: Transpiled Go wraps in `func() any { ... }()`, producing:
```
cannot use func() any {…}() (value of interface type any) as std.Option[time.Time] value in return statement: need type assertion
```

### Workaround

Use `if`/`return` statements instead of if-expression:

```gala
func getDeadline(t time.Time, ok bool) Option[time.Time] {
    if ok { return Some(t) }
    return None[time.Time]()
}
```

---

## BUG-053: `Try` on multi-return Go function can't be destructured in `match`

**Severity**: Low
**Status**: Open
**Date**: 2026-03-28

### Description

When wrapping a Go function that returns `(T1, T2, error)` with `Try(() => f())`, the result is `Try[(T1, T2)]` (a tuple). However, `match { case Success(a, b) => ... }` does not destructure the tuple — it's treated as two arguments to `Success` which causes a type mismatch.

### Repro

```gala
package example

import "os"

func readFile(path string) string =
    Try(() => os.ReadFile(path)) match {
        case Success(data, err) => string(data)  // does not work
        case Failure(_) => ""
    }
```

**Expected**: `case Success(data, err)` destructures the tuple result.

**Actual**: Type mismatch — `Success` extractor expects a single value.

### Workaround

Use Go-style multi-return assignment with `if err != nil`:

```gala
func readFile(path string) string {
    val data, err = os.ReadFile(path)
    if err != nil { return "" }
    return string(data)
}
```

Or wrap with `Try` and access `.Get()` as a single tuple value.

---

# Usability Problems & Missing APIs

## USR-001: No type assertion syntax in GALA

**Severity**: High
**Date**: 2026-03-28

### Description

GALA has no syntax for Go-style type assertions (`x.(*T)` or `x.(T)`). When a value is stored as `any` (e.g., in a context store), there is no way to cast it back to the concrete type from GALA code.

### Impact

Forces developers to write Go helper functions in the bridge layer for every type assertion. In gala-server, this required adding `httpcore.SessionFromContext()` just to extract `*SessionData` from a `ContextValues` map that stores `any`.

### Example of What's Needed

```gala
// Desired syntax (similar to Go)
val session = raw.(httpcore.SessionData)

// Or pattern-match style
raw match {
    case s: *httpcore.SessionData => use(s)
    case _ => handleError()
}
```

### Current Workaround

Write a Go helper in the bridge:

```go
func SessionFromContext(cv *ContextValues) (*SessionData, bool) {
    raw, ok := cv.Get("_session")
    if !ok { return nil, false }
    sd, ok := raw.(*SessionData)
    return sd, ok
}
```

---

## USR-002: No `Option.When` factory constructor

**Severity**: Medium
**Date**: 2026-03-28

### Description

There is no `Option.When(condition, value)` factory method (like Scala's `Option.when`). This is a common pattern for conditionally creating `Some` or `None` based on a predicate.

### Impact

Forces repeated `if (cond) Some(v) else None[T]()` patterns throughout codebases. In gala-server, this pattern appears in `request.gala` (Deadline, CtxGetInt, Cookie) and required custom helpers `optionFromString` and `optionFromBool`.

### What's Needed

```gala
// In std Option companion
func When[T any](condition bool, value T) Option[T] =
    if (condition) Some(value) else None[T]()

// Usage
val name = Option.When(s != "", s)           // Some("alice") or None
val deadline = Option.When(ok, t)            // Some(time.Time) or None
```

---

## USR-003: No `Array.Grouped(n)` or `Array.Sliding(size, step)`

**Severity**: Medium
**Date**: 2026-03-28

### Description

`collection_immutable.Array` has no `Grouped(n)` method (split into sub-arrays of size n) or `Sliding(size, step)` (sliding window). These are standard functional collection operations available in Scala, Kotlin, etc.

### Impact

In gala-server's `Server.URL()`, path parameters are passed as variadic key-value pairs (`"id", "42", "name", "alice"`). Processing them requires an imperative `var i = 0; for i < len(params) - 1 { ... i = i + 2 }` loop because there's no way to group the flat array into pairs.

### What's Needed

```gala
// Grouped: split into chunks of size n
ArrayOf(1, 2, 3, 4, 5, 6).Grouped(2)
// => Array[Array[int]]: [[1,2], [3,4], [5,6]]

// Sliding: overlapping windows
ArrayOf(1, 2, 3, 4, 5).Sliding(3, 1)
// => Array[Array[int]]: [[1,2,3], [2,3,4], [3,4,5]]
```

### Current Workaround

Imperative loop with `var` counter:

```gala
var i = 0
for i < len(params) - 1 {
    path = strings.Replace(path, s"{${params[i]}}", params[i + 1], 1)
    i = i + 2
}
```

---

## USR-004: Go struct literals required for bridge types (no named-arg construction)

**Severity**: Medium
**Date**: 2026-03-28

### Description

When constructing Go struct types imported from bridge packages, GALA requires Go's `Type{Field: value}` colon syntax instead of GALA's `Type(Field = value)` named-argument syntax. This creates an inconsistency — GALA-defined structs use `=` but Go-imported structs use `:`.

### Impact

In gala-server, `httpcore.BridgeCookie{Name: c.Name, Value: c.Value, ...}` and `httpcore.SSEEvent{Event: e.Event, ...}` must use Go syntax. This is confusing when the same file also uses `Cookie(Name = ..., Value = ...)` for GALA-defined structs.

### Example

```gala
// GALA struct — uses = syntax
val c = Cookie(Name = "session", Value = "abc", Path = "/")

// Go struct (from bridge) — forced to use : syntax
br.AddCookie(httpcore.BridgeCookie{
    Name:  c.Name,
    Value: c.Value,
    Path:  c.Path,
})
```

### What's Needed

Allow GALA named-arg syntax for Go structs:

```gala
br.AddCookie(httpcore.BridgeCookie(Name = c.Name, Value = c.Value, Path = c.Path))
```

---

## USR-005: No nil-safe bridge helpers for Go maps/slices

**Severity**: Medium
**Date**: 2026-03-28

### Description

Go functions frequently return `nil` maps or `nil` slices. GALA's `Option` and `Array` types don't interoperate with these — there's no built-in way to safely convert a possibly-nil Go map or slice to a GALA collection.

### Impact

In gala-server's `request.gala`, both `QueryParams` and `FormParams` have double nil-check boilerplate:

```gala
func (r Request) QueryParams(name string) Array[string] {
    val qp = r.bridge.AllQueryParams()
    if qp == nil { return EmptyArray[string]() }
    val values = qp[name]
    if values == nil { return EmptyArray[string]() }
    return ArrayOf(values...)
}
```

### What's Needed

A `go_interop` helper like:

```gala
// Convert nil-able Go slice to Array (nil -> empty)
func ArrayFromSlice[T any](slice []T) Array[T]

// Convert nil-able Go map lookup to Option
func OptionFromMap[K comparable, V any](m map[K]V, key K) Option[V]
```

Usage:

```gala
func (r Request) QueryParams(name string) Array[string] =
    OptionFromMap(r.bridge.AllQueryParams(), name)
        .Map((values) => ArrayOf(values...))
        .GetOrElse(EmptyArray[string]())
```

---

## USR-006: No recursive/tail-recursive helper for retry patterns

**Severity**: Low
**Date**: 2026-03-28

### Description

GALA has no `@tailrec` annotation or built-in retry/loop combinator. The `Retry` and `RetryWithBackoff` filters in gala-server require imperative `var` loops because there's no functional alternative for stateful retry logic with early returns.

### Impact

The `Retry` filter uses 2 `var` declarations and `RetryWithBackoff` uses 3. These are the last remaining `var` usages in the non-test codebase that can't be eliminated without language support.

### What's Needed

Either:
1. A `@tailrec` annotation that verifies tail-call optimization, or
2. A stdlib `retry` combinator:

```gala
import . "martianoff/gala/concurrent"

// Retry combinator in stdlib
func Retry[T any](
    maxAttempts int,
    backoff func(int) time.Duration,
    action func() Try[T],
) Try[T]
```

---

## USR-007: `Try` cannot wrap Go functions that return `(T, error)` with named result

**Severity**: Medium
**Date**: 2026-03-28

### Description

`Try(() => f(args))` works when `f` returns `(T, error)`, but the resulting `Try[T]` discards the first return value's name. When `f` returns `([]byte, string, error)` (3 values), `Try` only captures the error and it's unclear how the other values are handled. There's no `Try2` or `Try3` for multi-return functions.

### Impact

In gala-server, `httpcore.ReadFile(path)` returns `([]byte, string, error)`. Using `Try(() => httpcore.ReadFile(path))` doesn't give access to both the byte data and the content type string — only the error is captured. Forces Go-style `val data, ct, err = f(); if err != nil` pattern.

### What's Needed

```gala
// Try2 for (T1, T2, error) functions
func Try2[A, B any](f func() (A, B, error)) Try[Tuple[A, B]]

// Usage
Try2(() => httpcore.ReadFile(path)) match {
    case Success(result) => Blob(StatusOk(), result.V2, result.V1)
    case Failure(err) => NotFound(s"File not found: $path")
}
```
