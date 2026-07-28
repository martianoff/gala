---
layout: default
title: "GALA-E0037 — Non-Shareable Value Crosses a Goroutine Boundary"
description: "\"closure crossing a concurrency boundary captures reassignable var\" — GALA-E0037 is GALA's compile-time data-race check. Every shape that triggers it, the real compiler output, and the fix for each."
keywords: "gala-e0037, closure crossing a concurrency boundary, gala data race compile time, gala sendable, gala shareable, golang data race prevention, gala future capture, gala concurrency safety"
permalink: /docs/errors/gala-e0037/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0037</p>

# GALA-E0037 — Unshareable capture across a concurrency boundary

**What it means.** A closure — an explicit lambda, or a by-name thunk such as `Future(counter + 1)` — is passed to a **concurrency boundary** (a parameter typed `Sendable[F]`, as used by `Future`, `FutureOn`, and any library that marks its own boundaries) and it captures a binding that is not safe to share across a goroutine.

This is a data race caught at compile time, before the race detector ever has to see it.

A boundary is simply a parameter whose type is `Sendable[F]`. `Future` and `FutureOn` declare theirs that way, and you can declare your own: `func run(body Sendable[func() int]) int = body()` marks `run` as a boundary too.

---

## The shapes that trigger it

### 1. Capturing a reassignable `var`

The closure runs on another goroutine while the enclosing scope can still reassign the variable slot — the two race on the variable itself.

```gala
package main

import . "martianoff/gala/concurrent"

func main() {
    var counter = 0
    val f = Future(() => counter + 1)   // GALA-E0037
    Println(f.Get())
}
```

```
error[GALA-E0037]: closure crossing a concurrency boundary captures reassignable var "counter" — a data race, because the enclosing scope may reassign it while the goroutine runs
  --> f37a.gala:7:26
  |
7 |     val f = Future(() => counter + 1)
  |                          ^^^^^^^ snapshot it into an immutable `val` before the boundary
  |
  = hint: snapshot it into an immutable `val` before the boundary (e.g. `val counter = counter` outside the closure) so the goroutine captures a stable copy
```

The by-name form `Future(counter + 1)` desugars to the same closure and reports the same error.

### 2. Using a `val` of a non-shareable type as a whole

The binding is never reassigned, but the closure uses it *as a value* — referenced bare, passed to a function, returned, indexed, matched, or with a **method called on it** — and its type is not deeply immutable (a `collection_mutable` value, a struct with a `var` field, a Go-interop reference type, a bare slice, map, or pointer).

```gala
package main

import . "martianoff/gala/concurrent"
import . "martianoff/gala/collection_mutable"

func main() {
    val buffer = ArrayOf(1, 2, 3)
    val f = Future(() => buffer.Size())   // GALA-E0037 — a method call is a whole use
    Println(f.Get())
}
```

```
error[GALA-E0037]: closure crossing a concurrency boundary captures "buffer" (type *collection_mutable.Array[int]), whose type is not safe to share — a data race on its mutable contents
  --> f37b.gala:8:26
  |
8 |     val f = Future(() => buffer.Size())
  |                          ^^^^^^ use an immutable collection
  |
  = hint: use an immutable collection (collection_immutable) or snapshot the needed data into an immutable `val`; or restructure so the closure returns the value instead of capturing it
```

### 3. Reading an unshareable field path

The closure reads a field path (`x.a`, `x.a.b`) that passes through a mutable (`var`) field, or bottoms out at a type that is not deeply immutable.

```gala
package main

import . "martianoff/gala/concurrent"
import . "martianoff/gala/collection_immutable"

struct AppModel(team string, statuses Array[int], var attempts int)

func main() {
    val model = AppModel("qa", ArrayOf(1, 2, 3), 0)
    val f = Future(() => model.attempts + 1)   // GALA-E0037 — reads the `var` field
    Println(f.Get())
}
```

```
error[GALA-E0037]: closure crossing a concurrency boundary reads field path "model.attempts" on captured "model", which is not safe to share: it reads through a mutable (`var`) field — a data race, because the enclosing scope may reassign that field while the goroutine reads it
  --> f37c.gala:10:26
   |
10 |     val f = Future(() => model.attempts + 1)
   |                          ^^^^^ read only immutable
   |
   = hint: read only immutable (`val`) fields of "model" across the boundary, or snapshot the needed field into an immutable `val` before it
```

A field path that bottoms out at a mutable *type* rather than a `var` field reports the same code with a type-specific message and a hint pointing at a deeply-immutable field instead.

### 4. Capturing a bare `func` value

A function value whose declared type is a bare `func(…) …` says nothing about what *it* captured, so it cannot be shared.

