# GALA-E0037 — Unshareable capture across a concurrency boundary

**When it fires.** A closure — an explicit lambda or a by-name / thunk argument
such as `Future(counter + 1)` — is passed to a **concurrency boundary** (a
parameter typed `Sendable[F]`, as used by `concurrent.Future`,
`concurrent.FutureOn`, and any library that marks its own boundaries) and it
captures a binding that is **not safe to share** across a goroutine.

Four shapes trigger it:

1. **Reassignable `var` capture — a reassignment race.** The closure runs on
   another goroutine while the enclosing scope can still reassign the variable
   slot, so the two race on the variable itself.

   ```gala
   var counter = 0
   Future(() => counter + 1)   // ← GALA-E0037
   ```

2. **`val` used WHOLE, of a non-shareable type — a mutable-pointee race.** The
   binding is never reassigned, but the closure uses it AS A VALUE — referenced
   bare, passed to a function, returned, indexed, matched, or with a **method
   called on it** (`x.foo()` — a method may read or write mutable internals) —
   and its *type* is not deeply immutable (a `collection_mutable` value, a struct
   with a `var` field, a Go-interop reference type, a bare slice/map/pointer, …).
   The goroutine and the enclosing scope then alias the same mutable contents.

   ```gala
   val buffer = collection_mutable.ArrayOf(1, 2, 3)
   Future(() => buffer.Size())  // ← GALA-E0037 (a method call is a whole use)
   ```

   The check is **field-access-sensitive**: if the closure reads ONLY immutable
   (`val`) fields of the value, resolving to shareable types, it is *accepted* —
   reading a frozen projection of an otherwise-mutable value is race-free, and no
   snapshot `val` is needed. This fires only for a whole use, a method call, or a
   field-read path that reaches a mutable (`var`) field or an unshareable type
   (see shape 3).

   ```gala
   struct AppModel(team string, statuses Array[int], var attempts int)
   val model = AppModel("qa", ArrayOf(1, 2, 3), 0)
   // ACCEPTED: reads only the immutable `team`/`statuses` fields, even though
   // AppModel as a whole is unshareable (it has a `var attempts` field).
   Future(() => project(model.team, model.statuses))
   ```

4. **Unshareable field-read path — a race on the accessed field.** The closure
   reads a field path (`x.a`, `x.a.b`) that either passes through a mutable
   (`var`) field — reading it races with the field's reassignment — or bottoms
   out at a type that is itself not deeply immutable.

   ```gala
   struct AppModel(team string, statuses Array[int], var attempts int)
   val model = AppModel("qa", ArrayOf(1, 2, 3), 0)
   Future(() => model.attempts + 1)   // ← GALA-E0037: reads the `var` field
   ```

4. **Bare `func` capture — an unvouched closure.** A captured or forwarded
   **function value** whose declared type is a bare `func(...) ...` says nothing
   about what it captured internally, so it cannot be shared. Type the parameter
   `Sendable[...]` instead (see *Fix* below).

   ```gala
   func broken(compute func() int) Future[int] =
       concurrent.FutureApply(compute)   // ← GALA-E0037: make `compute` Sendable
   ```

A capture that resolves to a **top-level function, type, or package symbol** is
*not* a capture race and is ignored — those are not per-goroutine state. A
capture whose declared type is **`Sendable[F]`** is likewise allowed: the
`Sendable` bound means the caller already vouched for the function's own
captures (see *Fix* → *Propagate the guarantee with `Sendable`*).

**Error output.**

```
[SemanticError GALA-E0037] file.gala:7:22 closure crossing a concurrency boundary captures reassignable var "counter" — a data race, because the enclosing scope may reassign it while the goroutine runs (hint: snapshot it into an immutable `val` before the boundary (e.g. `val counter = counter` outside the closure) so the goroutine captures a stable copy)
```

```
[SemanticError GALA-E0037] file.gala:9:22 closure crossing a concurrency boundary captures "buffer" (type *collection_mutable.Array[int]), whose type is not safe to share — a data race on its mutable contents (hint: use an immutable collection (collection_immutable) or snapshot the needed data into an immutable `val`; or restructure so the closure returns the value instead of capturing it)
```

For an unshareable field-read path (shape 3), the message names the specific
offending field path and why:

