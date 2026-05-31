# GALA Design and Tradeoffs

This document explains *why* GALA is built the way it is, and what each decision costs.
It is the companion to [WHY_GALA.MD](WHY_GALA.MD): that document makes the case for the
features; this one explains the design reasoning behind them and the trade-offs we
accepted along the way. For *how to use* a feature, see the language reference
([GALA.MD](GALA.MD)); for *how it is implemented*, the deeper sections below link into
[TYPE_INFERENCE.MD](TYPE_INFERENCE.MD) and the relevant parts of GALA.MD.

The document is layered. The first half — **Design Goals** and **Key Decisions** — is a
narrative you can read end to end without knowing the transpiler internals. The second
half — **Implementation Notes** — goes one level deeper for contributors who want to know
how a decision maps onto generated Go.

---

## Language at a Glance

From a language-design standpoint, GALA classifies as:

- **Statically typed** — every expression has a type known at transpile time; there is no
  runtime type negotiation.
- **Strongly typed** — no implicit coercions; even numeric widening is explicit. An
  unresolved type is an error, never a silent `any`/`interface{}`.
- **Type-inferred** — types are checked but rarely written. Generic params, lambda
  parameter types, variable types, and fold accumulators are inferred (see
  [TYPE_INFERENCE.MD](TYPE_INFERENCE.MD)).
- **Functional-first** — expression-oriented, immutable by default, with first-class
  functions, sealed algebraic data types, exhaustive pattern matching, and a monad stack
  (`Option`/`Try`/`Either`/`Future`/`IO`).
- **Compiled, via transpilation** — GALA is not interpreted; it lowers to readable Go
  source and is then compiled by the Go toolchain to a native single binary.
- **Eagerly evaluated**, with opt-in laziness where it earns its keep (e.g. `Stream`).
- **Memory-managed by Go's runtime** — GALA brings no GC or scheduler of its own; it lives
  within Go's.

In one line: **a statically and strongly typed, type-inferred, functional-first language
that transpiles to Go.**

---

## Design Goals

Every language is a set of trade-offs in service of a small number of goals. GALA's are:

1. **Functional expressiveness on Go's runtime.** Bring sealed types, exhaustive pattern
   matching, immutability, and a monad stack to a language that still produces native
   single-binary Go programs with goroutines and fast startup.
2. **No FFI tax.** The host ecosystem is the whole point of targeting Go. Calling a Go
   library must cost nothing — no wrappers, no bindings, no marshalling layer.
3. **Safety the compiler enforces, not the reviewer.** Immutability and exhaustiveness
   should be compile errors, not conventions checked in code review.
4. **Inference over annotation.** If the compiler has enough information to know a type,
   the programmer should not have to write it.
5. **Readable — but not hand-written — output.** The generated Go should be legible enough
   to debug, review, and trust: clear names, ordinary control flow, no obfuscation. It is
   explicitly *not* a goal to reproduce idiomatic, high-quality, human-authored Go. The
   output is a compilation target shaped by GALA's semantics — you will see `std.Immutable`
   wrappers, generated `Copy` methods, discriminator switches, and other machinery a human
   writing Go by hand would never introduce. "Readable" here means *inspectable when you
   need to*, not *indistinguishable from a person's code*.
6. **Short feedback loops — for humans and AI alike.** Errors should be concise, *coded*
   (`GALA-Exxxx`), and actionable rather than vague resolution failures. A growing share of
   GALA is written with AI coding agents in the loop, and a short, machine-readable
   diagnostic that names the problem is one an agent can act on directly instead of
   guessing — which keeps the edit/transpile/fix cycle tight. Conciseness serves the same
   goal: less ceremony means less for both reader and agent to parse before the intent is
   clear. The deepest version of a short feedback loop, though, is the error that never
   reaches runtime at all — so GALA pushes as many failures as it can into transpile time:
   exhaustive pattern matching turns a forgotten case into a compile error, and replacing
   `nil` and bare pointers with `Option`/`Try`/`Either` removes whole classes of nil-deref
   and missing-check bugs before the program ever runs. A compile-time error is the
   shortest feedback loop there is, and the easiest for an AI agent to act on.

