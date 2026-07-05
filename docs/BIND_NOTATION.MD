# `bind` / `also` — Monadic Binding Notation

**Status:** Design proposal — not yet implemented.
**Verification (passing today, against the real transpiler):**
- [`examples/bind_notation_desugared.gala`](../examples/bind_notation_desugared.gala) — the runtime semantics this notation desugars to, over `std`'s `Try`.
- [`examples/bind_notation_user_monad.gala`](../examples/bind_notation_user_monad.gala) — the same desugaring over a **user-defined** monad with no relationship to `std`, proving extensibility (§7).

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
               createReceipt(o, payment))))   // o survives only via nesting
   ```

   The indentation deepens with every cross-reference, and the explicit `[U]` type
   argument must be repeated at each link because inference cannot always flow the
   accumulator type through the lambda.

2. **The independence shape.** When two fallible steps do *not* depend on each other,
   a monadic chain still forces them to run sequentially and short-circuit on the
   first error — you cannot run them concurrently, nor accumulate both errors.

Three surfaces were considered and rejected for GALA before landing here:

| Surface | Why not |
|---------|---------|
| Scala **for-comprehension** (`x <- e; yield`) | `<-`/`yield` ceremony; strictly sequential monadic bind; homogeneous-monad-only (needs monad transformers to mix); `withFilter` guard gotcha. |
| Rust **`?`** / Zig **`try`** | Imperative — a non-local return that hijacks the *enclosing function*. Not an expression; a middle slice of a `?`-chain cannot be factored into a helper without changing the function's return type. |
| OCaml **`let*` / `and*`** (the closest fit) | Perfect *semantics*, but the `*` spelling collides with GALA's pointer-deref `*`, and a `let`/`val` that secretly short-circuits reads as ordinary binding. |

`bind` / `also` keeps OCaml's semantics (see §9, Prior Art) with a keyword surface that
telegraphs fallibility, avoids every symbol collision, and reads like GALA's existing
declarations.

---

## 2. Surface syntax

```gala
func processOrder(id int) Try[Receipt] = {
    bind o       = fetchOrder(id)
    bind valid   = validateOrder(o)
    bind payment = chargePayment(valid)
    Receipt(o.Id, payment)          // trailing expression is the block's value
}
```

- **`bind name = expr`** — sequential monadic bind. `expr` must be of the block's
  bindable type `M[A]`; `name : A` is a normal immutable local, in scope for the rest
  of the block. On the short-circuit variant (`Failure`/`None`/`Left`) the whole block
  evaluates to that variant.
- **`also name = expr`** — an *independent* bind. A leading `bind` followed by one or
  more `also` forms one **product group** (see §4).
- **Legality.** `bind`/`also` are legal only inside a block whose result type is a
  bindable monad `M` (§7). Outside such a block they are a compile error — so there is
  no hidden, function-level control flow.
- **Trailing value.** The block's final expression is its result. If it is already
  `M[R]` it is used as-is; if it is a plain `R` it is auto-lifted via `M`'s `Pure`
  (`Success` / `Some` / `Right`). Both forms below are equivalent:

  ```gala
  Receipt(o.Id, payment)            // plain -> auto-lifted
  Success(Receipt(o.Id, payment))   // already M[R] -> used directly
  ```

---

## 3. Sequential semantics — desugars to `FlatMap`

The block in §2 desugars mechanically to:

```gala
func processOrder(id int) Try[Receipt] =
    fetchOrder(id).FlatMap((o) =>
    validateOrder(o).FlatMap((valid) =>
    chargePayment(valid).FlatMap((payment) =>
    Success(Receipt(o.Id, payment)))))
