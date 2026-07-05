# `bind` / `also` — Monadic Binding Notation

**Status:** Implemented.

**Runnable verification examples** (all pass under `bazel test //examples/...`):

- [`examples/bind_notation.gala`](../examples/bind_notation.gala) — sequential `bind` over `Try` (the "graph" case: an early value reused on the final line).
- [`examples/bind_notation_also.gala`](../examples/bind_notation_also.gala) — `also` over `Option` (fail-fast fallback).
- [`examples/bind_notation_validated.gala`](../examples/bind_notation_validated.gala) — `also` over `Validated`, **accumulating** all errors.
- [`examples/bind_notation_future.gala`](../examples/bind_notation_future.gala) — `also` over `Future`, running clauses **concurrently**.
- [`examples/bind_notation_user.gala`](../examples/bind_notation_user.gala) — a **user-defined** monad (`Step`) with no relationship to `std`, proving extensibility (§7).
- [`examples/bind_notation_desugared.gala`](../examples/bind_notation_desugared.gala) / [`examples/bind_notation_user_monad.gala`](../examples/bind_notation_user_monad.gala) — the hand-written `FlatMap` chains the notation lowers to, kept as golden references.

---

## 1. Motivation

GALA already has monadic error handling — `Option`, `Try`, `Either`, `Future` — with
`Map`/`FlatMap` combinators. Those are excellent when the computation is a single
linear pipeline. They fall down on two shapes that dominate real code:

1. **The graph shape.** When a later step needs a value from *two steps back*, a
   `FlatMap` chain has to nest, because each intermediate value is trapped inside a
   closure:

   ```gala
   // "the receipt needs both the order AND the payment"
   fetchOrder(id).FlatMap((o) =>
       validateOrder(o).FlatMap((valid) =>
           chargePayment(valid).FlatMap((payment) =>
               Success(Receipt(o.Id, payment)))))   // o survives only via nesting
   ```

   The indentation deepens with every cross-reference, and the explicit `[U]` type
   argument often has to be repeated at each link because inference cannot always flow
   the accumulator type through the lambda.

2. **The independence shape.** When two fallible steps do *not* depend on each other,
   a `FlatMap` chain still forces them to run sequentially and short-circuit on the
   first error — you cannot run them concurrently, nor accumulate both errors.

Three surfaces were considered and rejected for GALA before landing here:

| Surface | Why not |
|---------|---------|
| Scala **for-comprehension** (`x <- e; yield`) | `<-`/`yield` ceremony; strictly sequential monadic bind; homogeneous-monad-only (needs monad transformers to mix); `withFilter` guard gotcha. |
| Rust **`?`** / Zig **`try`** | Imperative — a non-local return that hijacks the *enclosing function*. Not an expression; a middle slice of a `?`-chain cannot be factored into a helper without changing the function's return type. |
| OCaml **`let*` / `and*`** (the closest fit) | Perfect *semantics*, but the `*` spelling collides with GALA's pointer-deref `*`, and a `let`/`val` that secretly short-circuits reads as ordinary binding. |

`bind` / `also` keeps OCaml's semantics (see §8, Prior Art) with a keyword surface that
telegraphs fallibility, avoids every symbol collision, and reads like GALA's existing
declarations.

---

## 2. Surface syntax

```gala
func processOrder(id int) Try[Receipt] {
    bind o = fetchOrder(id)
    bind valid = validateOrder(o)
    bind payment = chargePayment(valid)
    Success(Receipt(o.Id, payment))   // trailing expression is the block's value
}
```

- **`bind name = expr`** — sequential monadic bind. `expr` must be of the block's
  bindable type `M[A]`; `name : A` is a normal **immutable `val`**, in scope for the
  rest of the block. On the short-circuit variant (`Failure`/`None`/`Left`/`Invalid`)
  the whole block evaluates to that variant.
- **`also name = expr`** — an *independent* bind. A leading `bind` followed by one or
  more `also` forms one **product group** (see §4).
- **Legality.** `bind`/`also` are legal only inside a block whose result type is a
  bindable monad `M` (§7). Outside such a block they are a compile error — so there is
  no hidden, function-level control flow.
