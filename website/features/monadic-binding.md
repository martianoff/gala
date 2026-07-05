---
layout: default
title: "Golang Do-Notation — bind / also Monadic Binding for Go"
description: "GALA's bind/also do-notation flattens FlatMap chains into readable blocks. Sequential bind, plus also for independent steps that accumulate errors (Validated) or run concurrently (Future) — over any monad, no HKT."
keywords: "golang do notation, go monadic binding, golang for comprehension, go bind notation, golang applicative validation, go error accumulation, golang concurrent futures, go flatmap chain, gala bind also, golang monad syntax"
permalink: /features/monadic-binding/
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/features/monadic-binding/">Features</a> / Monadic Binding</p>

# Golang Do-Notation — `bind` / `also` for Go

GALA's [monad stack](/features/error-handling/) — `Option`, `Either`, `Try`, `Future` — gives you `Map` and `FlatMap`. Those are excellent for a single linear pipeline and awkward the moment a later step needs a value from two steps back: every intermediate value is trapped inside a closure, so cross-references force nesting.

`bind` / `also` is GALA's do-notation — the same idea as a Scala for-comprehension, a Haskell `do` block, or OCaml's `let*` / `and*`, with a keyword surface that tells you it can short-circuit. It desugars, mechanically, to a `FlatMap` chain.

---

## `bind`: flatten a nested `FlatMap` chain

When a later step reads an earlier value, combinators nest — here the final `Receipt` needs both the original order and the payment:

```gala
fetchOrder(id).FlatMap((o) =>
    validateOrder(o).FlatMap((valid) =>
        chargePayment(valid).FlatMap((payment) =>
            Success(Receipt(o.Id, payment)))))   // `o` survives only via nesting
```

With `bind`, every binding is a normal immutable local that stays in scope for the rest of the block:

```gala
func processOrder(id int) Try[Receipt] {
    bind o       = fetchOrder(id)
    bind valid   = validateOrder(o)
    bind payment = chargePayment(valid)
    Success(Receipt(o.Id, payment))   // `o` still in scope — no nesting
}
```

`bind name = expr` unwraps the monad, binds the success value, and short-circuits the **whole block** on the first `Failure` — `processOrder(0)` stops at the first `bind` and returns that `Failure` unchanged.

Unlike Rust's `?` or Zig's `try`, this is not a hidden return that hijacks the enclosing function. The block is an ordinary expression of type `Try[Receipt]` — you can name it, return it, or pass it around.

---

## `also`: independent binds, semantics chosen by the type

Steps that don't depend on each other shouldn't be sequenced. `also` marks a bind as independent of its group. A leading `bind` plus one or more `also` clauses form a **product group**; the clauses may not reference each other, and that non-dependence is what lets GALA do something smarter than sequencing — decided by the block's type:

| Type                        | What an `also` group does                     |
|-----------------------------|-----------------------------------------------|
| `Try` / `Option` / `Either` | sequential short-circuit on the first failure |
| `Validated`                 | **accumulates every error**                   |
| `Future`                    | runs the clauses **concurrently**             |

```gala
func sum2(x string, y string) Option[int] {
    bind a = lookup(x)
    also b = lookup(y)      // independent of `a`
    Some(a + b)
}
```

---

## `Validated`: report every error at once

Fail-fast is the wrong model for form validation. The `Validated[E, A]` type (in the `validation` package) is a distinct sealed type — `Valid` / `Invalid` — kept separate from `Either`'s fail-fast semantics. Over `Validated`, an `also` group accumulates all errors:

```gala
import . "martianoff/gala/validation"

func vName(s string)  Validated[string, string] = if (s != "") Valid(s) else InvalidOf("name required")
func vEmail(s string) Validated[string, string] = if (s != "") Valid(s) else InvalidOf("email required")
func vAge(n int)      Validated[string, int]    = if (n >= 0)  Valid(n) else InvalidOf("age negative")

func makePerson(name string, email string, age int) Validated[string, Person] {
    bind n = vName(name)
    also e = vEmail(email)
    also a = vAge(age)
    Valid(Person(n, e, a))
}
```

```gala
val bad = makePerson("", "", -1)
Println(s"errors: ${bad.GetErrors().Size()}")   // 3 — all three, not just the first
```

Swap the `also`s for `bind`s and you'd get `1`. Note there isn't a single explicit type argument — `Valid` / `Invalid` fix their type parameter from the declared return type, and `InvalidOf` infers its instantiation from context.

---

## `also` over `Future`: structured concurrency

The same `also`, over `Future`, runs the independent clauses concurrently:

```gala
import . "martianoff/gala/concurrent"

func total() Future[int] {
    bind a = compute(2)
    also b = compute(3)    // these three
    also c = compute(4)    // run in parallel
    Future[int](a + b + c)
}
```

The group lowers to `Future.Zip3(...)`, which starts every future and joins them. Concurrency is **visible in the source** — sequential `bind`s stay sequential; only an `also` group runs in parallel. The transpiler never auto-parallelizes consecutive `bind`s.

---

## It works over your own monads — no HKT

GALA's standard library gets no special treatment: `Try`, `Option`, `Either`, and `Future` resolve through the same mechanism a third-party type would. Go's generics can't express a higher-kinded `Bindable[F[_]]`, so GALA doesn't fake one — `bind` / `also` are resolved **structurally, at transpile time, on the block's concrete type**.

A type becomes bindable by providing one method:

```gala
func (m M[T]) FlatMap[U any](f func(T) M[U]) M[U]
```

That's the whole contract. A user-defined monad with no relationship to the standard library gets `bind` and sequential `also` for free:

```gala
sealed type Step[T any] {
    case Go(Value T)
    case Stop(Reason string)
}

func (s Step[T]) FlatMap[U any](f func(T) Step[U]) Step[U] = s match {
    case Go(v)   => f(v)
    case Stop(r) => Stop[U](r)
}

func pipeline(n int) Step[int] {
    bind a = start(n)
    bind b = twice(a)
    Go(a + b)          // `a` still in scope; a `Stop` anywhere short-circuits
}
```

Supply a `Pure`/constructor and you also get auto-lift of trailing plain values and sequential `also`; supply a custom `Zip` and you get concurrency or error accumulation.

---

## When to reach for `bind` / `also`

- **Multi-step monadic code where a later step reuses an earlier value** — the "graph" shape `bind` was built for.
- **Independent validations you want to report together** — `also` over `Validated`.
- **Independent async work** — `also` over `Future`.

For a single fallible operation, plain `Map` / `FlatMap` / `match` is clearer. `bind` earns its keep once the chain has more than one link and the values cross-reference.

---

## Further Reading

- [Error Handling](/features/error-handling/) — `Option`, `Either`, `Try` monads that `bind` works over
- [Concurrency](/features/concurrency/) — `Future[T]` and the concurrent `also`
- [Pattern Matching](/features/pattern-matching/) — how the sealed types behind these monads destructure