```

This is verified to compile and run in
[`examples/bind_notation_desugared.gala`](../examples/bind_notation_desugared.gala):
`processOrder(1)` yields `ok: order=1 charged=100`, and `processOrder(0)` short-circuits
at the first `bind` to `err: no order 0`.

Properties preserved:

- **Named bindings stay in scope** — the graph shape is expressed flat, no nesting.
- **Referential transparency** — the block is an ordinary expression of type `M[R]`;
  it can be named, returned, or passed. It is *not* `?`-style imperative control flow.
- **No type-argument tax** — the desugaring carries the accumulator type directly, so
  the per-link `.FlatMap[U]` annotation disappears from the surface.

---

## 4. Independent semantics — `also` desugars to `Zip` (applicative)

A `bind` + `also` group desugars through a **product** (`Zip`), not `FlatMap`:

```gala
bind name  = validateName(r.Name)
also email = validateEmail(r.Email)
also age   = validateAge(r.Age)
Form(name, email, age)
```

desugars to:

```gala
Zip3(validateName(r.Name), validateEmail(r.Email), validateAge(r.Age))
    .FlatMap((t) => Success(Form(t.V1, t.V2, t.V3)))
```

The `Zip` is where independence pays off, and its behavior is **per type**:

| Type | `Zip` behavior |
|------|----------------|
| `Future` | runs the group **concurrently** — structured concurrency for free |
| `Validated` | **accumulates all errors** instead of stopping at the first |
| `Try` / `Option` / `Either` | sequential short-circuit on the first failure; grouping still documents independence and licenses reordering |

**Independence rule.** `also` bindings may not reference each other or the `bind` in
their group — that non-dependence is exactly what makes the group applicative. A
cross-reference is a compile error that points the author to `bind`.

> **std gap.** `Zip`/product combinators do **not** exist on `Try`/`Option`/`Either`
> today (only `Map`/`FlatMap`/`Filter`). Implementing `also` requires adding them, plus
> a `Validated` accumulating type. This is why the verification example exercises only
> the sequential form.

---

## 5. Mixing monads — heterogeneous lift

`bind x = e` where `e : N[A]` and `N` differs from the block's `M` is legal **iff a
`Lift[N, M]` conversion is resolvable** — the same idea as Rust's `?` requiring a
`From` impl:

```gala
val out = Either[AppErr, Receipt] {
    bind cfg  = loadConfig()                        // Either[ConfigErr, _], lifted via Lift[ConfigErr, AppErr]
    bind user = findUser(id).ToRight(NotFound(id))  // Option -> Either at the call site
    buildReceipt(cfg, user)
}
```

Homogeneous binds need no lift. A missing `Lift` is a compile error, never a silent
coercion. This replaces Scala's monad transformers (`EitherT`/`OptionT`) for the common
case.

---

## 6. The "no zero" wrinkle

Short-circuiting to "nothing" is only free for types that *have* a canonical empty:

- `Option` → `None`, `List`/`Array` → empty, `Validated` → the accumulated errors.
- `Try` / `Either` short-circuit by propagating the **existing** `Failure`/`Left` from
  the bound expression — fine, because that error value came from the expression itself.

The wrinkle appears in exactly one case: **lifting a zero-type into an error-type**
(e.g. an `Option` bound inside a `Try` block). `None` carries no error, so that specific
`Lift` must *explicitly supply* one (as `.ToRight(err)` / `.ToTry(err)` does above). The
compiler forces the annotation there and nowhere else.

---

## 7. Extensibility: user-defined monads

Extensibility is a first-class requirement, not an afterthought: **the notation must
work over any user-defined monad exactly as it does over the standard library, and the
transpiler must special-case no type.** This is enforced by the project's standing rule
that `std` gets no special treatment — `Try`/`Option`/`Either`/`Future` are resolved
through the *same* mechanism a third-party monad is.

Go generics cannot express a higher-kinded `Bindable[F[_]]` interface, so GALA does not
try to. Instead the transpiler resolves the desugaring **structurally, on the block's
concrete type `M`, at transpile time** — before Go ever sees it.

Verified end-to-end in
[`examples/bind_notation_user_monad.gala`](../examples/bind_notation_user_monad.gala): a
user-defined sealed `Step` monad, with no relationship to `std`, desugars through its own
`FlatMap` identically to `Try`.

### 7.1 The minimal contract

A type `M[T]` becomes bindable by providing **one** method:

```gala
func (m M[T]) FlatMap[U any](f func(T) M[U]) M[U]
```

That alone enables `bind`. The short-circuit/empty behavior lives entirely inside the
author's `FlatMap`, so the desugaring never inspects — and never hardcodes — which
variant is the "zero". Everything else is **derived** from `FlatMap` + a unit:

| Feature | Needs | Derivation if not supplied |
|---------|-------|----------------------------|
| `bind` | `FlatMap` | — (required) |
| trailing plain value (auto-lift) | `Pure` (unit) | none — author must write the explicit constructor instead |
| `Map` | — | `FlatMap((t) => Pure(f(t)))` |
| `also` (sequential) | `FlatMap` + `Pure` | `Zip(a, b) = a.FlatMap((x) => b.Map((y) => Tuple(x, y)))` |
| `also` (concurrent / accumulating) | custom `Zip` | falls back to the sequential derivation above |
| heterogeneous `bind` | a resolvable `Lift[N, M]` | none — a missing lift is a compile error |

So the floor is `FlatMap`; supplying `Pure` unlocks auto-lift and `also`; supplying a
custom `Zip`/`Lift` unlocks richer applicative and cross-monad behavior. A brand-new user
monad gets `bind` *and* sequential `also` for free.

### 7.2 Resolution: convention first, explicit opt-in as the escape hatch

**Convention (zero ceremony).** A type exposing `FlatMap` with the canonical signature is
automatically bindable; if its package also exposes a unit function returning `M[T]`,
auto-lift is enabled. This is how `Step` in the example opts in — by existing.

**Explicit registration (escape hatch).** When names don't match the convention, or to
wire a custom `Zip`/`Lift`, the author declares a `bindable` instance. This is a
typeclass instance in spirit, resolved at transpile time, with no HKT and no runtime
dispatch:

```gala
bindable Step {
    flatMap = FlatMap       // method on Step[T]  (required)
    pure    = Go            // func/ctor: T -> Step[T]  (enables auto-lift + `also`)
    zip     = zipStep       // optional: custom applicative (concurrency / accumulation)
}

