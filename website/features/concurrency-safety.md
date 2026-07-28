---
layout: default
title: "Compile-Time Data-Race Safety for Go — Sendable and Shareable"
description: "GALA turns Go's worst production bug class into a compile error. Only deeply-immutable values may cross a goroutine boundary — checked statically at Sendable boundaries with GALA-E0037, no runtime race detector needed."
keywords: "golang data race, compile time race detection, go race detector alternative, sendable, shareable, goroutine safety, immutable concurrency, golang data race prevention, go concurrency safety, gala sendable"
permalink: /features/concurrency-safety/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/">Features</a> / Concurrency Safety</p>

# Compile-Time Data-Race Safety

A data race happens when two goroutines touch the same memory at the same time and at least one of them writes. In Go, the compiler will not stop you. `go test -race` will — but only sometimes, and only after the fact.

GALA moves the check to compile time. A value that crosses a goroutine boundary must be **deeply immutable**, and the transpiler proves it before your program ever runs. If it cannot, the build fails with [GALA-E0037](#gala-e0037--the-diagnostic) pointing at the exact variable.

---

## The problem with catching races at runtime

Go's race detector is a **dynamic, sampling** tool. It instruments memory accesses and reports a race only when two conflicting accesses actually happen, in that order, during that run. That means:

- A race on a code path the test never exercised is not reported.
- A race that needs an unlucky interleaving may not surface for months, then surface in production.
- You have to remember to run under `-race`, and keep the instrumented build in CI.

It is a genuinely good tool, and it stays useful for the Go you interoperate with. But it is a *detector*: it tells you a race happened. GALA's check is a *proof obligation*: it tells you a race cannot happen, on every path, at compile time.

---

## The rule

> **Only deeply-immutable values may cross a goroutine boundary.**

If nobody can ever mutate a value, any number of goroutines can read it concurrently with no synchronization and no race. That is the whole model.

GALA is already immutable-by-default — `val` over `var`, `collection_immutable` over `collection_mutable`, `Option`/`Try`/`Either` over `nil` and error pairs — so most values a well-written GALA program shares are **already** shareable. The check is silent in the common case. It fires only when a program tries to send something genuinely mutable.

Every capture in this program is checked and accepted:

```gala
package main

import (
    . "martianoff/gala/std"
    . "martianoff/gala/concurrent"
    . "martianoff/gala/collection_immutable"
)

func main() {
    // An immutable Int val.
    val base = 41
    val f1 = Future(base + 1)
    Println(f1.Get())

    // A deeply-immutable Array — concurrent reads never race.
    val xs = ArrayOf(10, 20, 30)
    val f2 = Future(xs.Size())
    Println(f2.Get())

    // An immutable HashMap.
    val counts = HashMapOf(("a", 1), ("b", 2), ("c", 3))
    val f3 = Future(counts.Size())
    Println(f3.Get())
}
```

No annotations, no `Sendable` in sight, no ceremony. Idiomatic GALA passes the check by construction.

---

## The `Sendable[F]` boundary

`Sendable[F]` is a **transparent** concurrency-boundary marker. For type checking and code generation it is *exactly* its inner type `F`: a `Sendable[func() T]` parameter is plain `func() T` in the generated Go, with no wrapper and no runtime cost. What it adds is a signal to the analyzer — **this parameter position is a goroutine boundary**.

The standard library marks its boundaries this way:

```gala
// concurrent/future.gala — the Future body is a boundary.
func (f Future[T]) Apply(body Sendable[func() T]) Future[T] = ...
func FutureOn[T any](body Sendable[func() T], ec go_interop.ExecutionContext) Future[T] = ...
```

`Sendable` is a **language-level marker any library can use** — the checker keys off the marker *type*, never off specific function names. Declare a boundary in your own library exactly the same way.

When a closure (an explicit lambda, or a by-name thunk such as `Future(counter + 1)`) is passed to a `Sendable` parameter, the transpiler runs capture analysis and checks every captured free variable.

---

## What is shareable

`Shareable(T)` is a pure, deep, transitive predicate over a GALA type. It looks through wrappers, collections, tuples, struct fields, and sealed variants until it reaches primitives or something it must reject.

| Type | Shareable when | Why |
|------|----------------|-----|
| `int`, `uint`, `float64`, `complex128`, … | always | value primitives — copied, never aliased |
| `bool`, `string`, `byte`, `rune` | always | value primitives (`string` is immutable) |
| `Array[T]`, `List[T]`, `HashMap[K,V]`, `HashSet[K]`, `TreeMap`, `TreeSet` from **`collection_immutable`** | all type arguments shareable | persistent immutable structures |
| `Option[T]`, `Try[T]`, `Either[A,B]` | all type arguments shareable | immutable wrappers |
| `Tuple2`…`Tuple10` | all type arguments shareable | immutable product types |
| `Immutable[T]`, `ConstPtr[T]` | `T` is shareable | the wrapper freezes reassignment, not the pointee |
| A **struct** | every field is a `val` field of a shareable type | deeply immutable |
| A **sealed** type | every field of every variant is shareable | variant fields are immutable constructor parameters |

## What is not

| Type | Why |
|------|-----|
| `Array`/`List`/`HashMap`/… from **`collection_mutable`** | backed by shared, mutable state |
| A struct with **any `var` field** | the field can be reassigned — a write race |
| A struct or variant with any field of a non-shareable type | mutability leaks in transitively |
| Opaque **Go-interop** types | the transpiler cannot prove immutability, so it must assume none |
| Bare Go **pointer** `*T`, **slice** `[]T`, **map** `map[K]V`, **channel** | shared mutable aliasing |
| **Function types** (bare `func`) | safety depends on what the closure captured, not on its type — see [passing functions across](#passing-functions-across-a-boundary) |
| **Unbounded generic type parameters** | without a `Shareable` guarantee, `T` could be anything |
| `any`, `error` | interfaces that may hold or close over mutable state |

**Fail closed.** Every "I don't know" resolves to *not shareable*: an unresolved name, an opaque Go type, a bare type parameter. The predicate never lets an unproven value cross.

`Immutable[T]` and `ConstPtr[T]` are deliberately **conditional**, not unconditional. `Immutable[T]` freezes reassignment of the wrapped value, but `Get()` returns the stored `T` by copy — and if `T` is a reference-backed mutable type, the copy still aliases the same backing store. So `Immutable[int]` and `Immutable[Array[int]]` are shareable; `Immutable[collection_mutable.Array[int]]` is not.

---

## GALA-E0037 — the diagnostic

Three of the four shapes of unsafe capture, and what the compiler actually prints for each — a [framed diagnostic]({{ '/features/compiler-dx/#framed-compile-diagnostics' | relative_url }}) with the caret on the offending capture. (The fourth, capturing a bare `func` value, is covered under [passing functions across a boundary](#passing-functions-across-a-boundary).)

**A reassignable `var`** — the closure runs on another goroutine while the enclosing scope can still reassign the slot:

```gala
package main

import (
    . "martianoff/gala/std"
    . "martianoff/gala/concurrent"
)

func main() {
    var counter = 0
    val f = Future(() => counter + 1)
    Println(f.Get())
}
```

```
error[GALA-E0037]: closure crossing a concurrency boundary captures reassignable var "counter" — a data race, because the enclosing scope may reassign it while the goroutine runs
  --> var_capture.gala:10:26
   |
10 |     val f = Future(() => counter + 1)
   |                          ^^^^^^^ snapshot it into an immutable `val` before the boundary
   |
   = hint: snapshot it into an immutable `val` before the boundary (e.g. `val counter = counter` outside the closure) so the goroutine captures a stable copy
```

**A `val` of a mutable type, used whole** — the goroutine and the enclosing scope alias the same mutable contents. A **method call is a whole use**, since a method may read or write mutable internals:

```gala
package main

import (
    . "martianoff/gala/std"
    . "martianoff/gala/concurrent"
    . "martianoff/gala/collection_mutable"
)

func main() {
    val buffer = ArrayOf(1, 2, 3)
    val f = Future(() => buffer.Size())
    Println(f.Get())
}
```

```
error[GALA-E0037]: closure crossing a concurrency boundary captures "buffer" (type *collection_mutable.Array[int]), whose type is not safe to share — a data race on its mutable contents
  --> mutable_capture.gala:11:26
   |
11 |     val f = Future(() => buffer.Size())
   |                          ^^^^^^ use an immutable collection
   |
   = hint: use an immutable collection (collection_immutable) or snapshot the needed data into an immutable `val`; or restructure so the closure returns the value instead of capturing it
```

**An unshareable field path** — the message names the specific field and why:

```gala
struct AppModel(team string, statuses Array[int], var attempts int)

val model = AppModel("qa", ArrayOf(1, 2, 3), 0)
val f = Future(() => model.attempts + 1)
```

```
error[GALA-E0037]: closure crossing a concurrency boundary reads field path "model.attempts" on captured "model", which is not safe to share: it reads through a mutable (`var`) field — a data race, because the enclosing scope may reassign that field while the goroutine reads it
  --> field_path.gala:13:26
   |
13 |     val f = Future(() => model.attempts + 1)
   |                          ^^^^^ read only immutable
   |
   = hint: read only immutable (`val`) fields of "model" across the boundary, or snapshot the needed field into an immutable `val` before it
```

The caret points at the exact offending capture identifier, and the hint names a concrete fix: read only immutable fields, snapshot into a `val`, switch to an immutable collection, or restructure so the closure returns the value instead of capturing it.

---

## Field-access sensitivity — no snapshot dance

A conservative whole-value check would force you to copy fields into local `val`s before every `Future`. GALA's check is **field-access-sensitive** instead: it records *how* a closure uses each capture.

A run of `.field` reads is recorded as a path, and a **trailing method call on that path** is treated as a read of its receiver — it defers to the leaf field's shareability rather than forcing a whole use. Anything else — a bare reference, an argument, an index, a chained (non-terminal) call, a `match` subject — marks the capture as used *whole*. A capture read only through field paths is accepted when every accessed path reads through `val` fields to a shareable type.

```gala
struct AppModel(team string, statuses Array[int], var attempts int)
func project(team string, statuses Array[int]) string = s"$team: ${statuses.Size()}"

val model = AppModel("qa", ArrayOf(1, 2, 3), 0)

// OK — reads only the immutable `team`/`statuses` fields, even though
// AppModel as a whole is unshareable (it has a `var attempts` field).
Future(() => project(model.team, model.statuses))

// GALA-E0037 — this path reaches the `var` field.
Future(() => model.attempts + 1)
```

Reading a frozen projection of an otherwise-mutable value is race-free, so no snapshot `val` is needed.

Calling a method on a field you just read works too — the receiver's own type decides:

```gala
// OK — `team` is a `string`, which is unconditionally shareable.
Future(() => model.team.Size())

// GALA-E0037 — the receiver field's type is mutable.
Future(() => model.buf.Size())    // buf: collection_mutable.Array[int]
```

Where a use isn't provably a field read — indexing, a chained call, a `match` subject — the capture counts as whole. That bias is deliberate and runs in the safe direction: it can reject a race-free access, never accept a racy one.

---

## Passing functions across a boundary

A closure's shareability cannot be decided from its *type*. A bare `func() T` says nothing about what it captured internally — one such value captures nothing, another captures a mutable array. So a captured or forwarded **function value** follows Rust's `Send`-style rule:

- a **bare `func(...)`** capture is rejected — nothing vouches for its captures;
- a **`Sendable`-typed** capture is accepted — the vouch has already been made;
- the requirement **rides up wrapper signatures** and **terminates at the closure literal**, where the captures are actually checked.

```gala
package main

import (
    . "martianoff/gala/std"
    . "martianoff/gala/concurrent"
)

// The forwarded parameter is `Sendable[func() int]`, so the capture-safety
// obligation propagates to whoever writes the closure.
func afterCompute(compute Sendable[func() int]) Future[int] =
    Future(() => compute() + 1)

// Thunk sugar desugars identically: `Future(compute() + 1)` is
// `Future(() => compute() + 1)`, capturing the same Sendable `compute`.
func afterComputeThunk(compute Sendable[func() int]) Future[int] =
    Future(compute() + 1)

func main() {
    // The closure literal supplied HERE has its own captures checked —
    // it captures the immutable `val base`, which is shareable.
    val base = 41
    Println(afterCompute(() => base).Get())
    Println(afterComputeThunk(() => base).Get())
}
```

Declaring the parameter as a bare `func() int` instead is a compile error at the forward, with a hint naming `Sendable[func() int]` as the fix.

**Application code passing closure literals never writes `Sendable`.** Only reusable concurrency-wrapper APIs annotate their forwarded parameters.

---

## What the check does not cover

Honest scope. This is a capture check at marked boundaries, not a whole-program ownership system.

- **`go_interop.Spawn` and raw goroutine plumbing are unchecked.** Their parameters are not `Sendable`, so closures spawned through them are not validated — you take responsibility for synchronization (mutexes, channels, atomics). Prefer `Future` for safe concurrency and reach for `go_interop` only when you deliberately manage synchronization yourself.
- **Only `Sendable` parameter positions are boundaries.** State shared with a goroutine by some other route — through a Go channel, a Go library that starts its own goroutines, a package-level `var` — is outside the model.
- **Unbounded type parameters are rejected conservatively.** A bare `T` with no `Shareable` guarantee cannot be proven immutable, so a generic wrapper over an unconstrained `T` will need its parameter marked `Sendable` or its type constrained.
- **Go interop is opaque by construction.** A type from an imported Go package is assumed mutable, because the transpiler cannot prove otherwise. That is the safe direction, but it means Go-typed values generally need a snapshot into GALA types before crossing.
- **The Go race detector still has a job.** It remains the right tool for the Go code you interoperate with and for anything crossing through the unchecked escape hatch.

---

## Further Reading

- [Concurrency — Futures, cancellation, and structured concurrency]({{ '/features/concurrency/' | relative_url }}) — the `Future[T]` API these boundaries protect
- [Immutability]({{ '/features/immutability/' | relative_url }}) — `val`, immutable fields, and `ConstPtr[T]`, the substrate the shareability model reads
- [Compiler DX]({{ '/features/compiler-dx/' | relative_url }}) — how GALA renders diagnostics like E0037
- [Collections]({{ '/features/collections/' | relative_url }}) — `collection_immutable` vs `collection_mutable`