- **Trailing value.** The block's final expression is its result and must already be
  `M[R]` — write the constructor explicitly (`Success(...)` / `Some(...)` /
  `Right(...)` / `Valid(...)` / your own). It is transformed with `M[R]` pushed as the
  expected type, so those constructors infer their type arguments and you rarely need
  to spell them out.

Bound names are ordinary immutable `val`s. There is no `bind`-specific mutability: a
`bind`/`also` name behaves exactly like a `val name = …` local, including single-unwrap
reads.

---

## 3. Sequential semantics — lowers to `FlatMap`

The block in §2 lowers mechanically to:

```gala
func processOrder(id int) Try[Receipt] =
    fetchOrder(id).FlatMap((o) =>
    validateOrder(o).FlatMap((valid) =>
    chargePayment(valid).FlatMap((payment) =>
    Success(Receipt(o.Id, payment)))))
```

This is verified in [`examples/bind_notation.gala`](../examples/bind_notation.gala):
`processOrder(1)` yields `ok: order=1 charged=100`, and `processOrder(0)` short-circuits
at the first `bind` to `err: no order 0`. The hand-written chain it lowers to is kept as
a golden in [`examples/bind_notation_desugared.gala`](../examples/bind_notation_desugared.gala).

Properties preserved:

- **Named bindings stay in scope** — the graph shape is expressed flat, no nesting.
- **Referential transparency** — the block is an ordinary expression of type `M[R]`;
  it can be named, returned, or passed. It is *not* `?`-style imperative control flow.
- **No type-argument tax** — the lowering carries the accumulator type directly, so
  the per-link `.FlatMap[U]` annotation disappears from the surface.

---

## 4. Independent semantics — `also` uses `Zip` when the monad provides it

A `bind` + `also` group of arity `N` (2..10) lowers to `M`'s `Zip{N}` **when `M`
defines that method**, threading every clause through one `FlatMap` continuation:

```gala
bind name  = validateName(r.Name)
also email = validateEmail(r.Email)
also age   = validateAge(r.Age)
Valid[string, Form](Form(name, email, age))
```

lowers to (conceptually):

```gala
Zip3(validateName(r.Name), validateEmail(r.Email), validateAge(r.Age))
    .FlatMap((t) => Valid[string, Form](Form(t.V1, t.V2, t.V3)))
```

The `Zip` is where independence pays off, and its behavior is **per type**:

| Type | `Zip{N}`? | `also` behavior |
|------|-----------|-----------------|
| `Future` (in `concurrent`) | yes | runs the group **concurrently** — structured concurrency for free |
| `Validated` (in `validation`) | yes | **accumulates all errors** instead of stopping at the first |
| `Try` / `Option` / `Either` (in `std`) | no | falls back to the sequential `FlatMap` chain — fail-fast on the first failure; grouping still documents independence |

**Fallback.** When `M` has no `Zip{N}` — a lone `bind`, or a fail-fast `std` monad —
the group lowers to the same nested `FlatMap` chain as §3. For a fail-fast monad the two
lowerings are observably identical, so the fallback loses nothing. `also` is therefore
always legal; supplying a `Zip{N}` only changes *how* the independent clauses combine.

**Independence rule.** `also` bindings may not reference each other or the `bind` in
their group — that non-dependence is exactly what makes the group applicative. If a later
clause needs an earlier value, use `bind` (sequential) instead.

---

## 5. Mixing monads

A `bind`/`also` clause's monad must match the block's monad. Binding an `Option` inside a
`Try` block, for example, is rejected with a clear diagnostic
(`cannot bind a Option inside a Try block (heterogeneous bind is not supported)`).

Convert at the **call site** so every clause has the block's type — e.g. turn an
`Option` into a `Try` with a small helper before binding it:

```gala
func toTry[T any](o Option[T], err error) Try[T] = o match {
    case Some(v) => Success(v)
    case None()  => Failure[T](err)
}

func buildReceipt(id int) Try[Receipt] {
    bind cfg  = loadConfig()                                        // Try[Config]
    bind user = toTry(findUser(id), NoSuchElementError(Message = "no user"))  // Option -> Try first
    Success(assemble(cfg, user))
}
```

The conversion is explicit and local, which keeps the block homogeneous and the error
type visible — no implicit widening, and no hidden monad-transformer machinery.

---

## 6. Type-safety rules

1. `bind`/`also` are rejected unless the enclosing block's result type is a known,
   bindable `M` (a type exposing `FlatMap`, §7).