// cross-monad bind: register how N lifts into M
lift OptionToStep = func[A](o Option[A]) Step[A] =
    o match { case Some(v) => Go(v); case None() => Stop[A]("empty") }
```

`std`'s own monads are registered the same way in the standard library — there is no
hidden built-in list.

### 7.3 Laws are the author's contract

The desugaring is sound only if `M` obeys the monad laws. The transpiler cannot verify
them, so they are the author's responsibility (a `test` helper that checks them on
representative values is recommended):

- **Left identity:** `Pure(a).FlatMap(f)` ≡ `f(a)`
- **Right identity:** `m.FlatMap(Pure)` ≡ `m`
- **Associativity:** `m.FlatMap(f).FlatMap(g)` ≡ `m.FlatMap((x) => f(x).FlatMap(g))`

A custom `Zip` used for `also` must additionally agree with the sequential derivation up
to the effect it adds (ordering for concurrency, error set for accumulation).

### 7.4 Conformance checklist for a new monad

1. Define `M[T]` (typically a sealed type).
2. Implement `FlatMap[U](f func(T) M[U]) M[U]` obeying the laws — `bind` now works.
3. Provide a unit (`Pure`/constructor) — auto-lift and sequential `also` now work.
4. *(optional)* Implement `Zip` for concurrency or error accumulation.
5. *(optional)* Register `Lift[N, M]` instances to allow binding other monads inside an
   `M` block.
6. Add a `bindable` declaration only if step 2/3 names differ from the convention.

---

## 8. Type-safety rules

1. `bind`/`also` are rejected unless the enclosing block's result type is a known,
   bindable `M`.
2. A `bind` whose expression's short-circuit type cannot be lifted to `M` is a compile
   error (no implicit widening to `any`).
3. `also` bindings that reference their group is a compile error (§4).
4. The trailing expression must be `M[R]` or plainly liftable to it via `Pure`.

These keep the notation from degrading into untyped, `?`-style control flow.

---

## 9. Prior art

| Language | Construct | Relationship |
|----------|-----------|--------------|
| OCaml | `let* x = e in …`, `and*` | Direct model. `bind` = `let*`, `also` = `and*`. Same desugaring (`bind`/`product`), resolved lexically instead of structurally. |
| Haskell | `do { x <- e; … }` | Same monadic denotation; `do` has no applicative-independence keyword equivalent to `also`. |
| F# | computation expressions `let!` / `and!` | `let!`≈`bind`, `and!`≈`also`. |
| Scala | for-comprehension | Same flatMap denotation; rejected surface (see §1). |
| Gleam | `use x <- e` | Callback-desugaring, single-value. |
| Rust / Zig | `?` / `try` | Imperative escape hatch; complementary, not equivalent (see §1). |

---

## 10. Grammar sketch

Illustrative additions to `internal/parser/grammar/gala.g4` (the ANTLR source; the
generated `*.go` is never hand-edited). Modeled on the existing
`valDeclaration: 'val' (tuplePattern | identifierList) (type)? '=' expressionList;`:

```antlr
declaration
    : valDeclaration
    | varDeclaration
    | bindDeclaration      // new
    | alsoDeclaration      // new
    | ...
    ;