```
[SemanticError GALA-E0037] file.gala:8:22 closure crossing a concurrency boundary reads field path "model.attempts" on captured "model", which is not safe to share: it reads through a mutable (`var`) field — a data race, because the enclosing scope may reassign that field while the goroutine reads it (hint: read only immutable (`val`) fields of "model" across the boundary, or snapshot the needed field into an immutable `val` before it)
```

```
[SemanticError GALA-E0037] file.gala:8:22 closure crossing a concurrency boundary reads field path "model.sessions" on captured "model", which is not safe to share: its type (collection_mutable.Array[int]) is not safe to share — a data race on its mutable contents (hint: read a deeply-immutable field of "model" instead, or snapshot the needed data into an immutable `val` before the boundary)
```

```
[SemanticError GALA-E0037] file.gala:4:37 closure crossing a concurrency boundary captures function value "compute" (type func() int), which is not safe to share — a bare function type carries no guarantee that its own captures are shareable (hint: declare the forwarded parameter's type as `Sendable[func() int]` so the caller vouches that the function's captures are shareable; that `Sendable` bound then propagates to the caller (the standard Send-style rule))
```

The caret points at the exact offending capture identifier.

**Fix.** Make the capture immutable and shareable:

- **Snapshot to a `val`.** Copy the value into an immutable `val` *before* the
  boundary; the closure then captures a stable copy.

  ```gala
  val snapshot = counter
  Future(() => snapshot + 1)
  ```

- **Use an immutable collection.** Prefer `collection_immutable` over
  `collection_mutable` for anything a `Future` reads.

  ```gala
  val xs = collection_immutable.ArrayOf(1, 2, 3)
  Future(() => xs.Size())     // OK
  ```

- **Read only immutable fields.** If you captured a value only to read a frozen
  projection of it, read its immutable (`val`) fields directly across the
  boundary — the check is field-access-sensitive and accepts that without a
  snapshot, even when the value as a whole is unshareable. Avoid calling a
  *method* on it (a method is treated as a whole use, since it may touch mutable
  internals).

  ```gala
  struct AppModel(team string, statuses Array[int], var attempts int)
  func project(team string, statuses Array[int]) string = s"$team: ${statuses.Size()}"
  val model = AppModel("qa", ArrayOf(1, 2, 3), 0)
  // OK: reads only the immutable `team`/`statuses` fields (no method call on model).
  Future(() => project(model.team, model.statuses))
  ```

- **Restructure** so the closure returns the value instead of capturing it, or
  compute the needed data before the boundary.

- **Propagate the guarantee with `Sendable`.** When the capture is itself a
  **function value** you are forwarding into a boundary (the concurrency-wrapper
  pattern), you cannot snapshot it — you must instead push the safety obligation
  up to your caller. Declare the forwarded parameter `Sendable[func() T]` rather
  than a bare `func() T`. The `Sendable` bound is the standard `Send`-style rule:
  the caller vouches that the closure it supplies captures only shareable state
  (checked at *their* call site), and that vouch propagates through your wrapper.

  ```gala
  // Reject: `compute` is a bare func — nothing vouches for its captures.
  func broken(compute func() int) Future[int] =
      concurrent.FutureApply(compute)          // GALA-E0037

  // OK: `Sendable[func() int]` makes the caller vouch; the bound propagates.
  func ok(compute Sendable[func() int]) Future[int] =
      concurrent.FutureApply(compute)
  ```

  `Sendable[F]` is transparent — it is exactly `F` in generated Go — so callers
  still pass an ordinary lambda; the annotation is purely the compile-time
  capture-safety contract. Concurrency-wrapper functions should always type their
  forwarded closure parameters `Sendable[func() T]`.

**Rationale.** Only deeply-immutable values can be shared across goroutines
without a data race: concurrent reads of an immutable value never conflict. A
`var` can be reassigned; a mutable pointee can be written; either invites a race
the Go race detector would catch only at runtime, non-deterministically.
Rejecting the unsafe capture at the boundary names the exact value and points at
the fix. See [CONCURRENCY_SAFETY.MD](../CONCURRENCY_SAFETY.MD) for the full
shareability model.

**Escape hatch.** `go_interop.Spawn` / `SpawnWithRecover` and raw goroutine
plumbing are the low-level, *unchecked* zone: their parameters are not
`Sendable`, so captures are not validated and you take responsibility for
synchronization. Prefer `Future` for safe concurrency.