2. A `bind`/`also` whose expression is not that same `M` is a compile error
   (heterogeneous bind is not supported — convert at the call site, §5).
3. `also` must directly follow a `bind` or another `also`; an `also` that does not is a
   compile error.
4. The trailing expression must be `M[R]` (write the constructor — it is transformed
   with `M[R]` as the expected type).

These keep the notation from degrading into untyped, `?`-style control flow.

---

## 7. Extending `bind`/`also` to your own monad

Extensibility is a first-class requirement, not an afterthought: **the notation works
over any user-defined monad exactly as it does over the standard library, and the
transpiler special-cases no type.** This upholds the project's standing rule that `std`
gets no special treatment — `Try`/`Option`/`Either`/`Future`/`Validated` are resolved
through the *same* structural mechanism a third-party monad is.

Go generics cannot express a higher-kinded `Bindable[F[_]]` interface, so GALA does not
try to. Instead the transpiler resolves the lowering **structurally, on the block's
concrete type `M`, at transpile time** — before Go ever sees it. It looks up `M`'s
metadata and emits `M`'s own `FlatMap` (as the free-function form `M_FlatMap`, with no
package qualifier for a local type).

Verified end-to-end in [`examples/bind_notation_user.gala`](../examples/bind_notation_user.gala):
a user-defined sealed `Step` monad, with no relationship to `std`, lowers through its own
`FlatMap` identically to `Try`:

```gala
sealed type Step[T any] {
    case Go(Value T)
    case Stop(Reason string)
}

// The only method `bind` requires: FlatMap. The short-circuit lives inside it.
func (s Step[T]) FlatMap[U any](f func(T) Step[U]) Step[U] = s match {
    case Go(v)   => f(v)
    case Stop(r) => Stop[U](r)
}

func pipeline(n int) Step[int] {
    bind a = start(n)
    bind b = twice(a)
    Go(a + b)              // `a` still in scope; a Stop anywhere short-circuits
}
```

### 7.1 The minimal contract

A type `M[T]` becomes bindable by providing **one** method:

```gala
func (m M[T]) FlatMap[U any](f func(T) M[U]) M[U]
```

That alone enables `bind` **and** sequential `also` (via the §4 fallback). The
short-circuit/empty behavior lives entirely inside the author's `FlatMap`, so the
lowering never inspects — and never hardcodes — which variant is the "zero". A type with
no `FlatMap` gives a clear compiler error naming the method it must define.

| Feature | Needs |
|---------|-------|
| `bind` | `FlatMap` (required) |
| `also` (sequential, fail-fast) | `FlatMap` — nothing more |
| `also` (concurrent / accumulating) | a `Zip{N}` method for the group's arity `N` (2..10) |

So the floor is `FlatMap`; adding `Zip2`…`Zip{N}` unlocks the richer applicative
behavior (concurrency, error accumulation) for `also` groups of that arity. A brand-new
user monad gets `bind` *and* sequential `also` for free.

### 7.2 The `Zip{N}` shape

To make `also` groups of arity `N` combine independently, define a method whose receiver
starts the group and whose parameters are the remaining clauses, returning `M` of a std
`Tuple{N}`:

```gala
// enables `also` groups of arity 3 to combine via this method
func (a M[A0]) Zip3[A1, A2](b M[A1], c M[A2]) M[Tuple3[A0, A1, A2]]
```

The lowering calls `Zip{N}` to build `M[Tuple{N}]`, then `FlatMap`s once, binding each
`val` to a tuple field (`V1`, `V2`, …) before the continuation. `Validated`'s `Zip`
pre-collects every clause's errors (accumulation); `Future`'s starts every clause before
joining (concurrency). `std`'s own monads are looked up the same way — there is no
built-in list.

### 7.3 Laws are the author's contract

The lowering is sound only if `M` obeys the monad laws. The transpiler cannot verify
them, so they are the author's responsibility (a `test` helper that checks them on
representative values is recommended):

- **Left identity:** `Pure(a).FlatMap(f)` ≡ `f(a)`
- **Right identity:** `m.FlatMap(Pure)` ≡ `m`
- **Associativity:** `m.FlatMap(f).FlatMap(g)` ≡ `m.FlatMap((x) => f(x).FlatMap(g))`