bindDeclaration: BIND identifier (type)? '=' expression;
alsoDeclaration: ALSO identifier (type)? '=' expression;

BIND: 'bind';
ALSO: 'also';
```

`also` is only well-formed immediately following a `bind`/`also` (enforced in the
transformer, not the grammar, to give a good error message).

---

## 11. Implementation plan

Phases are ordered by dependency. Each has explicit acceptance criteria; do not start a
phase until the previous one's criteria pass. **No implementation begins until Phase 0 is
signed off.** Every phase ends green against the standing pre-commit checklist
(`bazel build //...`, `bazel test //...`, `bazel run //:gazelle`, examples compile).

### Phase 0 — Design sign-off (gate)
- Resolve the §12 open questions (`Validated` shape, `also` applicative-vs-monadic,
  `Future` auto-parallelism) and record the decisions in this doc.
- **Acceptance:** this document approved; the two verification examples
  (`bind_notation_desugared`, `bind_notation_user_monad`) pass. *(Both already pass.)*

### Phase 1 — Grammar
- Add `BIND`/`ALSO` tokens and `bindDeclaration`/`alsoDeclaration` to
  `internal/parser/grammar/gala.g4` (§10). Add the `bindable`/`lift` registration forms
  (§7.2).
- Regenerate the parser via the grammar build. **Never hand-edit the generated `*.go`.**
- **Acceptance:** `bind`/`also`/`bindable`/`lift` parse into the expected AST; a parser
  unit test covers each form and the "`also` without a preceding `bind`" error.

### Phase 2 — Transformer / desugaring
- In `internal/transpiler/transformer`, desugar `bind`/`also` groups to `FlatMap` /
  `Zip` + `Pure` (§3, §4); implement structural resolution and the `bindable`/`lift`
  registry (§7); emit the §8 type-safety diagnostics; implement heterogeneous lift (§5)
  and the no-zero error (§6).
- **No special-casing of `std`** — `Try`/`Option`/`Either`/`Future` resolve through the
  same registry as a user monad.
- **Acceptance:** transformer unit tests for sequential `bind`, `also` groups, auto-lift,
  heterogeneous lift, and each diagnostic; a golden test asserting `bind` output is
  identical to the hand-written `FlatMap` chain in `bind_notation_desugared`.

### Phase 3 — Standard library
- Add `Zip`/product combinators to `Try`, `Option`, `Either` (`std/`), a **concurrent**
  `Zip` to `Future` (`concurrent/`), and introduce a `Validated` accumulating type.
- Register each as `bindable` in the standard library (proving the mechanism on `std`).
- **Acceptance:** `std` tests for each `Zip` (short-circuit, concurrent, accumulating);
  `Validated` law/accumulation tests.

