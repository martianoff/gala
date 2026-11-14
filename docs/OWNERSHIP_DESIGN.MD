# Ownership: Inferred Uniqueness and `inout` Parameters

Status: **proposal**, for maintainer review. Nothing here is implemented.
Discussion: [#961](https://github.com/martianoff/gala/discussions/961).
Related: [FORMAL_SEMANTICS.MD](FORMAL_SEMANTICS.MD), [CONCURRENCY_SAFETY.MD](CONCURRENCY_SAFETY.MD).

This document proposes an ownership discipline for GALA. It exists for two reasons:

1. A deterministic GALA → Lean translator (`gala2lean`) can translate a function only if
   every mutable value it touches has a single owner. Shared mutable state behind a
   pointer has no faithful value-semantics translation.
2. The `Sendable` boundary ([GALA-E0037](errors/GALA-E0037.md)) rejects every
   non-`Shareable` capture, even a freshly built buffer that the sending goroutine never
   touches again. Ownership can prove such a hand-off safe.

Ownership is a language-level decision, so this is a design to review. No code changes
come with it.

## Summary

- **Phase 1: inferred uniqueness.** This is an analysis with no syntax and no new
  errors. It decides which `Ptr[T]` values have a single owner and are only lent
  downward, and gives each function a *borrow summary*. The translator and the coverage
  report use it. On the measured code it recovers about **209 functions (3.1% of 6,669)**.
- **Phase 2: `inout` parameters.** These are opt-in, second-class and have no lifetimes,
  in the style of Swift and Hylo. In Go they lower to `*T`, exactly the Go a programmer
  writes with `Ptr[T]` today. They turn the inferred summary into a checked contract.
  They also cover what inference cannot see: function types and fields such as
  gala-tui's `Render func(Rect, *Buffer, *HitRegistry)`.
- **Phase 3: non-escaping closures may capture an `inout` place.** This is what
  `rules.ForEach((r) => b.add(r))` needs. It recovers about **69 more functions (1.0%)**.
- **Phase 4: transfer across `Sendable`.** A capture of a disconnected value is accepted
  when nothing uses it after the send. E0037 accepts more programs, and nothing it
  accepts is unsafe.
- **Not proposed:** Rust lifetimes, borrowed results, destructors, or any change to Go's
  GC or to the Go generated for existing programs.
- **Coverage, measured:** translatable functions go from **72.0% to about 75.2%** after
  phases 1 and 2, and **76.2%** after phase 3. With models for owned Go slices and
  captured `var`s, the upper bound is about **79%**. The research estimate of "about 80%"
  was optimistic. Its "about 7%" of pointer-blocked functions includes persistent
  collection internals, escaping pointers, and functions that have a second blocker.
- **Interop guarantees ([3.5](#35-interop-guarantees)):** `inout`'s guarantees also hold
  at the Go boundary, in both directions:
  - Go calls into an exported `inout` function through a copy-in/copy-out wrapper.
  - GALA may pass an `inout` place to Go only when the callee is on an allow-list of
    functions that don't retain pointers. CI checks the list against the pinned Go
    compiler's escape analysis.

```mermaid
flowchart LR
    SRC[GALA source] --> TR[gala transpiler] --> GO[Go: unchanged<br/>inout lowers to *T]
    SRC --> P[galop parser] --> EXP[Core JSON exporter]
    EXP --> OWN[ownership analysis<br/>borrow summaries]
    OWN --> G2L[gala2lean]
    G2L --> LEAN[Lean 4<br/>value semantics]
    OWN -->|escape reasons| COV[COVERAGE.md<br/>UNSUPPORTED counts]
    TR -. phase 2: inout checks .-> OWN
    TR -. phase 4: Sendable transfer .-> OWN
```

## 1. Goals and non-goals

**Goals**

| # | Goal | Measure |
|---|---|---|
| G1 | **Faithful Lean translation of single-owner mutable state.** A function that mutates state through a uniquely owned pointer translates to a pure Lean function `T → M (R × T)`, with no heap model. | Share of functions that gala2lean translates, reported in `formal/COVERAGE.md`. |
| G2 | **Safe moves across `Future`/`Sendable`.** A freshly built mutable value may be handed to another goroutine when the sender never uses it again. | Programs E0037 rejects today that are race-free by construction. |
| G3 | **No change to the generated Go or to Go's GC.** Existing programs transpile byte for byte as today. `inout` lowers to the `*T` code that `Ptr[T]` produces. The one addition is a copy-in/copy-out wrapper on Go-visible `inout` functions (3.5.1). | Unchanged golden outputs; no new runtime package. |
| G4 | **Opt-in.** No existing program changes meaning or stops compiling. | Zero source changes required in std, galop, gala-tui, gala-team and gala-acp. |

**Non-goals**

- Lifetimes, lifetime annotations, or borrows that outlive a call.
- Functions that return a borrow (`&mut` results, Swift `_modify`, Hylo subscripts).
  Section 3.8 explains why leaving them out keeps the Lean translation closure-free.
- Deterministic destruction, linear types or move-only types. Resources keep `use`
  and `defer`.
- Translating Go interop (10% of functions) or concurrency (3%). Ownership does not help
  either one.
- Translating mutable collections. `collection_mutable` is *modelled* in Lean, not
  translated.
- Changing what `Ptr[T]`, `*T` or `&x` mean in existing code.

## 2. Measurement

The research scan classifies each of the 6,669 non-test functions in galop, std, the
examples, gala-team, gala-tui and gala-acp by the first feature that blocks a translation.
72% need no ownership. The `pointer` class (438 functions, 6.6%) is the target here.

To size each phase, the scan was extended. For every `pointer` function, it now decides
which mechanism would fix it:

| Sub-class | Meaning |
|---|---|
| `uniq-local` | Every pointer is allocated in the function and only lent downward. |
| `uniq-return` | Same as `uniq-local`, but the fresh pointer is returned (a constructor such as `newBuilder`). |
| `inout` | The pointers are parameters or receivers, used only downward: field reads and writes, method calls, and arguments to GALA calls. |
| `inout-closure` | Same as `inout`, but a lambda or a method value (`p.alternative`) captures the pointer. |
| `escape` | The pointer is stored in a field, collection, constructor or `Option`, aliased into another binding, returned while it is a parameter, or passed to Go. |
| `go-ptr` / `other` | The pointee is a Go type, or no named pointer binding was found (a heuristic miss). |

A function counts as **fixed** only when the pointer is its sole blocker. With the
pointer ignored, it must classify as pure, local-var, go-pure or ptr-read.

| Tree | Funcs | Pointer | uniq | inout | inout-closure | escape | other / go-ptr | Fixed by P1+P2 | Fixed by P3 | Second blocker (Go slice, mutable coll.) |
|---|---|---|---|---|---|---|---|---|---|---|
| galop | 575 | 96 | 0 | 58 | 30 | 6 | 2 | 57 | 30 | 1 |
| gala-tui | 1,363 | 101 | 1 | 57 | 31 | 6 | 6 | 51 | 29 | 12 |
| std | 1,122 | 127 | 1 | 89 | 7 | 18 | 12 | 77 (46 net) | 6 (4 net) | 27 |
| examples | 2,290 | 112 | 5 | 56 | 6 | 14 | 31 | 55 | 6 | 32 |
| gala-team, gala-acp, concurrent | 874 | 2 | 0 | 0 | 0 | 0 | 2 | 0 | 0 | 2 |
| **Total** | **6,669** | **438** | **7** | **260** | **74** | **44** | **53** | **240 (209 net)** | **71 (69 net)** | **74** |

"Net" subtracts 31 + 2 functions in `collection_immutable`'s persistent trees (`Array`,
`TreeMap`, `TreeSet`). Their nodes are shared by structural sharing, so they are not
single-owner. A translation models these collections with Lean's own structures and does
not translate their internals.

| Tree | Today | After P1+P2 | After P3 |
|---|---|---|---|
| galop | 75.3% | 85.2% | 90.4% |
| gala-tui | 86.7% | 90.5% | 92.6% |
| std | 68.4% | 72.5% | 72.8% |
| examples | 75.1% | 77.5% | 77.7% |
| **All** | **72.0%** | **75.2%** | **76.2%** |

Two further steps raise the upper bound to about 79%:

- Captured `var`s (106 functions, 1.6%) use the same non-escaping-closure rule as phase 3.
  The scan cannot tell which of these closures escape, so this is an upper bound.
- Owned Go slices inside owned structs (74 functions) can be modelled as Lean `Array`
  under the freshness rule in section 2.4.

The scan is textual and heuristic. The galop and gala-tui figures were checked by reading
the code. The findings below are what the counts alone do not show.

### 2.1 galop: the ideal case

All of galop's mutable state lives in eight private types: `parser`, `lexer`, `builder`,
`configSet`, `rng`, `memory`, `usage` and `buffer`. Each is allocated once and lent down a
recursive-descent call tree.

- `Lex` allocates `&lexer{...}` locally and calls `Ptr[lexer]` methods. It never stores,
  returns or captures the pointer. This is uniqueness plus borrowed receivers, so the
  whole lexer translates. `lexer.data` is a `[]byte` that is only read, and it is
  modelled as a `ByteArray`.
- `Parse` allocates `&parser{...}` inside a `FlatMap` lambda, local to that lambda.
  `sepBy1(p, "|", p.alternative)` passes `p` and also a method value that captures `p`.
  Under phase 1 that is an alias, and under phase 2 it is an exclusivity error.
  Section 3.9 shows the rewrite: `item func(inout parser) T`. Phase 3 does not help
  here either, because the closure argument captures the place that the same call
  borrows `inout`.
- `newBuilder` returns a fresh `Ptr[builder]` (`uniq-return`). Its
  `rules.ZipWithIndex().ForEach((ri) => { b.ruleStart = ... })` is a non-escaping
  closure that captures `b`, so it needs phase 3. 13 `builder` methods have this shape.
- `sample.walker` stores `Rand Ptr[rng]` and `Used Ptr[usage]` in a value struct. That is
  a real escape. To translate it, `walker` must hold `rng` and `usage` by value and be
  passed `inout`.

### 2.2 gala-tui: inference is not enough

- `Buffer` holds `var cells []Cell` and writes `b.cells[idx] = c`. `NewBuffer` builds a
  fresh slice and `Clone` copies one. A translation needs the owned-slice rule (2.4).
- The widget tree is a public API that passes pointers through **function values**:
  `case CustomW(MinW int, MinH int, Render func(Rect, *Buffer, *HitRegistry))` and the
  backend's `Paint func(*Buffer, *Buffer)`. Inference has no body to summarise behind a
  function value, so a call through one counts as an escape. `renderToInternal` is
  blocked until the function type can say `func(Rect, inout Buffer, inout HitRegistry)`.
  This is the strongest case for phase 2.
- `HitRegistry` entries carry `Payload func(int, int) any`. The registry's own state
  translates, but `any` payloads remain Go interop.

### 2.3 std

- **`json` encoder and decoder (39 functions):** private `Ptr[...Impl]` receivers, lent
  downward. The encoder holds a `bytes.Buffer`, which needs a model for an owned
  `bytes.Buffer`.
- **`strings.StringBuilder`:** its fluent API returns the receiver
  (`func (sb Ptr[StringBuilder]) Append(s Str) Ptr[StringBuilder]`). A second-class
  `inout` cannot return its borrow, so fluent builders stay `Ptr`. This is a deliberate
  limitation. See open question Q9.
- **`lazy.Lazy`:** memoisation through a shared pointer, which is a genuine escape.

### 2.4 Owned Go slices

A Go slice field of an owned struct is modelled as a Lean `Array` only if the field is
**fresh**: every assignment to it is from `SliceWithCapacity`, `SliceEmpty`, a copy, or
`SliceAppend(field, …)` of the field itself. Otherwise two structs could share a backing
array, and the field stays `go-mutable`. This is a modelling trust point. Its Lean model
(`Array.set!`, `Array.push`) belongs in the prelude.

## 3. Design

### 3.1 Phase 1: inferred uniqueness

The analysis runs on the Core representation the exporter already produces. It is part of
the formal tooling, not of `gala build`, so it costs nothing in normal compilation.

**Definitions.** A *place* is a `var` local, an `inout` parameter (phase 2), or a field
path `x.f.g` through `var` fields of a place. A pointer binding is `p: Ptr[T]`, or a `*T`
that is known not to be nil.

A pointer binding `p` in function `f` is **unique** when both of these hold:

1. **Fresh origin.** `p` is bound to one of:
   - an allocation: `&T{…}` or `&T(…)`;
   - a call whose summary is *returns fresh*;
   - a borrowed parameter of `f`, which is unique for the duration of the call (see
     below).

   `&v` of a local `var` is fresh only when `v` is never used directly again. Otherwise
   `v` and `p` are two access paths to the same value.
2. **Every use is a lend.** Each occurrence of `p` is one of:
   - a field read `p.f`, or a field write `p.f = e` where `e` does not mention `p` as a
     whole;
   - a deref read `*p`, which yields a copy;
   - a method call `p.m(…)` whose receiver summary is *borrowed*;
   - an argument to a GALA function whose summary for that parameter is *borrowed*;
   - a `return p` that makes `f`'s summary *returns fresh*. This applies only when `p`
     came from an allocation.

**What counts as escaping.** Any other use is an escape. The table lists them.

| Escape | Example | Why it breaks single ownership |
|---|---|---|
| Stored | `w.Rand = rand`, `walker(…, rand, …)`, `Some(p)`, `(p, 1)` | A second path to the pointee outlives the call. |
| Aliased through a collection | `xs.Append(p)`, `ArrayOf(p)`, `m.Put(k, p)` | Same as stored. |
| Aliased into a binding | `val q = p` | Two names. The analysis could track this, but phase 1 does not. |
| Returned (when not fresh) | `func f(p Ptr[T]) Ptr[T] = p` | The caller now holds a second path. |
| Captured | `() => p.x`, `xs.ForEach((x) => p.add(x))`, the method value `p.alternative` | Go closures capture by reference, and the closure may outlive the call. Phase 3 relaxes this for non-escaping closures. |
| Passed to Go | `bufio.NewWriter(p)`, an `any` or interface conversion, a Go `func(*T)` slot | Go may store it. Ownership does not cross the interop boundary. |
| Compared | `p == q` | Pointer identity has no value-semantics meaning. |
| Passed twice | `f(p, p)`, `p.blit(p)` | The callee sees two aliases. This is an exclusivity violation. |
| Global | a package-level `var` holding a `Ptr` | It is reachable from anywhere. |
| Unknown callee | calling a function value with `p` as an argument | No summary is available. Phase 2's `inout` function types fix this. |

**Shape restriction.** The pointee type `T` must itself be owned-shaped. Every field must
be a value: a primitive, a GALA value type, an immutable collection, or a fresh Go slice
(section 2.4). A `Ptr` or `*U` field makes the pointee a graph, not a tree, and the binding
cannot be translated.

**Summaries and the fixed point.** Every function gets a summary in two parts:

- one bit per pointer parameter and receiver: *borrowed* or *escapes*;
- one bit for the result: *returns fresh*.

Summaries are computed per strongly connected component of the call graph, as a
*greatest* fixed point. Recursive calls start optimistic (borrowed) and are demoted until
nothing changes. The lattice has height 2 per bit, so the cost is linear in program size
times a small constant. Exported functions get summaries too, and they are written into
package metadata, as the non-nil property of a `Ptr` receiver already is. A caller in
another package can then use them.

```mermaid
flowchart TD
    A[pointer binding p in f] --> O{fresh origin?<br/>allocation, fresh call,<br/>or borrowed param}
    O -->|no| X[not unique<br/>f untranslatable at this use]
    O -->|yes| U{every use a lend?}
    U -->|stored, aliased,<br/>captured, Go, ==,<br/>passed twice| X
    U -->|yes| S{pointee owned-shaped?}
    S -->|Ptr field, shared slice| X
    S -->|yes| OK[unique: translate as a value<br/>param summary = borrowed]
    X --> R[reason recorded in COVERAGE.md]
```

**Exclusivity at a call site.** A callee with two borrowed parameters assumes they are
disjoint. A caller meets this by construction: an argument must be a unique local or a
borrowed parameter of the caller, and the same binding may not appear twice. A unique
binding has never been stored, so no other argument can reach it.

**Soundness argument.** Take a unique binding `p`. At every program point, the only live
access path to its pointee is `p`, or the borrowed parameter that `p` was lent to while
that call is active. Borrowed parameters follow a stack discipline: a callee cannot store,
return, capture or pass on its parameter except by lending it further down. So a write
through `p` is observable only through `p`.

The proof is a simulation. Relate the heap semantics, where the pointee is a cell, to the
value semantics, where the cell's content is a value threaded through the function and
returned from each borrowing call. The relation holds after every step, by induction on
evaluation: a field write updates the cell and the threaded value identically, and a
borrowing call returns the updated value, which the caller rebinds. Nothing else reads
the cell, so nothing can tell the two semantics apart.

In Lean this becomes a stretch theorem, **T8**: for a program the analysis accepts,
galaspec's evaluation with a heap agrees with the translation in value semantics. T8 needs
galaspec to grow a heap model for `Ptr` (open question Q12).

**Panics.** Go keeps writes made before a panic. If the panic is caught (`Try`, its
combinators, `recover`) while a borrowed place is still live, Go shows the partial update.
A translation that threads the value through the *result* would drop it. Phase 1
therefore treats a borrowing call inside a panic-catching construct as an escape when the
borrowed place is used after the catch. Q5 discusses keeping such places in the monad's
state instead. The prototype's `EStateM` keeps its state when an error is raised.

**Diagnostics.** Phase 1 issues no compile errors and no warnings. It does not change the
language. Its output is translator-facing:

- an `UNSUPPORTED ptr-escape` verdict per function, with the reason and position, for
  example `p captured by method value p.alternative at parser.gala:291`;
- counts per reason in `COVERAGE.md`;
- optionally, an LSP hover line: `Ptr[parser] p: unique (borrowed by 31 methods)`.

**What it covers.** Uniqueness alone covers 4 function bodies. The borrow summaries it
infers cover the 205 net `inout`-class functions, provided the whole call graph is
visible. galop's `parser`, `lexer`, `builder` and `configSet` qualify entirely. Phase 1
does not cover function values, closures, or a gala-tui widget written by a user in
another package.

### 3.2 Phase 2: `inout` parameters

Inference is silent and fragile. One edit to a body, such as storing `p` in a debug
field, makes a whole call tree untranslatable without a single compile error. An `inout`
parameter turns the summary into a checked contract, and it adds three things inference
cannot give:

- `inout` in function types and fields;
- mutation of value types with no `Ptr` in the API;
- precise errors at the line that breaks ownership.

#### 3.2.1 Syntax options

| Option | Declaration | Call site | Assessment |
|---|---|---|---|
| **A. `inout` modifier (recommended)** | `func advance(inout l lexer)`, `func (inout l lexer) advance()` | `advance(&l)`; receiver calls stay `l.advance()` | Mirrors the existing `var n int` parameter modifier. Swift readers know it. `&` at the call site is the Go that gets generated. |
| B. Marker type `Mut[T]` | `func advance(l Mut[lexer])` | `advance(&l)` | Consistent with `Sendable[F]` and `Ptr[T]`, but a type invites `Array[Mut[T]]`, `Option[Mut[T]]` and `Mut` fields, which would all need bans. A second-class *type* is a contradiction. |
| C. `Ptr[T]` parameters are non-escaping by default | unchanged | unchanged | No new syntax, but **mandatory and breaking**. It rejects the 44 escaping functions, std's persistent-node internals, fluent builders, and every Go-facing `*T`. Rejected. |
| D. Reuse `var` parameters | `func advance(var l lexer)` | `advance(&l)` | `var n` already means "a local mutable copy". Changing it changes the meaning of valid programs. Rejected. |
| E. `mut` / `ref` modifier | `func advance(mut l lexer)` | `advance(&l)` or `advance(ref l)` | Equivalent to A. `mut` suggests Rust's `&mut` and its lifetimes; `ref` suggests C#'s, with no exclusivity. A matter of taste (Q2). |

The rest of this section uses option A.

```gala
type parser struct {
    toks Array[Token]
    var pos int
    var err Option[Diagnostic]
}

// A value receiver: reads only.
func (p parser) peek() Token = p.toks.Get(p.pos)

// An inout receiver: the caller's parser is updated in place.
func (inout p parser) advance() Token {
    val t = p.peek()
    if !t.Kind.isTokEOF() {
        p.pos = p.pos + 1
    }
    t
}

func Parse(toks Array[Token]) Either[Diagnostic, Grammar] {
    var p = parser(toks = toks, pos = 0, err = None[Diagnostic]())
    val g = grammar(&p)          // an explicit argument needs &
    p.err match {
        case Some(d) => Left(d)
        case None() => Right(g)
    }
}
```

#### 3.2.2 Flow of an `inout` call

```mermaid
sequenceDiagram
    participant C as caller (var p)
    participant F as callee (inout q)
    participant G as Go (generated)
    C->>C: evaluate arguments left to right
    C->>F: lend place p (exclusive for the call)
    Note over C: p is inaccessible to the caller<br/>and to every closure until F returns
    F->>F: read and write q.pos and q.err,<br/>reassign q, lend q further down with &q
    F-->>C: return result; p holds the final value of q
    Note over G: Go: func grammar(q *parser) Grammar<br/>caller: grammar(&p). Same as Ptr code today
```

#### 3.2.3 Typing rules

| Rule | Statement |
|---|---|
| **Binding** | `inout x T` binds `x` as a *mutable place* of type `T` in the body. `x` can be read, its `var` fields can be written, and `x = e` reassigns it, even when every field of `T` is `val`. This is Hylo's mutable value semantics: an immutable struct is updated by replacement (`p = p.Copy(pos = p.pos + 1)`). |
| **Argument** | At an `inout` position the argument is `&e`, where `e` is a mutable place: a `var` local, an `inout` parameter, a `var` field path of a place, or the pointee of a `Ptr` (`p`, `p.f`). A `val`, a `val` field, a call result, a literal, a collection element or a value receiver is an error. |
| **Receiver** | `func (inout x T) m()` is called as `v.m()` on a `var` (auto-`&`, as a `Ptr` receiver is today), on an `inout` parameter, or on a `Ptr[T]`. On a `val`, a call result or a literal it is an **error**. Compare `Ptr` receivers, which silently mutate a copy through `AddrOfCopy` (Q4). |
| **Exclusivity** | No two `inout` arguments of one call, the receiver included, may name overlapping places: the same root, or a path where one is a prefix of the other (`&p` and `&p.lex`). Disjoint fields of the same `var` (`&p.a`, `&p.b`) are allowed. No other argument of that call may be a closure that captures an overlapping place. Plain reads of the place in other arguments are allowed: they run before the call, left to right, and produce copies. |
| **No escape** | An `inout` binding `x` may be read and lent (`&x` to another `inout` position) but not captured by a lambda or method value (phase 3 allows non-escaping closures). `&x` may not be stored, returned, put in a collection or `Option`, converted to `any` or an interface, passed to a `Ptr[T]` or `*T` parameter, or passed to Go. |
| **Second-class** | `inout` appears only on parameters, receivers and the parameters of function types. It is not a type. A field, local, result or type argument cannot be `inout`. |
| **Function types** | `func(inout T, A) R` is a GALA function type distinct from `func(Ptr[T], A) R`. Both lower to Go's `func(*T, A) R`. A function with an `inout` parameter fills a `func(inout T…)` slot. It can also fill a Go `func(*T)` slot, where it is wrapped in the copy-in/copy-out adapter of [3.5.1](#351-go-calls-gala), because Go may call it with an aliased pointer. A `func(*T)` value never fills a `func(inout T)` slot, because it may store its argument. |

**Interactions**

- **`val` / `var`:** only a `var` (or a place reached from one) can be lent. To lend a
  `val`, copy it first: `var c = v; f(&c)`. That is E0076's existing advice.
- **`Ptr[T]`:** a `Ptr` can be lent to an `inout T` with `&*p` or just `p`. This is the
  bridge from existing code. The program compiles either way. Whether the *caller*
  translates depends on phase 1 proving `p` unique.
- **Lambdas:** `(inout b Buffer) => b.Fill(r, c)` is a lambda with an `inout` parameter.
  It fills a `func(inout Buffer)` slot. A lambda may not capture an outer `inout` binding
  until phase 3.
- **Generics:** `inout x T` with `T` a type parameter is allowed. `inout` is not a type,
  so a generic `func(A) R` cannot be instantiated with an `inout` `A`. A higher-order
  function that lends state says so: `func sepBy1[T any](inout p parser, sep string,
  item func(inout parser) T) Array[T]`.
- **Pattern matching:** `x match { … }` on an `inout` binding matches a copy. Pattern
  variables are `val`s. To change the case, assign `x = Some(n + 1)`. `inout` pattern
  bindings (projections into a case) are not part of this proposal (Q10).
- **`Shareable` / `Sendable`:** an `inout` binding can never be captured by a `Sendable`
  closure, because the no-escape rule already forbids the capture. A `Shareable` value
  may be lent `inout`: the *binding* changes, and the old value stays immutable.
- **Method values:** `x.m` where `m` has an `inout` receiver captures `x`, so it is an
  escape unless called on the spot.
- **Interfaces:** an `inout`-receiver method is a Go pointer-receiver method, so the
  method-set rules of `Ptr` receivers apply unchanged (the E0080 family). Q8 asks whether
  an interface may declare that a method is `inout`.
- **Go interop:** `inout x T` lowers to the Go parameter `x *T`:
  - a whole read of `x` becomes `*x`, and `x = e` becomes `*x = e`;
  - `x.f` stays `x.f`, and `&v` stays `&v`;
  - an exported function checks its `inout` parameters for nil on entry, as it does for
    `Ptr` parameters.

  Go code may hold, share or alias the pointer it passes. An exported function or method
  with an `inout` parameter is therefore reached from Go through a copy-in/copy-out
  wrapper ([3.5.1](#351-go-calls-gala)). Passing an `inout` place *to* Go is limited to
  allow-listed callees ([3.5.2](#352-gala-calls-go)).

#### 3.2.4 Error codes

New codes get the next free numbers when they are implemented. Each needs a page in
`docs/errors/` and `website/docs/errors/`.

| Code (to allocate) | Error | Example |
|---|---|---|
| E-inout-place | an `inout` argument is not a mutable place | `advance(&v)` for a `val v`; `advance(&f())` |
| E-inout-amp | an `inout` argument is missing its `&` | `advance(p)` for a `var p parser` |
| E-inout-overlap | overlapping access in one call | `blit(&b, &b)`; `sepBy1(&p, "\|", p.alternative)` |
| E-inout-escape | an `inout` binding escapes | `w.Rand = &r`; `() => r.next()`; `bufio.NewWriter(&w)` |
| E-inout-receiver | an `inout`-receiver method is called on a non-place | `parser(…).advance()`; on a `val` |
| E-inout-position | `inout` where only a parameter may have it | a field, local, result or type argument (a parse error, if the grammar allows) |
| E0037 (extended, phase 4) | a value is used after it was sent | see 3.4 |

#### 3.2.5 Lean translation

Aeneas translates a Rust function that takes `&mut T` into a *forward* function, which
computes the result, and a *backward* function, which computes the final value of the
borrowed input. Current Aeneas fuses the two: the function returns its result together
with the updated input. Because `inout` borrows are second-class and never returned, every
GALA case is the simple one:

```
func f(inout x T, a A) R      ⟶      def f (x : T) (a : A) : M (R × T)
```

`M` is the prototype's monad, `EStateM Panic St`. With several `inout` parameters the
tuple grows: `M (R × T₁ × T₂)`. A call rebinds every lent place, as in Lean's `do`
notation with `let mut`.

```mermaid
flowchart LR
    subgraph GALA
      C1["var p = parser(...)"] --> C2["val t = p.advance()"] --> C3["p.pos used later"]
    end
    subgraph Lean
      L1["let mut p := { toks := … }"] --> L2["let (t, p') ← parser.advance p<br/>p := p'"] --> L3["… p.pos …"]
    end
    C1 -.-> L1
    C2 -.->|forward and backward fused:<br/>result and new value| L2
    C3 -.-> L3
```

**Worked example 1: an `inout` receiver.** The GALA code is `parser.advance` from 3.2.1.

```lean
structure parser where
  toks : Array Token
  pos  : Int64
  err  : Option Diagnostic

-- a value receiver: a pure forward function
def parser.peek (p : parser) : M Token := arrayGet p.toks p.pos

-- an inout receiver: forward and backward fused
def parser.advance (p : parser) : M (Token × parser) := do
  let mut p := p
  let t ← parser.peek p
  if !(t.Kind.isTokEOF) then
    p := { p with pos := p.pos + 1 }
  return (t, p)

def Parse (toks : Array Token) : M (Either Diagnostic Grammar) := do
  let mut p : parser := { toks := toks, pos := 0, err := none }
  let (g, p') ← grammar p
  p := p'
  match p.err with
  | some d => return .left d
  | none   => return .right g
```

**Worked example 2: field projection and two `inout` places.**

```gala
type front struct {
    var lex lexer
    var par parser
}

func step(inout l lexer, inout p parser) { … }

func (inout f front) pump() {
    step(&f.lex, &f.par)       // disjoint fields of one place: allowed
    f.lex.advance()            // auto-& on a field path
}
```

```lean
def front.pump (f : front) : M (Unit × front) := do
  let mut f := f
  let ((), l', p') ← step f.lex f.par
  f := { f with lex := l', par := p' }
  let ((), l'') ← lexer.advance f.lex
  f := { f with lex := l'' }
  return ((), f)
```

**Worked example 3: a loop.** `for p.ok() && p.acceptPunct(sep) { items = items.Append(item(&p)) }`
becomes a `while` in `do` notation. `let mut p` and `let mut items` are reassigned after
each call. This is the rule the prototype already uses for local `var`.

**Why the translation stays closure-free.** A Rust function that *returns* `&mut U` into
its argument needs a backward function that is a closure (`U → T`). That closure is what
makes Aeneas's output and proofs heavier. Leaving out borrowed results (a non-goal) means
every backward part is a plain returned value. Lean's own runtime then helps: a value
threaded this way has reference count 1, so `{ p with pos := … }` and `Array.set!` update
in place (see Prior art). The translated Lean is efficient as well as faithful.

### 3.3 Phase 3: non-escaping closures may capture `inout` places

galop's `builder` (13 functions), most of gala-tui's `inout-closure` group (29), and
captured local `var`s (106 functions) all share one shape. A closure that mutates the
caller's state is passed to a function that only calls it during the call:

```gala
rules.ZipWithIndex().ForEach((ri) => {
    b.ruleStart = b.ruleStart.Append(b.state(ri.V2, RuleStartState()))
})
```

This follows Swift's rule: a closure may capture an `inout` place if the closure is
**non-escaping**. A function parameter `f` is non-escaping when the callee only calls
`f`. It never stores, returns or captures `f`, and never passes it to a parameter that
escapes.

- **Inferred,** like the borrow summaries. It is written to package metadata and holds
  for std's `ForEach`, `Map`, `FoldLeft`, `Filter` and the rest.
- **Exclusive:** while the closure is live, the captured place is lent to it, so the call
  may not also lend the place `inout` (this is `sepBy1`'s problem).
- **`Sendable` parameters always escape.** A `Future` body runs later on another
  goroutine.

**Lean.** The closure becomes a computation in `StateT S M`, where `S` is the tuple of the
places it captures. The higher-order function runs in that monad:

```lean
let ((), b') ← (rules.zipWithIndex.forM fun ri => do
    let b ← get
    let (s, b) ← builder.state b ri.2 .RuleStartState
    set { b with ruleStart := b.ruleStart.push s }).run b
b := b'
```

std's higher-order functions need monad-polymorphic Lean models (`forM`, `mapM`,
`foldlM`). A GALA-defined higher-order function is translated generically over
`[Monad m] [MonadLiftT M m]` when it has a non-escaping function parameter (Q6).

### 3.4 Phase 4: transfer across `Sendable` boundaries

Today E0037 rejects this, although the program is race-free:

```gala
val buf = collection_mutable.ArrayOf(1, 2, 3)
buf.Append(4)
Future(() => buf.Size())     // GALA-E0037 today
// buf is never used again
```

Swift solved the same problem with region-based isolation and `sending`. The rule here
is: a captured `val` `x` of non-`Shareable` type is accepted at a `Sendable` boundary when
all of these hold:

1. **`x` is disconnected.** It was built in this function by constructors, literals,
   `&T{…}` or mutable-collection constructors, from arguments that are `Shareable` or
   themselves disconnected. Since then it has only been lent (phase 1 summaries). It was
   never stored, aliased, captured by another closure, or passed to Go.
2. **The closure holds the last use of `x`.** No path from the boundary call reaches
   another use of `x`.
3. **The closure is created once.** It is not created inside a loop that would send the
   same `x` twice.

A `var` capture is still rejected, because the binding slot itself is shared.

```mermaid
flowchart TD
    C[closure passed to a Sendable parameter<br/>captures val x] --> S{x Shareable?}
    S -->|yes| OK[accept, as today]
    S -->|no| D{x disconnected?<br/>fresh, only lent}
    D -->|no| E1[GALA-E0037, as today]
    D -->|yes| L{any use of x<br/>after the send?}
    L -->|yes| E2[GALA-E0037: x is used after it was sent<br/>secondary span at the later use]
    L -->|no| T[accept: ownership moves<br/>to the other goroutine]
```

The generated Go is unchanged: the closure captures `x` exactly as it does now. The check
only accepts more programs, and only ones that are race-free by construction. AGENTS.md
rule 8 forbids weakening checks, so this needs the maintainer's explicit approval, as
does the sending of non-`Shareable` `Future` results back to the awaiting goroutine (Q7).

### 3.5 Interop guarantees

An `inout` parameter promises three things for the duration of a call:

- **exclusive:** nothing else reads or writes the place;
- **non-escaping:** no reference to it outlives the call;
- **unaliased:** no second path to it exists.

These guarantees, and the Lean translation that relies on them, must hold when the caller
or the callee is Go. Go has no such rules. A `*T` can be stored, shared between
goroutines, or passed twice. So every crossing either **re-establishes** the guarantee
with a value copy, **proves** it from a checked fact, or **refuses**.

```mermaid
flowchart LR
    subgraph GO_SIDE[Go]
      GC[Go caller<br/>p *T, maybe aliased or retained]
      GF[Go function<br/>f p *T]
      GI[Go type implementing<br/>a GALA interface]
    end
    subgraph GALA_SIDE[GALA]
      W[exported wrapper<br/>copy-in, copy-out]
      IMPL[implementation<br/>inout x T: exclusive]
      PL[inout place / unique Ptr]
      AL{callee on the<br/>no-retain allow-list?}
      IF[interface with<br/>inout method]
    end
    GC -->|"Step(p)"| W
    W -->|"v := *p, then impl(&v)"| IMPL
    IMPL -->|"*p = v on normal return"| W
    W --> GC
    PL -->|"&x to a *T parameter"| AL
    AL -->|yes, verified in CI| GF
    AL -->|no| ERR[E-inout-escape<br/>phase 1: UNSUPPORTED go-escape]
    GI -.->|converted in GALA code:<br/>E-inout-foreign-impl| IF
    GC -.->|"interface value from Go:<br/>foreign, opaque"| IF
```

#### 3.5.1 Go calls GALA

An exported function `func Step(inout p Parser)` must be callable from Go as
`Step(p *Parser)`, the shape GO_INTEROP.MD promises: a GALA declaration *is* a Go
declaration, and GALA never renames. The method form, `func (inout p Parser) Advance()`,
must be a Go pointer-receiver method so that method sets and interfaces work (decision
Q8). Go may hand in a `p` that it has stored, that another goroutine holds, that a
callback will reach during the call, or that is passed twice: `Swap(p, p)`.

| Option | Guarantee | Go ergonomics | Cost | Verdict |
|---|---|---|---|---|
| **A. Copy-in/copy-out wrapper** | Restored for the body: it works on a local copy that only it can reach. Aliased arguments (`Swap(p, p)`) get deterministic Swift semantics: each is copied in, and they are copied out left to right, so the last write wins. | Idiomatic: the Go signature is the expected `*T`, and methods are pointer-receiver methods. | Two shallow struct copies per call from Go. None for GALA callers, in any package (Q13). | **Recommended** |
| B. Separate Go-facing signature (`func StepGo(p Parser) Parser`) | Holds, by value semantics. | Poor: a second name breaks "GALA never renames"; methods cannot be expressed; results become tuples. | No copies through a pointer, but a whole value is returned. | Rejected as the default |
| C. Documented contract only | **None**: unenforceable, and it contradicts the requirement. | Best | Zero | Rejected |
| D. Refuse to export `inout` (an error on an exported function or method with an `inout` parameter) | Holds trivially. | Blocks Go-facing libraries and Go-visible implementations of interfaces with `inout` methods (Q8). | Zero | Rejected: too restrictive |

**Recommended: option A.** This is the Swift definition of `inout`. Swift *specifies*
`inout` as copy-in/copy-out and treats passing by reference as an optimisation that is
valid only when exclusivity is proved. GALA proves exclusivity statically for GALA
callers, so it passes by reference there. For Go callers it cannot, so it copies.

How the code is generated:

```go
// Generated for: func (inout p Parser) Advance() Token
func (p *Parser) Advance() Token {          // the Go-visible symbol, unchanged name
    if p == nil { panic(/* existing Ptr entry-check message */) }
    v := *p                                 // copy in
    r := _gala_Parser_Advance(&v)           // body: exclusive local place
    *p = v                                  // copy out, on normal return only
    return r
}
func _gala_Parser_Advance(p *Parser) Token { /* the GALA body */ }
```

- **Who calls what:**
  - GALA callers in the same package call `_gala_…` directly. Identifiers starting with
    `_gala_` are reserved for generated code.
  - GALA callers in another package also call the body directly (decision Q13). It is
    exported under a reserved generated name, so only Go goes through the wrapper.
    Go exports only identifiers that begin with an upper-case letter, and `_gala_…` does
    not. So the cross-package body needs an exported spelling in the reserved family,
    which Q18 asks about.
  - Calls through an interface dispatch to the wrapper.
- **Panics:** the copy-out is skipped. A Go caller that recovers sees `*p` unchanged:
  all-or-nothing. Inside GALA no caller can observe a partial write anyway, because
  decision Q5 forbids mutating an `inout` place inside `Try` or recover. This is decided
  in Q14.
- **Callbacks:** if the body calls a Go callback that reads `*p` through its own alias,
  it sees the value from before the call. This is the one observable difference from
  plain pointer semantics, and it is exactly what makes the body translatable. It is
  documented on the wrapper's doc comment.
- **The copy is shallow.** A Go slice, map or pointer field still shares its target with
  whatever Go holds. Section 2.4's owned-slice rule therefore treats a value that
  arrived from Go as **not fresh**. The body still compiles and runs. Its slice fields
  translate only if the body replaces them with fresh slices first.
- **`New<T>` and `std.Immutable`:** unaffected. `inout` may replace a value whose fields
  are `val` (`p = p.Copy(...)`). Seen from Go, `*p = v` replaces the struct, including
  its `std.Immutable` fields. That matches GALA's model: the binding is reassigned, and
  no immutable field is mutated in place.
- **Concurrency:** Go writing `*p` from another goroutine during the call is a Go data
  race, outside GALA's guarantees. That is true of any Go function that takes a pointer.
- **Lean:** the wrapper is Go-only glue and is not translated. The translation covers
  `_gala_…`, whose precondition (exclusive, unaliased) the wrapper establishes.

**Cost.** A shallow copy of the struct's header words, twice per call from Go: for
gala-tui's `Buffer`, nine words. That is negligible next to the call. Large value structs
called in hot Go loops are the only case where it shows. Such an API can keep a `Ptr[T]`
parameter, which stays legal and is simply not translated.

#### 3.5.2 GALA calls Go

GALA passes a place to Go as `&x` to a `*T` parameter, or by calling a Go pointer-receiver
method on it (`b.WriteString(s)` on an `inout b strings.Builder`).

| Option | Sound? | Coverage | Stable? | Verdict |
|---|---|---|---|---|
| **1. Every Go call escapes** | Yes | Lowest | Yes | **The default.** Phase 1 already does this (`UNSUPPORTED go-escape`), and phase 2's E-inout-escape rejects passing an `inout` binding to Go. |
| 2. Go's escape analysis decides | Not on its own (see below) | High | No: it depends on the Go version | Rejected as the deciding rule. Used only to verify option 3. |
| **3. Allow-list of non-retaining Go functions** | Yes, relative to a verified list | Targeted | Yes, versioned | **Recommended relaxation** |

**Why gc's escape analysis cannot be the rule.** `go build -gcflags=-m` reports
`p does not escape` or `leaking param: p` (`leaking param content`, `… to result`).
These are not a contract:

- The facts change between Go releases and with inlining decisions. Whether a GALA
  program compiles must not depend on compiler internals.
- `go/types` and x/tools' SSA expose no escape facts, and writing our own escape
  analysis over Go SSA is a project in itself.
- "Does not escape" is about the heap. It misses retention through
  `uintptr(unsafe.Pointer(p))`, and it says nothing about results that alias the
  argument's *contents* unless `-m` reports a leak to the result. `bytes.Buffer.Bytes()`
  returns a slice of the receiver's storage.
- Dynamic calls (interfaces, function values) are always "escapes".

**The allow-list.** The list lives in the standard library's metadata, so it uses the same
resolution as any other package (AGENTS.md rule 2). Each entry names a Go function or
method and a parameter (or the receiver). It asserts two things:

- the callee does not retain the pointer or anything reachable from it after returning;
- no result aliases memory reachable from it.

An entry may also carry a Lean model, which the translation needs (3.5.5).

- **Verified in CI.** A test builds a stub that calls each entry with the pinned toolchain
  (`MODULE.bazel` pins Go 1.25.5) under `-gcflags=-m=2`. An entry fails the test when the
  parameter shows as leaking, to the heap or to a result. A Go upgrade that changes a
  verdict breaks the build, not a user program.
- **What it relaxes:** E-inout-escape is not reported for an allow-listed parameter.
  Phase 1 stops counting such calls as `go-escape`.
- **Initial scope:** about 30 entries chosen from the phase 1 escape reasons, starting
  with the most frequent `go-escape` callees. Candidates are `strings.Builder` and
  `bytes.Buffer` writer methods (not `Bytes()`), `fmt.Fprint*` with a `*strings.Builder`
  writer, `binary.Read`, `sort.Sort` on an owned slice adapter, and `json.Unmarshal`'s
  destination.
- **Users cannot extend the list (decision Q15).** Only std's CI-verified list exists. A
  user annotation would be an unchecked trust point, like `go_interop.Spawn`.
- **The workaround needs no new feature:** copy in and out by hand,
  `var tmp = x; goFn(&tmp); x = tmp`. Go may keep `&tmp`, but `x` stays exclusive.
- **Phase 1 relaxation path:** as a translator-only measure, phase 1 may also read the
  pinned compiler's `-m` output to count *coverage* (never to accept a program), so we
  learn which allow-list entries would pay off (decision Q17).

#### 3.5.3 Interfaces with `inout` methods

Decision Q8 allows `inout` method requirements in an interface. They lower to a Go
interface whose implementers have pointer-receiver methods. An interface value therefore
*holds a pointer*, and two interface values can hold the same one.

| Situation | Checked | Trusted |
|---|---|---|
| A **GALA** type implements the interface | Its `inout` method bodies, under the normal rules. Go dispatch reaches them through the 3.5.1 wrapper, so they are exclusive. | — |
| An interface value built in GALA from `&T(...)` or `&T{...}` (fresh) | Phase 1 tracks it like a unique `Ptr`. Calling an `inout` method lends the boxed value. It is translatable while the interface value is unique. | — |
| An interface value built from `&x` of a GALA `var` | It stores a pointer to `x`, so `x` is no longer unique. In phase 2 this is E-inout-escape if `x` is an `inout` binding. | — |
| A **Go-declared** type converted to the interface in GALA code | **E-inout-foreign-impl**: its methods are unchecked Go and may retain or alias the receiver. Types on the allow-list (methods marked non-retaining) are accepted. | The allow-list entries |
| An interface value that **arrives from Go** (a parameter of an exported function, the result of a Go call) | It is marked *foreign*. Calls compile and run with Go semantics, and the translation treats them as opaque Go calls (3.5.5). | That the Go implementation does what its Lean model says, if one exists |

GALA cannot stop Go code from implementing the interface and passing the value in. So the
guarantee is scoped: **`inout` method calls on interface values built in GALA from
GALA types are exclusive and translatable. Values of foreign origin are trusted, not
checked** (decision Q16). The rejected alternative was a compile error on every exported
function that accepts such an interface.

#### 3.5.4 Goroutines and Futures across Go code

Phase 4 transfer (3.4) has three interactions with Go:

- **A Go call is never a transfer.** Passing a disconnected value to Go makes it
  connected, unless the callee is allow-listed as non-retaining. Examples are a Go
  library that starts a goroutine, `go_interop.Spawn`, and a channel send through
  go_interop. The value can no longer be sent to a `Future`.
- **Values from Go are not disconnected.** A Go result may be retained by the library,
  for example in a registry. Go's escape analysis cannot tell a fresh result from a
  retained one: every returned pointer "escapes". So only allow-list entries marked
  `returns fresh` (such as `bytes.NewBuffer`, `strings.NewReader`) produce disconnected
  values. A GALA composite literal of a Go struct type (`strings.Builder{}`) is fresh.
- **`inout` never crosses a goroutine boundary.** The no-escape rule already forbids
  capturing an `inout` binding in a `Sendable` closure. A Go callee cannot receive it
  except through 3.5.2, and allow-listed callees do not retain it, so they do not start
  goroutines that keep it.

`go_interop.Spawn`, `SpawnWithRecover` and raw channels remain the unchecked zone that
CONCURRENCY_SAFETY.MD describes. Values passed there are treated as escaped.

#### 3.5.5 What stays trusted, and how Lean sees Go

| Trust point | Why it is trusted | Mitigation |
|---|---|---|
| The Go compiler and runtime | We run Go, we do not model it. | Out of scope, as in FORMAL_SEMANTICS.MD. |
| The no-retain allow-list | It is a claim about Go code. | Each entry is verified in CI against the pinned compiler's escape output; reviewed like code. |
| `returns fresh` entries | Escape analysis cannot see freshness. | Few entries, constructors only, each with a comment citing the source it was read from. |
| Lean models of Go APIs (the prelude) | A model can be wrong. | Each model is a small pure function, tested differentially (the model's result against a real Go call on generated inputs). |
| Go implementations of `inout` interfaces, and Go callers' concurrency | Go code is unchecked. | Foreign values are marked; the copy-in/copy-out wrapper confines the effect to before and after the call. |
| `unsafe`, reflection, cgo | They bypass the type system. | Untranslatable; never allow-listed. |

**How the Lean translation treats a Go call.** A Go call is translatable only when its
callee has a Lean model in the prelude. A model is a pure function over the Lean
representations (`strings.Builder` as `String`, `bytes.Buffer` as `ByteArray`, owned
`[]T` as `Array T`).

- An allow-listed call that lends an `inout` place or a unique pointer is translated like
  a GALA `inout` call. The model has type `T → M (R × T)`, and the caller rebinds the
  place.
- A call with no model is `UNSUPPORTED go-call <pkg.Func>` in `COVERAGE.md`, counted per
  callee. That count decides which models to write next.
- Callbacks that Go calls (a GALA lambda passed to Go) are translatable only if the model
  describes when Go calls them. `sort.Slice`'s comparator is an example. Otherwise they
  are unsupported.

**Error codes added by this section**, with numbers allocated when implemented:

| Code | Error |
|---|---|
| E-inout-escape (extended) | An `inout` binding is passed to a Go parameter or receiver that is not on the allow-list. The hint suggests the copy-in/copy-out pattern. |
| E-inout-foreign-impl | A Go-declared type is converted to an interface with `inout` method requirements. |

**Questions raised by this section.** Q13–Q17 are answered in [§7](#7-decisions). Q18 is
open.

| # | Question | Recommendation |
|---|---|---|
| Q13 | Should GALA callers in *another* package pay the wrapper's two copies, or should the implementation also be exported under a reserved name (for example `GALA_impl_…`) that GALA calls directly? | Pay the copies at first, and measure. A reserved exported name is visible to Go users and needs its own naming decision. |
| Q14 | On a panic, skip the copy-out (all-or-nothing, as proposed) or perform it (plain pointer semantics)? | Skip it. It cannot be observed from GALA (Q5), and Go callers get a cleaner contract. |
| Q15 | May users add allow-list entries (an annotation on a go_interop wrapper)? | Not in this proposal. If they may, it should be an explicit, greppable trust marker, as `go_interop.Spawn` is. |
| Q16 | Interface values with `inout` methods that arrive from Go: trusted and marked foreign (proposed), or rejected at exported boundaries? | Trusted and marked. Rejecting them would make such interfaces unusable from Go. |
| Q17 | May the phase 1 translator use the pinned compiler's `-m` escape output, for coverage measurement only? | Yes. It never decides whether a program compiles. |
| Q18 | *Open.* Go cannot see a lower-case `_gala_…` name from another package. How is the cross-package body from Q13 spelled? | Reserve one exported prefix, for example `GALA_` (`GALA_Parser_Advance`), and extend E0097 to cover it. No GALA identifier in std, the examples, galop, gala-tui, gala-team or gala-acp starts with `GALA_`; only environment-variable strings do. Go users see these names, so each one's doc comment says it is generated and names the method to call instead (`Advance`). |

## 4. Migration and breaking-change analysis

**Opt-in, not mandatory.** Every phase adds capability without changing what existing
programs mean.

| Phase | Language change | Breaks existing code? | Required edits |
|---|---|---|---|
| 1 | none (analysis only) | no | 0 |
| 2 | the keyword `inout` | Only if reserved, and only identifiers named `inout`. The scan found **0** in std, galop, the examples, gala-tui, gala-team and gala-acp. | 0 |
| 3 | none (inferred) | no | 0 |
| 4 | E0037 accepts more | no | 0 |

**What a mandatory design would cost.** This is option C: `Ptr`/`*T` parameters
non-escaping by default. At least 75 functions would stop compiling: the 44 escaping
ones, std's 31 persistent-node internals, and every function-typed field that takes a
pointer. Their callers and fluent builders would follow. `*T` is also Go's pointer, so
non-escaping by default would break Go interop. Not recommended.

**Opting in, to gain translatability:**

| Repository | What changes | API impact |
|---|---|---|
| galop | About 90 receivers on 8 private types, `Ptr[parser]` → `inout parser` and so on. Receiver call sites are unchanged. Explicit argument sites gain `&`. `sepBy1` takes `item func(inout parser) T`. `walker` holds `rng` and `usage` by value. | None: all the types are unexported. |
| gala-tui | 125 mentions of `*Buffer` and `*HitRegistry` in 15 non-test files, including the public `Custom(minW, minH, render func(Rect, *Buffer, *HitRegistry))` and every widget's render signature. | **Breaking for gala-tui users** (custom widgets). It would ship as a gala-tui minor release with the maintainer's approval, under the "confirm every breaking change" rule. |
| std | Optional: `json`'s private encoder and decoder. `StringBuilder`'s fluent `Ptr` API stays. | None. |
| gala-team, gala-acp | No pointer functions. | None. |

**A safe codemod.** Phase 1's summaries say exactly which `Ptr` receivers and parameters
are *borrowed*. A `gala fix` rewrite, `Ptr[T]` → `inout T` plus `&` at argument sites, is
safe for exactly those, and it would never produce an E-inout error. That makes migration
mechanical.

**IDE and LSP impact**

- **Grammar:** `inout` in parameter, receiver and function-type-parameter positions, in
  `gala.g4`. galop regenerates the native parser. Then run the `gala-ide-sync` skill for
  the IntelliJ plugin's lexer and keyword lists and for the LSP keyword data.
- **LSP:**
  - hover shows `inout` in signatures;
  - signature help marks the `inout` parameters;
  - completion offers `inout` in parameter position;
  - diagnostics for the new codes;
  - code actions "add `&`" and "convert `Ptr` receiver to `inout`" (the latter driven by
    the phase 1 summaries);
  - optionally, a phase 1 hover line for uniqueness.
- **Docs:** `docs/GALA.MD` (parameters, receivers, pointers), `docs/TYPE_INFERENCE.MD`
  (function types), `docs/CONCURRENCY_SAFETY.MD` (phase 4), the website mirror, and the
  error-code pages.

## 5. Prior art

| System | Mechanism | What GALA takes | What GALA leaves |
|---|---|---|---|
| [Rust](https://doc.rust-lang.org/book/ch04-00-understanding-ownership.html) | Ownership, `&`/`&mut` borrows with lifetimes, a borrow checker | Exclusive mutable access; the escape table | Lifetimes, borrowed results, moves of every value |
| [Aeneas](https://github.com/AeneasVerif/aeneas) ([ICFP 2022](https://dl.acm.org/doi/10.1145/3547647), [symbolic borrow checking, ICFP 2024](https://arxiv.org/abs/2404.02680)) | Translates safe Rust to pure Lean with forward and backward functions; current versions fuse them | `T → M (R × T)` for every `inout` | Backward *closures*, which only borrowed results need |
| [Swift `inout`](https://docs.swift.org/swift-book/documentation/the-swift-programming-language/functions/#In-Out-Parameters), [exclusivity (SE-0176)](https://github.com/swiftlang/swift-evolution/blob/main/proposals/0176-enforce-exclusive-access-to-memory.md), [ownership manifesto](https://github.com/swiftlang/swift/blob/main/docs/OwnershipManifesto.md) | Second-class `inout` with `&` at the call site; the Law of Exclusivity; non-escaping closures may capture `inout` | The keyword, `&`, exclusivity, the closure rule (phase 3) | Dynamic exclusivity checks (GALA's are all static), `_modify` accessors |
| [Swift region isolation (SE-0414)](https://github.com/swiftlang/swift-evolution/blob/main/proposals/0414-region-based-isolation.md), [`sending` (SE-0430)](https://github.com/swiftlang/swift-evolution/blob/main/proposals/0430-transferring-parameters-and-results.md) | Non-`Sendable` values cross isolation boundaries when their region is disconnected and unused afterwards | Phase 4, nearly verbatim | The explicit `sending` marker, for now (Q7) |
| [Hylo](https://www.hylo-lang.org) ([mutable value semantics, JOT 2022](https://www.jot.fm/issues/issue_2022_02/article2.pdf)) | Every type has value semantics; parameter conventions `let`/`inout`/`sink`/`set`; projections instead of references | `inout` that reassigns an immutable struct; no references in APIs | `sink`/`set` conventions and subscripts/projections (Q10) |
| [Vale](https://vale.dev) ([regions](https://verdagon.dev/blog/zero-cost-memory-safety-regions-overview)) | Generational references plus region borrowing; immutable regions skip checks | The idea that freezing a region (`Shareable`) is cheap | Runtime generation checks: GALA keeps Go's GC |
| [Koka FBIP](https://www.microsoft.com/en-us/research/publication/fp2-fully-in-place-functional-programming/) ([Perceus, PLDI 2021](https://www.microsoft.com/en-us/research/publication/perceus-garbage-free-reference-counting-with-reuse/)) | Precise reference counting; reuse when unique; `fip` functions checked to run in place | Uniqueness as an *inferred* property, as in phase 1 | Reference counting in the target: Go keeps its GC |
| [Lean 4](https://lean-lang.org) ([Counting Immutable Beans, IFL 2019](https://arxiv.org/abs/1908.05647)) | Reference counting with destructive update when the count is 1 (`Array.set!`, structure update) | The reason the translation is efficient: a GALA-unique value is RC = 1 in Lean | Nothing to change: the translation relies on it |

## 6. Phased plan

The estimates assume one maintainer working with AI agents, as in FORMAL_SEMANTICS.MD.

```mermaid
gantt
    dateFormat  YYYY-MM-DD
    axisFormat  W%W
    section Phase 1 (no language change)
    Borrow-summary analysis on Core JSON    :p1a, 2026-11-23, 7d
    gala2lean unique Ptr as value            :p1b, after p1a, 5d
    COVERAGE.md escape reasons               :p1c, after p1a, 2d
    T8 heap model + simulation (stretch)     :p1d, after p1b, 10d
    section Phase 2 (inout, opt-in)
    Grammar + transformer lowering to *T     :p2a, after p1b, 7d
    Checks place overlap escape and codes    :p2b, after p2a, 7d
    Docs, error pages, IDE/LSP sync          :p2c, after p2b, 4d
    gala2lean fused forward and backward     :p2d, after p2a, 4d
    section Phase 3
    Non-escaping params + StateT closures    :p3, after p2d, 12d
    section Phase 4
    Sendable transfer (E0037 extension)      :p4, after p2b, 7d
    section Adoption
    gala fix codemod + galop migration       :a1, after p2c, 5d
```

| Phase | Deliverable | Effort | Done when |
|---|---|---|---|
| 1 | Borrow summaries and freshness, written to package metadata; gala2lean translates unique `Ptr`s as values; `COVERAGE.md` lists escape reasons | 2–3 weeks | galop's `lexer` and `configSet` round-trip through Lean and agree with Go on their tests; coverage up about 3 points |
| 1b (stretch) | A galaspec heap model and theorem T8 | 2 weeks | T8 proved for S1 plus `Ptr` |
| 2 | `inout` syntax, lowering, the five checks and their codes, docs, IDE/LSP; interop (3.5): copy-in/copy-out wrappers, the allow-list and its CI escape check, E-inout-foreign-impl | 4–5 weeks | The generated Go for existing examples is byte-identical; new examples cover each code; gala-tui's `Custom` can be written with `inout` |
| 3 | Inferred non-escaping function parameters; `StateT` closure translation; monadic std models | 2–3 weeks | galop's `builder` translates; coverage about 76% |
| 4 | Sendable transfer | 1–2 weeks | The `ArrayOf` example above compiles; a use after the send is E0037 with a secondary span |
| Adoption | `gala fix` codemod; galop migrated; gala-tui only with approval | 1 week, plus gala-tui | galop at about 90% translatable |

## 7. Decisions

The maintainer answered the open questions below on 2026-11-13. Phase 1 is approved and in progress. Answers for later phases are provisional and will be confirmed once phase 1's escape reasons are in `COVERAGE.md`.

| # | Decision |
|---|---|
| Q1 | **Build phase 1 now.** Decide phase 2 after phase 1's data is in. |
| Q2 | Keyword **`inout`, reserved**. |
| Q3 | **`&` marks `inout` arguments**; receivers stay implicit. |
| Q4 | Calling a mutating method on a `val` is a compile error, **for `inout` receivers and also for today's `Ptr` receivers** (no more silent `AddrOfCopy` mutation of a hidden copy). The `Ptr` part is a breaking change, approved and scheduled independently of phase 2. |
| Q5 | Mutating an `inout` place inside a `Try`/recover region is a compile error, at least at first. |
| Q6 | Non-escaping function parameters are **inferred** and recorded in package metadata. |
| Q7 | A transfer across a `Sendable` boundary is **inferred from last use**. Transfer of `Future` results is decided separately. |
| Q8 | **Interfaces may declare `inout` method requirements**, lowered to pointer-receiver method-set requirements in Go. |
| Q9 | **Std's fluent builders (`StringBuilder`, …) will be redesigned** in the `inout`/value style when phase 2 lands. Breaking; approved. |
| Q10 | Projections (`inout` pattern bindings, `inout` locals) are out of scope. |
| Q11 | gala-tui's public `*Buffer` API stays as is. Ask again after phase 2. |
| Q12 | A galaspec heap model and theorem T8 are a **stretch goal of phase 1**. |
| Q13 | GALA callers in other packages **call the body directly**. It is exported under a reserved generated name in the `_gala_` family, consistent with [GALA-E0097](errors/GALA-E0097.md). Only Go goes through the copy-in/copy-out wrapper. The exported spelling is still open (Q18 in [3.5.5](#355-what-stays-trusted-and-how-lean-sees-go)). |
| Q14 | On a panic the **copy-back is skipped**: a Go caller that recovers sees its value unchanged (all-or-nothing). |
| Q15 | **No user allow-list entries** for now. Only std's list, verified in CI. |
| Q16 | Interface values with `inout` methods that arrive from Go are **trusted and marked foreign**. The Lean translation treats calls on them as opaque Go calls. |
| Q17 | Phase 1 **may read the pinned compiler's escape output to measure coverage**, never to decide whether a program compiles. |

## 8. Open questions for the maintainer (answered above)

| # | Question | Recommendation |
|---|---|---|
| Q1 | Is phase 1 alone enough for now? It gives most of the measured gain with no language change. | Yes, start there. Decide on phase 2 once phase 1's escape reasons are in `COVERAGE.md`. |
| Q2 | Keyword: `inout`, `mut` or `ref`? Reserved or contextual? | `inout`, reserved: 0 identifiers named `inout` exist, and a reserved word keeps the grammar and IDE simpler. |
| Q3 | Mark `inout` arguments with `&` (Swift), or leave them implicit? | `&` for arguments, implicit for receivers, as `Ptr` receivers are today. |
| Q4 | Today a `Ptr`-receiver call on a `val` mutates a hidden copy (`AddrOfCopy`). Should `inout` receivers reject it, as proposed, while `Ptr` keeps the current behaviour? | Reject for `inout`. Leave `Ptr` alone. Changing it would be breaking. |
| Q5 | When a panic is caught after a partial write: thread the place through the result and reject `inout` mutation under `Try`/`recover` (proposed), or keep live places in the monad's state? | Reject first. Revisit if real code needs it. |
| Q6 | Non-escaping function parameters: inferred (proposed) or declared, with Swift's `@escaping` default inverted? | Inferred, in package metadata, like `Ptr` non-nullness. |
| Q7 | Sendable transfer: inferred last use (proposed), or an explicit `sending`/`move` marker? Should a non-`Shareable` `Future[T]` result transfer back too? | Inferred for captures. Decide on results separately. |
| Q8 | May an interface declare a method `inout` (a mutating method requirement)? | Not in phase 2. Go interfaces cannot express it. |
| Q9 | Fluent builders that return `Ptr[Self]` (`StringBuilder`) cannot become `inout`. Keep them as `Ptr`, untranslated? | Keep them. Model `StringBuilder` in the Lean prelude. |
| Q10 | Hylo-style projections: `inout` pattern bindings, or `inout` locals (`inout l = &p.lex`)? | Out of scope. They need borrowed results, the one thing that brings back backward closures. |
| Q11 | Migrating gala-tui's public `*Buffer` API to `inout Buffer` breaks gala-tui users. Is it worth it? | Only after phase 2 is stable, and only with your approval. |
| Q12 | Should galaspec gain a heap model for `Ptr`, so that T8 (uniqueness implies the two semantics agree) can be proved? | Yes, as a stretch goal for phase 1. Without it, phase 1's soundness rests on the argument in 3.1 and on differential testing. |