A custom `Zip{N}` used for `also` must additionally agree with the sequential fallback up
to the effect it adds (ordering for concurrency, error set for accumulation).

### 7.4 Conformance checklist for a new monad

1. Define `M[T]` (typically a sealed type).
2. Implement `FlatMap[U](f func(T) M[U]) M[U]` obeying the laws — `bind` and sequential
   `also` now work.
3. *(optional)* Implement `Zip{N}` for the arities you want `also` to combine
   concurrently or accumulatively.

---

## 8. Prior art

| Language | Construct | Relationship |
|----------|-----------|--------------|
| OCaml | `let* x = e in …`, `and*` | Direct model. `bind` = `let*`, `also` = `and*`. Same lowering (`bind`/`product`), resolved structurally instead of via a registered `let` operator. |
| Haskell | `do { x <- e; … }` | Same monadic denotation; `do` has no applicative-independence keyword equivalent to `also`. |
| F# | computation expressions `let!` / `and!` | `let!`≈`bind`, `and!`≈`also`. |
| Scala | for-comprehension | Same flatMap denotation; rejected surface (see §1). |
| Gleam | `use x <- e` | Callback-lowering, single-value. |
| Rust / Zig | `?` / `try` | Imperative escape hatch; complementary, not equivalent (see §1). |

---

## 9. Grammar

`bind`/`also` are declarations, modeled on the existing
`valDeclaration: 'val' (tuplePattern | identifierList) (type)? '=' expressionList;` in
`internal/parser/grammar/gala.g4` (the ANTLR source; the generated `*.go` is never
hand-edited):

```antlr
declaration
    : valDeclaration
    | varDeclaration
    | bindDeclaration      // bind name (type)? = expr
    | alsoDeclaration      // also name (type)? = expr
    | ...
    ;

bindDeclaration: BIND identifier (type)? '=' expression;
alsoDeclaration: ALSO identifier (type)? '=' expression;

BIND: 'bind';
ALSO: 'also';
```

`also` is only well-formed immediately following a `bind`/`also`; the transformer (not
the grammar) enforces that, to give a good error message.

---

## 10. Implementation

- **Grammar:** `BIND`/`ALSO` tokens and `bindDeclaration`/`alsoDeclaration` in
  `internal/parser/grammar/gala.g4`.
- **Lowering:** `internal/transpiler/transformer/bind.go` — collects a `bind`+`also`
  group, checks bindability structurally (`FlatMap` in the type's metadata), and emits
  either the nested `FlatMap` chain or `Zip{N}(...).FlatMap(...)`. Each bound name is
  registered as a `val` so reads unwrap exactly once. No type is special-cased.
- **Standard library:** `Zip{N}` on `Future` (`concurrent/`) and on the new
  `Validated[E, A]` accumulating type (`validation/`). `Try`/`Option`/`Either` keep only
  `Map`/`FlatMap`/`Filter` and use the sequential fallback for `also`.
- **Tests:** `internal/transpiler/transformer/bind_test.go` (golden lowerings, user
  monad, non-monad rejection, `also` fallback, `also` Zip) plus the `examples/` programs
  listed at the top of this doc.

---

## 11. Resolved design decisions

- **One `also` keyword, semantics chosen by the type.** GALA exposes a single `also`; it
  does *not* mirror OCaml's `and*`/`and+` split. The applicative-vs-monadic distinction
  is academic to most users, so it is hidden: `also` uses the block type's `Zip{N}` when
  present, and each type defines what that means (concurrent for `Future`, accumulating
  for `Validated`, sequential short-circuit otherwise). See §4.
- **`Validated` is a distinct sealed type.** Error accumulation lives in
  `validation`'s `Validated[E, A]` (`Valid`/`Invalid`) with a combining `E`, kept
  separate from `Either`'s fail-fast semantics — never an overloaded second behavior on
  `Either`.
- **`Future` concurrency is opt-in via `also`.** Sequential `bind`s on a `Future` run
  sequentially; only an `also` group runs concurrently. Concurrency is always visible in
  the source — the transpiler never auto-parallelizes consecutive `bind`s.
- **No heterogeneous bind, no auto-lift of plain trailing values.** A clause must be the
  block's own monad (convert at the call site, §5), and the trailing expression must be
  `M[R]` (write the constructor, §2). Both keep the generated Go concrete and the control
  flow honest.