### Phase 4 — Examples (verification programs)
- Rewrite `bind_notation_desugared` to use real `bind` syntax; keep the desugared form as
  a golden reference. Keep and extend `bind_notation_user_monad` to also exercise `also`
  and a registered `lift`. Add a new `bind_notation_validated` example (error
  accumulation) and a `bind_notation_future` example (concurrent `also`).
- **Acceptance:** all `examples/` `gala_exec_test` targets pass; per CLAUDE.md, a
  verification example exists for every new language feature (`bind`, `also`, `bindable`,
  `lift`).

### Phase 5 — IDE / LSP sync
- Run the IDE sync so the IntelliJ plugin and LSP server learn the new keywords
  (`bind`, `also`, `bindable`, `lift`): syntax highlighting, semantic annotator, and a
  live template for a `bind` block.
- **Acceptance:** plugin builds; new keywords highlight; `gala-ide-sync` reports no drift.

### Phase 6 — In-repo documentation (`docs/`)
- **`docs/GALA.MD`** — promote the "Design proposals" pointer to real sections: §6
  Control Flow (the `bind`/`also` block) and §9 Standard Library Types (`Validated`,
  `Zip`). Remove the "not yet implemented" framing from this file's status header.
- **`docs/TYPE_INFERENCE.MD`** — the lift resolution, trailing-value auto-lift, and
  `also` product inference rules.
- **`docs/EXAMPLES.MD`** — concise `bind`/`also` examples (concise functional syntax).
- **`docs/GALA_BEST_PRACTICES.MD`** — a new rule: prefer `bind`/`also` over nested
  `FlatMap` for multi-step monadic code; prefer `also` over `bind` for independent steps;
  when to still reach for `Map`/`match`/`Recover` (see §"complementary tools").
- **Extension guide** — a dedicated "Extending `bind`/`also` to your own monad" section
  (promote §7 here) with the conformance checklist and the `Step` walkthrough.
- **Acceptance:** `gala-doc-verifier` passes; every doc code block transpiles.

### Phase 7 — Website (`website/`, Jekyll)
- **`website/features/error-handling.md`** — add a `bind`/`also` subsection (or a new
  `website/features/monadic-binding.md` linked from the features nav in `_config.yml`).
- **`website/docs/language-reference.md`** — mirror the `docs/GALA.MD` additions.
- **`website/docs/examples.md`** — add the `bind`/`also`/`Validated` examples.
- **`website/vs-go.md`** — optional: a side-by-side of a `bind` block vs the Go
  `if err != nil` ladder.
- **Extension how-to on the site** — surface the user-monad extension guide as a page so
  third-party authors can find it.
- **Acceptance:** site builds (`bundle exec jekyll build`); new pages appear in nav; code
  samples match the verified examples.

### Phase 8 — Final verification
- Full `bazel build //...` + `bazel test //...`; `bazel run //:gazelle`; all `examples/`
  compile; docs verified; website builds.
- **Acceptance:** the CLAUDE.md pre-commit checklist passes end to end.

---

## 12. Resolved decisions (Phase 0)

- **One `also` keyword, semantics chosen by the type.** GALA exposes a single `also`; it
  does *not* mirror OCaml's `and*`/`and+` split. The applicative-vs-monadic distinction
  is academic to most users and a source of confusion, so it is hidden: `also` desugars
  through the block type's registered `Zip`, and each type defines what that means
  (concurrent for `Future`, accumulating for `Validated`, sequential short-circuit
  otherwise). See §4.
- **`Validated` is a distinct sealed type.** Error accumulation lives in a new
  `Validated[E, A]` (Valid/Invalid) with a combining `E`, kept separate from `Either`'s
  fail-fast semantics — never an overloaded second behavior on `Either`.
- **`Future` concurrency is opt-in via `also`.** Sequential `bind`s on a `Future` run
  sequentially; only an `also` group runs concurrently. Concurrency is always visible in
  the source — the transpiler never auto-parallelizes consecutive `bind`s.