These goals are in tension with each other — goal 1 (rich runtime semantics) pushes
against goal 5 (clean output), and goal 2 (zero-cost interop) constrains how far goal 3
(enforced safety) can reach into Go values. Most of the interesting decisions below are
about where we drew those lines.

---

## Key Decisions

### 1. Transpile to Go source, not to a VM or native code

**The decision.** GALA emits readable `.go` files and hands them to the Go toolchain.
It is not a bytecode compiler, not a JVM-style runtime, not an LLVM frontend.

**Why.** This is the decision everything else hangs on. Targeting Go *source* (rather
than, say, Go's internal SSA or a custom backend) is what makes goal 2 free: a GALA
import of `net/http` is literally a Go import of `net/http` after transpilation. We
inherit the Go compiler's optimizer, the race detector, `pprof`, cross-compilation, and
every library in the ecosystem without writing a single adapter. It also keeps the
implementation tractable for a young language — we are responsible for the front end and
the lowering, not for code generation, GC, or a scheduler.

**What it costs.**
- **A transpilation step in the loop.** There is an extra build phase, and stack traces /
  debugger line numbers refer to generated Go, not GALA source. Readable output (goal 5)
  is partly a mitigation for this: when you do drop into the Go, it is legible enough to
  follow — though it carries GALA's machinery and is not the idiomatic Go a person would
  have hand-written.
- **Latency to new Go features.** A new Go language or stdlib feature is usable from GALA
  only once the transpiler knows how to emit or infer it.
- **Semantics bounded by Go's.** GALA cannot offer anything Go's runtime can't express —
  no green-thread model of its own, no tail-call guarantee, no value semantics Go lacks.
  We lower our richer ideas onto Go's primitives, which sometimes means a wrapper (see
  immutability, below).

### 2. Immutability by default, enforced via `std.Immutable[T]`

