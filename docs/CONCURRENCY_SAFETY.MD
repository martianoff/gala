# Concurrency Safety: the Shareability Model

GALA is adding **compile-time data-race safety** for values that cross a
goroutine boundary — a value captured by, or passed into, a `Future`, a
`Spawn`, or any other construct that runs work on another goroutine.

This document describes the **shareability model** and the `Shareable` type
predicate that decides it.

> **Status.** Enforcement is live. The `Shareable` predicate and capture
> analysis are wired into a hard compile-time check at `Sendable[F]` concurrency
> boundaries ([GALA-E0037](errors/GALA-E0037.md)). See
> [Enforcement — the `Sendable` boundary and GALA-E0037](#enforcement--the-sendable-boundary-and-gala-e0037).

---

## Motivation

A data race happens when two goroutines touch the same memory at the same time
and at least one of them writes. The cleanest way to make a value provably
race-free is to guarantee it is **deeply immutable**: if nobody can ever mutate
it, then any number of goroutines can read it concurrently with no
synchronisation and no race.

So the rule for crossing a goroutine boundary is simple:

> **Only deeply-immutable values may cross.**

GALA is already immutable-by-default (`val` over `var`, immutable collections
over mutable ones, `Option`/`Try`/`Either` over `nil`/error pairs), so *most*
values a well-written GALA program shares are **already** shareable. The check
is therefore silent in the common case — it only fires when a program tries to
send something genuinely mutable (a `collection_mutable.Array`, a struct with a
`var` field, a raw Go slice/map/pointer, or an opaque Go-interop handle) across
the boundary.

---

## The `Shareable` predicate

`Shareable(T)` is a **pure, deep, transitive** predicate over a GALA type. It is
side-effect free and terminates on recursive types (via a visited set). It
answers a single question: *may a value of type `T` be shared across goroutines
without risking a data race?*

It reasons **structurally** — it looks through wrappers, collections, tuples,
struct fields, and sealed variants until it reaches primitives or something it
must reject.

### What IS shareable

| Type | Shareable when | Why |
|------|----------------|-----|
| `int`, `int8…int64`, `uint`, `uint8…uint64` | always | value primitives — copied, never aliased |
| `float32/64`, `complex64/128` | always | value primitives |
| `bool`, `string`, `byte`, `rune` | always | value primitives (`string` is immutable) |
| `Unit` / void | always | there is no value to race on |
| `Array[T]`, `List[T]`, `HashMap[K,V]`, `HashSet[K]`, `TreeMap`/`TreeSet` from **`collection_immutable`** | all type arguments shareable | persistent immutable structures — never mutated, so concurrent reads are safe |
| `Option[T]`, `Try[T]`, `Either[A,B]` | all type arguments shareable | immutable wrappers |
| `Tuple2…Tuple10[…]` | all type arguments shareable | immutable product types |
| `Immutable[T]` | `T` is shareable | see [Immutable / ConstPtr](#immutable--constptr-are-conditional) |
| `ConstPtr[T]` | `T` is shareable | see [Immutable / ConstPtr](#immutable--constptr-are-conditional) |
| A **struct** | every field is an immutable (`val`) field whose type is shareable | a `val` field cannot be reassigned; if its type is also shareable, the whole struct is deeply immutable |
| A **sealed** type | every field of every variant is shareable | sealed-variant fields are immutable constructor parameters; only their types need checking |

### What is NOT shareable

| Type | Why |
|------|-----|
| `Array`/`List`/`HashMap`/`HashSet`/… from **`collection_mutable`** | backed by shared, mutable state |
| A struct with **any `var` field** | the field can be reassigned/mutated — a write race |
| A struct/sealed variant with **any field of a non-shareable type** | mutability leaks in transitively |
| Opaque **Go-interop** types (a type from an imported Go package) | the transpiler cannot prove it is immutable, so it must assume it is not |
| Bare Go **pointer** (`*T`), **slice** (`[]T`), **map** (`map[K]V`), **channel** | shared mutable aliasing / mutable containers |
| **Function types** (closures) | a closure's safety depends on *what it captures*, which is not decidable from its type alone — see [Function types](#function-types-are-deferred-to-capture-analysis) |
| **Unbounded generic type parameters** (`T` with no bound) | without a `Shareable` bound we cannot prove `T` is immutable, so we reject conservatively |
| `any`, `error` | interfaces that may hold or close over mutable state |
| `NilType` / unresolved / `nil` | no type information — reject conservatively, never crash |

### Design principle: fail closed

Every "I don't know" answer resolves to **not shareable**. An unresolved name,
an opaque Go type, a bare type parameter, a `nil` input — all return `false`.
The predicate never lets an unproven value cross the boundary, and it never
panics on a degenerate input.

---

## Notable modelling decisions

### Immutable collections vs mutable collections are told apart by *package*

`collection_immutable` and `collection_mutable` expose types with **identical
names** (`Array`, `List`, `HashMap`, `HashSet`, `TreeMap`, `TreeSet`). The type
name alone is therefore ambiguous — the predicate keys the decision on the
**package qualifier** (`collection_immutable` ⇒ shareable-if-args-shareable,
`collection_mutable` ⇒ never). A collection whose package cannot be resolved is
treated conservatively as not shareable.

### `val` vs `var` fields

A struct field carries an immutability flag (`val` ⇒ immutable, `var` ⇒
mutable). Any `var` field makes the whole struct non-shareable, because the
field can be reassigned or mutated while another goroutine reads it. Only a
struct all of whose fields are `val` **and** whose field types are themselves
shareable is deeply immutable.

### `Immutable` / `ConstPtr` are conditional

The `Immutable[T]` and `ConstPtr[T]` wrappers are shareable **iff `T` is
shareable** — *not* unconditionally.

```gala
type Immutable[T] struct { var value T }
func (i Immutable[T]) Get() T = i.value
```

`Immutable[T]` freezes *reassignment* of the wrapped value, but `Get()` returns
the stored `T` **by copy**. If `T` is a reference-backed mutable type — say a
`collection_mutable.Array[int]`, whose value is just a header aliasing a Go
slice — the copy still aliases the same backing store, so two goroutines could
mutate/read it concurrently. `ConstPtr[T]` holds a `*T` and only forbids writes
*through the `ConstPtr`*; it cannot stop another holder of the underlying value
from mutating it. Hence both are shareable exactly when their payload is.

For the overwhelmingly common uses — `Immutable[int]`, `ConstPtr[string]`,
`Immutable[Array[int]]` — this still evaluates to shareable, matching the intent
of the wrappers. It only rejects the genuinely unsound cases such as
`Immutable[MutableArray[int]]`.

### Function types are deferred to capture analysis

A function/closure type always returns `false` from this predicate. Whether a
closure is safe to send depends on **what it captures**, not on its type
signature — a `() => int` that captures a `MutableArray` is unsafe, while an
identical-typed one that captures nothing is safe. Deciding that is the job of
the **[capture-analysis](#capture-analysis)** pass, which computes the free
variables a closure captures; enforcement then verifies each captured variable
is itself shareable. This type-only predicate cannot and does not make that call.

### Unbounded type parameters

A bare type parameter `T` with no bound returns `false`. A future PR introduces
a `Shareable` bound; a `T: Shareable` will then be provably shareable, but until
the bound is present the predicate cannot guarantee it.

---

## Capture analysis

The `Shareable` predicate answers *"is this **type** safe to share?"*. A closure
has no useful type-only answer — `() => int` is safe if it captures nothing but a
data race waiting to happen if it captures a `collection_mutable.Array`. What
matters is **what a closure captures**. Deciding that is the job of the
**capture-analysis** pass (`internal/transpiler/concurrency/capture.go`).

### What a capture is

A **capture** (free variable) is a **value-position identifier** a closure's body
references that is **not bound within the closure's own scope** — i.e. it must
come from the enclosing scope. Capture analysis returns the set of such
identifiers, each with the source position of its first reference:

```go
type Capture struct {
    Name  string
    Pos   transpiler.SourcePos
    Paths []string // pure field-read access chains: `x.a` → "a", `x.a.b` → "a.b"
    Whole bool     // used as a value (bare ref, arg, method receiver, index, match, …)
}
```

Alongside each captured name, the walk records **how** the closure uses it, so
enforcement can be **field-access-sensitive** (see
[Field-access sensitivity](#field-access-sensitivity)): `Paths` collects the pure
`.field` selector chains applied to the variable, and `Whole` is set the moment
it is used as a value in any other way.

It is a **pure, syntactic** walk over the ANTLR parse tree (like the
transformer), with an explicit lexical scope stack — no analyzer, no type
system, no AST mutation. Two entry points share one walker:

- `FreeVariablesInLambda(lambda)` — an explicit lambda; its declared parameters
  are bound, its body may be an expression or a block.
- `FreeVariablesInExpression(paramNames, expr)` — a bare expression, so the same
  analysis serves the by-name / thunk sugar (see below).

### Scoping rules

A name is **bound** (and therefore *not* a capture) when it is introduced by any
of these constructs inside the closure:

| Construct | Example | What binds |
|-----------|---------|------------|
| The closure's own parameters | `(x, y) => …` | `x`, `y` |
| `val` / `var` locals | `val a = …` | `a` |
| `bind` / `also` do-notation | `bind a = …` / `also b = …` | `a`, `b` |
| `use` resource binding | `use fh = …` | `fh` |
| `for` loop variables | `for i := range xs { … }` | `i` |
| `match` / partial-function patterns | `case Some(x) =>`, `case Cons(h, t) =>` | `x`, `h`, `t` |
| Nested-lambda parameters | `(x) => (y) => …` | `y` (only inside the inner lambda) |
| Nested `func` declarations | `func g(z) = …` | `g`, and `z` inside its body |

Lexical scoping is honoured precisely:

- **Locals and bindings are visible only after they are declared.** The RHS of a
  binding is analysed *before* the name is bound, so `val x = x + 1` captures the
  outer `x` while later references resolve to the new local. This is proper
  **shadowing**: an outer name referenced *before* an inner `val x` is a capture;
  the same name referenced *after* resolves to the local.
- **Nested lambdas shadow only within their own scope.** A name bound by an outer
  lambda's parameter is free in an inner lambda but is *not* a capture of the
  outer closure; an inner lambda's parameter is never a capture.
- A capture is **de-duplicated by name**, keeping the **first** position (the
  caret a diagnostic points at).

### What is *not* a capture

- **Type names** in annotations (`(x int) => …`, `val a T = …`, `case x: T =>`) —
  the analysis never descends into a `type`.
- The **method / field name** in a selector: in `recv.method(arg)`, `recv` and
  `arg` are captures but `method` is not.
- **Pattern constructor names**: in `case Some(x) =>`, `Some` is not captured;
  only the binding `x` is introduced.

### Field-access sensitivity

For each captured variable the walk also records **how** it is used, so
enforcement need not demand the *whole* value be shareable when the closure only
reads an immutable slice of it:

- A **pure field-read chain** — `x.a`, `x.a.b`, a run of `.field` selectors *not*
  followed by a call, index, or `match` — is recorded as a **path** (`"a"`,
  `"a.b"`) on the capture.
- Any other use marks the capture **`Whole`**: a bare reference (`x`), passing it
  as an argument (`foo(x)`), a **method call** (`x.foo()` — a method may read or
  write mutable internals, so it is *not* a safe field read), an index (`x[i]`), a
  `match` subject, or anything the syntactic walk cannot classify. A variable used
  both ways is `Whole` (the conservative direction).

This is what lets enforcement accept `Future(() => g(m.team, m.statuses))` when
`m`'s type is unshareable as a whole but `team`/`statuses` are immutable fields —
no `val`-snapshot workaround needed. The **conservative bias** is preserved:
anything not provably a pure field read counts as a whole use, the safe
direction.

### String interpolation is analysed too

An interpolated (`s"…"`) or format (`f"…"`) string is a **single lexer token**,
so its embedded `${ … }` and `$name` expressions are *not* child nodes of the
parse tree. A naive walk would never see them and would miss a variable
referenced only inside an interpolation — an unsound false negative. The walk
therefore re-parses each embedded expression (reusing the same interpolation
splitter the transformer uses to transpile `s"…"`/`f"…"`, in the shared
`internal/interpolation` package) and runs the normal expression walk over it, in
the current scope. So `s"count is $counter"` captures `counter`, `f"${x + y}"`
captures `x` and `y`, a nested `${ items.Map((i) => i + base) }` binds `i` and
captures `items` + `base`, and an interpolation that references a **local**
(`val v = 1; s"$v"`) captures nothing.

### The thunk sugar is analysed as `() => expr`

`Future(counter + 1)` desugars its by-name argument to a thunk. The analysis
treats the two spellings identically: analysing the bare expression
`counter + 1` (via `FreeVariablesInExpression`) yields the **same** captures as
analysing the explicit thunk `() => counter + 1` (via `FreeVariablesInLambda`).
So whichever way a program spells a deferred computation, its capture set — and
therefore its shareability verdict — is the same.

### Complete by construction, deliberately over-approximate

The walk is **complete**: precise handlers cover every binding-introducing
construct (lambda, block, `val`/`var`, `for`, `match`/case, `bind`/`also`, `use`,
nested `func`) and every identifier reference, and **any parse-tree node without
a precise handler falls through to a generic child recursion** that descends into
all of its children. No subtree — and therefore no reference — is ever silently
skipped, and the analysis stays correct as the grammar grows (a new expression
form is covered the day it is added, before it has a dedicated handler).

Where a purely syntactic walk cannot be certain, it prefers a spurious free
identifier over a missed capture:

- the **callee of a bare call** `foo(…)` is reported as a free variable;
- **bracketed arguments** `x[…]` (index vs. explicit type argument are
  syntactically indistinguishable) are walked as value references;
- **composite-literal keys** are treated as value positions;
- a node reached only by **generic recursion** contributes every bare identifier
  it contains.

None of these is unsound: a name that actually resolves to a top-level function
or a type simply falls out as non-local when PR3 classifies each capture, exactly
as a top-level function reference does. Over-approximation is the **safe**
direction for a race-safety check — it can flag an extra name, never miss a real
capture.

### How enforcement uses this

Enforcement resolves each reported capture to its `val`/`var` **kind** and
**type**. A `var` capture is rejected outright (a reassignable binding shared
across goroutines is a write race by construction, even if the closure only reads
its immutable fields — the binding slot itself can be rebound). For a `val`,
enforcement is **field-access-sensitive**:

- a capture used **`Whole`** (as a value, or with a method called on it) must have
  its **entire type** be `Shareable`;
- a capture used **only via field-read paths** is accepted iff **every accessed
  path** reads through immutable (`val`) fields to a `Shareable` type — the path's
  type is resolved by walking the binding's type through each field with the same
  metadata the `Shareable` predicate uses. A path through a `var` field, or one
  that bottoms out at an unshareable type, is rejected and named.

A capture that resolves to a top-level function or a type is non-local and
ignored.

## Enforcement — the `Sendable` boundary and GALA-E0037

The `Shareable` predicate and capture analysis are wired into a hard
compile-time check.

### The `Sendable[F]` marker

`Sendable[F]` is a **transparent** concurrency-boundary marker. For
type-checking and code generation it is *exactly* its inner type `F` — a
`Sendable[func() T]` parameter is just `func() T` in the generated Go, with no
runtime wrapper and no cost. What it adds is a signal to the analyzer: **this
parameter position is a goroutine boundary.** A closure (an explicit lambda, or a
by-name / thunk argument such as `Future(counter + 1)`) passed to a `Sendable`
parameter is subject to the capture-safety check.

`Sendable` is a **language-level marker any library can use** — the checker keys
off the marker *type*, never off specific function names. Declare a boundary in
your own library the same way std does:

```gala
// A library-defined boundary. `body` is transparently `func() T`; the closure
// passed to it is checked for capture-safety.
func runAsync[T any](body Sendable[func() T]) Future[T] = ...
```

The standard library marks its boundaries this way:

- `concurrent.Future` / `concurrent.FutureOn` — `body: Sendable[func() T]`.

### The check

When a lambda or thunk is passed to a `Sendable` parameter, the transpiler runs
capture analysis and classifies every captured free variable:

| Capture resolves to…                              | Result |
|---------------------------------------------------|--------|
| a top-level function, type, or package symbol     | ignored — not per-goroutine state |
| a reassignable `var`                              | **GALA-E0037** — reassignment race (even through an immutable field path) |
| a binding declared `Sendable[F]`                  | allowed — a caller-vouched function value (see below) |
| a `val` used **whole** of a non-`Shareable` type (incl. a bare `func`, or a method called on it) | **GALA-E0037** — mutable-pointee / unvouched-closure race |
| a `val` read **only via field paths** that reach a `var` field or an unshareable type | **GALA-E0037** — race on the accessed field |
| a `val` used whole of a `Shareable` type          | allowed |
| a `val` read **only via field paths** that all reach immutable fields of `Shareable` type | allowed — a frozen projection, no snapshot needed |

```gala
var counter = 0
Future(() => counter + 1)   // GALA-E0037: captures reassignable var `counter`

val buffer = collection_mutable.ArrayOf(1, 2, 3)
Future(() => buffer.Size()) // GALA-E0037: a method call is a whole use; `buffer`'s type is not shareable

val xs = collection_immutable.ArrayOf(1, 2, 3)
Future(() => xs.Size())     // OK — a deeply-immutable value is safe to share

// Field-access-sensitive: AppModel is unshareable as a whole (it has a `var`
// field), but the closure reads only its immutable `team`/`statuses` fields.
struct AppModel(team string, statuses Array[int], var attempts int)
val model = AppModel("qa", ArrayOf(1, 2, 3), 0)
Future(() => project(model.team, model.statuses))   // OK — no snapshot needed
Future(() => model.attempts + 1)                     // GALA-E0037: reads the `var` field
```

The diagnostic ([GALA-E0037](errors/GALA-E0037.md)) points its caret at the exact
offending capture and explains *why* it is unsafe (a `var` reassignment race, a
mutable-pointee race, or a specific unshareable field path — named as
`model.attempts`), with a hint suggesting concrete fixes: read only immutable
fields, snapshot the value into an immutable `val`, switch to an immutable
collection, or restructure so the closure returns the value instead of capturing
it.

### Passing functions across a boundary — the `Sendable` bound

A closure's shareability cannot be decided from its *type* alone: a bare
`func() T` says nothing about what the function captured internally. So a captured
or forwarded **function value** is treated exactly like Rust's `Send` bound.

`Sendable[func() T]` on a parameter is a **promise the caller vouches for**: the
closure it passes captures only shareable state. It is transparent — it compiles
to plain `func() T` with no runtime representation or cost — and it carries only
the compile-time capture-safety contract. The propagation rule:

- A **bare `func(...) ...`** capture is **rejected** (GALA-E0037): nothing
  vouches for its captures.
- A **`Sendable`-typed** capture is **accepted**: the vouch has already been
  made.
- The requirement **rides up wrapper signatures** and **terminates at the closure
  literal**, where the captures are actually checked. A reusable
  concurrency-wrapper API annotates its forwarded parameter `Sendable[func() T]`;
  the obligation then lands on whoever writes the closure.

**Application code passing closure literals never writes `Sendable`** — only
reusable concurrency-wrapper APIs annotate their forwarded parameters.

**Before → after.** A helper that forwards a computation into a `Future`
(mirroring an `afterDelay` / `async` shape):

```gala
// BEFORE — bare `func` parameter forwarded into a boundary: rejected.
func afterDelay[T any](d Duration, make func() T) Future[T] =
    concurrent.Future[T](() => { sleep(d); make() })
// error[GALA-E0037]: closure crossing a concurrency boundary captures function
// value "make" (type func() T) — a function value may itself close over mutable
// state, so it cannot cross the boundary unless it is Sendable
//   hint: declare the parameter as `make Sendable[func() T]` — the Sendable bound
//   requires each caller to pass a closure whose own captures are shareable, and
//   propagates the check to the call site where the closure is written.

// AFTER — the forwarded parameter is `Sendable[func() T]`: accepted.
// The bound propagates the capture-safety obligation up to the caller.
func afterDelay[T any](d Duration, make Sendable[func() T]) Future[T] =
    concurrent.Future[T](() => { sleep(d); make() })   // OK
```

The obligation terminates where the closure is actually written — the app-level
caller, which passes an ordinary lambda and never mentions `Sendable`. That is
where a genuinely unsafe capture is caught:

```gala
// OK — the closure captures only a shareable `val`.
afterDelay(oneSecond, () => base + 1)

// GALA-E0037 — caught HERE, at the closure literal, on `counter`:
var counter = 0
afterDelay(oneSecond, () => counter + 1)   // captures reassignable var `counter`
```

### Escape hatch — the advanced zone

`go_interop.Spawn` / `SpawnWithRecover` and raw goroutine plumbing are the
**low-level, unchecked** zone. Their parameters are *not* `Sendable`, so a closure
spawned through them is **not** validated — you take responsibility for sharing
correctly (mutexes, channels, atomics). Prefer `Future` for safe concurrency and
reach for `go_interop` only when you deliberately manage synchronization
yourself.