```gala
package main

import . "martianoff/gala/concurrent"

func broken(compute func() int) Future[int] = Future(compute)   // GALA-E0037

func main() {
    Println(broken(() => 7).Get())
}
```

```
error[GALA-E0037]: closure crossing a concurrency boundary captures function value "compute" (type func() int) — a function value may itself close over mutable state, so it cannot cross the boundary unless it is Sendable
  --> f37d.gala:5:54
  |
5 | func broken(compute func() int) Future[int] = Future(compute)
  |                                                      ^^^^^^^ declare the parameter as `compute Sendable[func() int]` — th…
  |
  = hint: declare the parameter as `compute Sendable[func() int]` — the Sendable bound requires each caller to pass a closure whose own captures are shareable, and propagates the check to the call site where the closure is written
```

---

## What is always allowed

- A capture resolving to a **top-level function, type, or package symbol** — not per-goroutine state, so not a race.
- A capture whose declared type is **`Sendable[F]`** — the caller already vouched for its captures.
- **Reading only immutable (`val`) fields** of an otherwise-unshareable value. The check is field-access-sensitive: a frozen projection is race-free, and no snapshot is needed.

Only an unbroken run of `.field` reads counts as a path. The first non-field operation ends the run and marks the capture as used *whole* — including a method call on the field just read. The check is deliberately conservative here and rejects some accesses that are in fact race-free:

```gala
// Accepted — a pure field-read path, passed onward as a value.
Future(project(model.team, model.statuses))

// Rejected — `.Size()` ends the field-path run, so `model` counts as used
// whole, even though `model.team` is a string and could never race.
Future(model.team.Size())
```

The second form reports the whole-value message, naming the struct's type:

```
error[GALA-E0037]: closure crossing a concurrency boundary captures "model" (type AppModel), whose type is not safe to share — a data race on its mutable contents
  --> rej37.gala:10:20
   |
10 |     val f = Future(model.team.Size())
   |                    ^^^^^ use an immutable collection
   |
   = hint: use an immutable collection (collection_immutable) or snapshot the needed data into an immutable `val`; or restructure so the closure returns the value instead of capturing it
```

To keep it compiling, bind the field to a local `val` outside the closure and call the method on that: `val team = model.team`, then `Future(team.Size())`.

---

## How to fix it

**Snapshot into a `val`.** Copy the value before the boundary so the closure captures a stable copy:

```gala
val snapshot = counter
Future(() => snapshot + 1)
```

**Use an immutable collection.** Prefer `collection_immutable` for anything a `Future` reads — concurrent reads of an immutable value never conflict:

```gala
val xs = ArrayOf(1, 2, 3)
val f = Future(xs.Size())
Println(f.Get())
```

**Read only immutable fields**, as shown above — but avoid calling a *method* on the value, since a method may touch mutable internals and counts as a whole use.

**Restructure** so the closure returns the value instead of capturing it, or compute what you need before the boundary.

**Propagate the guarantee with `Sendable`.** When the capture is a function value you are forwarding into a boundary — the concurrency-wrapper pattern — you cannot snapshot it. Push the obligation up to your caller instead:

```gala
// Rejected: `compute` is a bare func — nothing vouches for its captures.
func broken(compute func() int) Future[int] = Future(compute)

// Accepted: the Sendable bound makes the caller vouch, and propagates.
func ok(compute Sendable[func() int]) Future[int] = Future(compute)
```

`Sendable[F]` is transparent — it is exactly `F` in the generated Go — so callers still pass an ordinary lambda. The annotation is purely a compile-time capture-safety contract. Concurrency-wrapper functions should always type their forwarded closure parameters `Sendable[func() T]`.

---

## Why the rule exists

Only deeply-immutable values can be shared across goroutines without a data race. A `var` can be reassigned; a mutable pointee can be written; either invites a race that Go's race detector would catch only at runtime, non-deterministically, and only if the test happened to interleave the right way. Rejecting the unsafe capture at the boundary names the exact value and points at the fix — at compile time, every time.

**Escape hatch.** `go_interop.Spawn` / `SpawnWithRecover` and raw goroutine plumbing are the low-level, unchecked zone: their parameters are not `Sendable`, so captures are not validated and synchronization is your responsibility. Prefer `Future` for safe concurrency.

---

## Related

- [Concurrency Safety](/features/concurrency-safety/) — the full shareability model
- [Concurrency](/features/concurrency/) — `Future`, `Promise`, `ExecutionContext`, `Await`
- [Immutability](/features/immutability/) — `val` bindings and immutable fields
- [Collections](/features/collections/) — immutable vs. mutable collection types
- [All GALA error codes](/docs/errors/)