**The decision.** `val` (and `:=` inside functions) bind immutable values; struct fields
are immutable unless marked `var`. Under the hood, immutable values are lowered to a
`std.Immutable[T]` container, and the transpiler inserts the `.Get()` unwrapping
automatically. See [GALA.MD §12](GALA.MD#12-immutability-under-the-hood).

**Why.** Goal 3 says safety should be enforced by the compiler. Go has no language-level
read-only binding for runtime values (`const` is compile-time only), so to make `val`
*mean* immutable we needed a representation Go can't silently mutate. Wrapping is that
representation: a `std.Immutable[T]` exposes no setter, so reassignment is impossible by
construction rather than by convention. Pointer bindings get the same treatment via
`ConstPtr[T]` (a C++-`const T*` analogue), which is why taking the address of a `val`
yields a read-only pointer.

**What it costs.**
- **A layer of indirection.** Wrapping and `.Get()` unwrapping add a level between the
  name and the value. For most application code this is noise the optimizer erases or
  that never matters; for tight numeric inner loops it can matter, and `var` /
  `collection_mutable` are the deliberate escape hatches.
- **Inference complexity.** Because `val` is `Immutable[T]` and `var` is raw `T`, the
  transpiler must track which is which everywhere and wrap/unwrap at exactly the right
  points. This is a real source of subtlety — it is most of what
  [TYPE_INFERENCE.MD](TYPE_INFERENCE.MD) exists to document.
- **An asymmetry to learn.** "Update by copy, not mutation" (`p.Copy(age = 31)`) is the
  idiom that replaces field assignment. The auto-generated `Copy` method is what keeps
  that asymmetry from becoming boilerplate.

### 3. Sealed types as tagged structs, with compiler-checked exhaustiveness

**The decision.** A `sealed type` with `case` variants lowers to a single Go struct
carrying a discriminator plus the union of variant fields; `match` lowers to a `switch`
on the discriminator. The transpiler verifies that a `match` over a sealed type handles
every case — a missing variant is a compile error, no `default`/`case _` required.

**Why.** Go has no algebraic data types, and the two idioms people reach for —
interfaces with type switches, or structs with an iota discriminator — share one fatal
flaw: the compiler cannot tell you when you forgot a case. Exhaustiveness is the whole
value of sum types, so GALA owns the check itself rather than delegating to a runtime
panic. The tagged-struct encoding (over, say, sealed interfaces) keeps the generated code
allocation-light and the `match` a plain integer switch.

**What it costs.**
- **Wider structs.** A variant struct carries fields that are meaningless for most cases
  (a `Circle` value still has the `Rectangle` fields present). This trades a little memory
  for a cheap, branch-predictable switch and no interface boxing.
- **Exhaustiveness is the transpiler's job, forever.** Because Go won't check it for us,
  every cross-package and generic case the checker can't yet see is a correctness bug we
  have to fix in the transpiler — never work around in user code. The coded diagnostics
  (e.g. `GALA-E0025`) exist partly to make those gaps legible.

### 4. Monadic error handling layered on top of Go errors, not replacing them

**The decision.** `Option[T]`, `Try[T]`, and `Either[E, T]` are ordinary sealed types
with `Map`/`FlatMap`/`Filter`/recovery combinators. Go functions returning `(T, error)`
are automatically adapted: inside a `Try` lambda, a non-nil error becomes a `Failure`.

**Why.** Goal 2 forbids an FFI boundary, so we can't ask the Go ecosystem to speak in
`Either`. Instead the monads sit *on top of* `(T, error)` and convert at the edges. This
keeps the happy path readable (a pipeline of `.Map`/`.FlatMap` instead of interleaved
`if err != nil`) while every Go call remains directly reachable. Because the monads are
sealed types, pattern-matching their results is exhaustive — error handling you can't
silently forget.

**What it costs.**
- **Two error idioms coexist.** At the boundary you still meet `(T, error)`; inside GALA
  you work in `Try`/`Either`. The auto-unwrapping hides most of the seam, but it is a seam.
- **A conceptual prerequisite.** `FlatMap` and railway-oriented composition are an
  investment. For a team new to FP this is a cost before it is a benefit — called out
  honestly in [WHY_GALA.MD](WHY_GALA.MD#when-not-to-use-gala).

### 5. Aggressive local type inference, via a two-layer engine

**The decision.** Generic params on calls, lambda parameter types, variable types, and
fold accumulator types are all omitted and inferred. Inference uses two layers: a fast
"manual" path for the common cases, falling back to a Hindley-Milner solver for the hard
ones. See [TYPE_INFERENCE.MD](TYPE_INFERENCE.MD).

**Why.** Goal 4: annotations the compiler could derive are noise. `Some(42)` over
`Some[int](42)`, `people.Map((p) => p.Name)` over a spelled-out generic call. The two-layer
design is itself a trade-off — the manual layer keeps the common path fast and
predictable, while HM handles the cases where local reasoning isn't enough, so we don't
pay full constraint-solving cost on every expression.

**What it costs.**
- **Inference can fall short across boundaries.** The historically rough edge has been
  complex generics *across package boundaries*, where the call-site expected type has to
  flow into a constructor defined elsewhere. This area has had the most fixes and is where
  new bugs are most likely to surface.
- **Error legibility.** When inference fails, the message has to explain something the
  programmer never wrote. Coded diagnostics (`GALA-Exxxx`) are the answer to keeping those
  errors actionable rather than vague.
- **Annotation is still required where there's nothing to infer from** — `None[int]()`,
  `Left[string, int](...)`, empty collections, standalone lambdas. The rule is "omit what's
  inferable," not "never annotate."

### 6. Reuse Go's ecosystem rather than build a parallel one

**The decision.** GALA ships a functional standard library (collections, monads, codecs,
crypto, IO) but does *not* reinvent what Go already does well. Go packages are imported
and called directly; `go_interop` is the explicit bridge for slices, variadics, and byte
conversions.

**Why.** A transpiled language is only as useful as its access to the host ecosystem
(goal 2). Building a parallel ecosystem would both waste effort and reintroduce the FFI
tax we set out to avoid. The standard library's job is to make the *functional* idioms
first-class (so `Array`/`HashMap`/`Option` feel native), not to wall GALA off from Go.

**What it costs.**
- **Two collection worlds.** GALA's immutable `Array`/`List`/`HashMap` and Go's slices/maps
  both exist, and you convert at the interop boundary (`SliceOf`, `.ToGoSlice()`). This is
  a deliberate, visible seam rather than a hidden one.
- **No special-casing, even for `std`.** A hard project rule: the standard library resolves
  through the same import mechanism as any third-party library. This keeps the model
  honest but means the stdlib gets no shortcuts the language wouldn't give everyone.

### 7. Persistent immutable collections as the default

**The decision.** The default `Array`/`List`/`HashMap`/`HashSet`/`TreeSet` from
`collection_immutable` are persistent (structurally shared) immutable data structures.
Mutable variants live in `collection_mutable` for hot paths.

**Why.** Immutability by default (goal 3) extends naturally to collections, and structural
sharing is what makes immutable collections practical rather than a copy-everything tax:
`Array` gives O(log₃₂ n) random access, `List` gives O(1) prepend. You get persistence and
safe sharing without the naive cost of cloning on every update.

**What it costs.**
- **Not free versus a raw slice.** O(log₃₂ n) is effectively constant for realistic sizes,
  but it is not a bare array index, and there is allocation behind structural sharing. The
  `collection_mutable` package and `arrayBuilder` exist precisely so performance-sensitive
  code has an opt-out.
- **A decision the programmer must make.** "Which collection?" is now a real choice
  (`Array` vs `List`, immutable vs mutable). [GALA_BEST_PRACTICES.MD](GALA_BEST_PRACTICES.MD)
  exists in part to make that choice mechanical.

---

## Implementation Notes

This section goes one layer deeper for contributors. It assumes familiarity with the
transpiler layout in [CLAUDE.md](../CLAUDE.md) (`internal/transpiler/transformer` for
GALA-AST → Go-AST, `internal/transpiler/generator` for emission).

### Immutability lowering and the wrap/unwrap discipline

The core representational rule: **`val` → `std.Immutable[T]`, `var` → raw `T`.** The
transpiler wraps a value when it is bound to a `val` and inserts `.Get()` wherever that
binding is *used* as its underlying value. Getting this right everywhere — call arguments,
struct fields, match scrutinees, interpolation, closures — is the bulk of the inference
work, and a mis-wrapped or mis-unwrapped value is a classic transpiler-bug shape. Pointer
immutability is a separate axis: GALA distinguishes the *binding's* mutability (can the
pointer be reassigned) from the *pointee's* mutability, and `ConstPtr[T]` represents a
read-only pointer obtained from a `val`. This is why an immutable pointer field can't be
assigned `nil` — a forever-nil immutable pointer is useless, so it's a compile error
directing you to `var`. Full rules: [GALA.MD §12](GALA.MD#12-immutability-under-the-hood).

### The two-layer inference engine

Inference is split deliberately (see [TYPE_INFERENCE.MD](TYPE_INFERENCE.MD)):

- **Layer 1 — manual inference** (`getExprTypeNameManual`): a direct, fast walk that
  resolves the overwhelmingly common cases (literals, known method signatures, obvious
  generic instantiations) without building a constraint system.
- **Layer 2 — Hindley-Milner** (`inferExprType`): the fallback for genuinely ambiguous
  cases, where types must be solved from constraints rather than read off directly.

The split is a performance/power trade-off: full HM on every expression would be slower
and its failures harder to explain, while manual-only would miss the hard cases. The
hardest region for both layers is cross-package generic inference, where the expected type
at a call site must propagate into a generic constructor or `.Map` defined in another
package. Most recent inference fixes live in that region.

### Go-SDK-assisted inference is optional, by design

For resolving Go stdlib/third-party signatures (return types, struct fields, method
signatures), the transpiler can consult the Go SDK at transpile time. This is wired
through `.bazelrc` (`--action_env=GOROOT`/`PATH`) and `no-sandbox` tags on the transpile
rules. Crucially, **if the Go SDK is absent, transpilation still succeeds** — Go type
inference is simply disabled with a warning, and GALA's own type system is unaffected.
That graceful degradation is intentional: it keeps GALA's core independent of an external
toolchain while still letting interop inference get sharper when the SDK is present.

### Exhaustiveness and coded diagnostics

Exhaustiveness checking for `match` over sealed types is performed by the transpiler
because Go cannot perform it for us. As the type system reaches across packages and into
generics, gaps in the checker become correctness bugs — hence the project rule to reproduce
any such gap as a minimal test and fix the transpiler rather than work around it in GALA.
The `GALA-Exxxx` coded diagnostics (see [docs/errors/](errors/)) are the user-facing side
of this: a named, documented error beats a vague resolution failure when inference or
resolution can't proceed.

---

## The Cross-Cutting Trade-off: the transpilation tax

Most individual costs above are instances of one underlying tax: **GALA buys
expressiveness with a layer between you and Go.** That layer is the transpiler, the
`Immutable`/`ConstPtr` wrappers, the monad adapters, and the persistent-collection
representations. The payoff is real (enforced immutability, exhaustive matching, readable
functional pipelines); the recurring price is indirection that you usually don't see but
occasionally must reason about — when profiling a hot loop, when reading a stack trace in
generated Go, or when inference can't see across a package boundary.

GALA's design response is consistent: **make the safe, expressive thing the default, and
make the escape hatch explicit and visible** — `var` for mutation, `collection_mutable`
and `arrayBuilder` for performance, `go_interop` for the raw Go boundary. You opt *out* of
the safety layer where you've decided you need to, rather than opting in to safety
everywhere.

---

## What GALA Deliberately Does Not Do

Stating the non-goals is part of the design:

- **It does not replace Go.** Rewriting a large Go codebase in GALA is rarely justified;
  GALA targets new modules where the expressiveness pays off. (See
  [WHY_GALA.MD](WHY_GALA.MD#when-not-to-use-gala).)
- **It does not give `std` special treatment.** The standard library uses the same import
  and resolution mechanism as any other library — no hardcoding in the transpiler.
- **It does not emit `any`/`interface{}` to paper over inference failures.** GALA is
  type-safe; an unresolved type is an error, not an `interface{}`.
- **It does not invent runtime semantics Go lacks.** No custom scheduler, no GC of its
  own — it lowers onto Go's runtime and lives within it.
- **It does not work around its own transpiler bugs.** A failing construct becomes a repro
  test and a transpiler fix, never a rewritten-to-dodge-it idiom.

---

## Summary

| Decision | Buys you | Costs you |
|----------|----------|-----------|
| Transpile to Go source | Zero-cost ecosystem access, Go toolchain for free | Build step; lag on new Go features; bounded by Go semantics |
| Immutability via `std.Immutable[T]` | Compiler-enforced `val`; safe sharing | Indirection; inference complexity; copy-not-mutate idiom |
| Sealed types as tagged structs | Exhaustive matching, cheap integer switch | Wider structs; exhaustiveness is the transpiler's burden |
| Monads over Go errors | Readable happy path, exhaustive error handling | Two error idioms at the seam; FP learning curve |
| Two-layer type inference | Minimal annotations | Cross-package generic edges; failure-message complexity |
| Reuse Go's ecosystem | No FFI tax | Two collection worlds; explicit interop boundary |
| Persistent immutable collections | Safe sharing, practical immutability | Not a bare slice; collection-choice decision |

For the feature-by-feature case and side-by-side Go comparisons, see
[WHY_GALA.MD](WHY_GALA.MD). For the full language reference, see [GALA.MD](GALA.MD). For the
inference internals, see [TYPE_INFERENCE.MD](TYPE_INFERENCE.MD).
