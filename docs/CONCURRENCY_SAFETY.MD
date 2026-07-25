# Concurrency Safety: the Shareability Model

GALA is adding **compile-time data-race safety** for values that cross a
goroutine boundary — a value captured by, or passed into, a `Future`, a
`Spawn`, or any other construct that runs work on another goroutine.

This document describes the **shareability model** and the `Shareable` type
predicate that decides it.

> **Status.** This document describes the predicate only. The predicate is
> currently wired into nothing — there is no enforcement, no surface syntax, and
> no behaviour change. See [Enforcement — a later PR](#enforcement--a-later-pr).

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
identical-typed one that captures nothing is safe. Deciding that is the job of a
later **capture-analysis** pass, which will inspect a closure passed to a
`Sendable` parameter and verify each captured free variable is itself
shareable. This type-only predicate cannot and does not make that call.

### Unbounded type parameters

A bare type parameter `T` with no bound returns `false`. A future PR introduces
a `Shareable` bound; a `T: Shareable` will then be provably shareable, but until
the bound is present the predicate cannot guarantee it.

---

## Enforcement — a later PR

This predicate is the **foundation only**. It is not called by any non-test code
yet. Enforcement lands in a later PR and will add:

- a **`Sendable`** bound / boundary marker for the parameters that cross a
  goroutine boundary (Future / Spawn),
- **capture analysis** for closures passed where a `Sendable` is expected,
- a dedicated **error code** and diagnostic that points at the offending value
  and explains *why* it is not shareable (e.g. "`Bag` has a
  `collection_mutable.Array` field").

Until then, sending a non-shareable value compiles exactly as it does today; the
model described here simply defines what *will* be allowed.
