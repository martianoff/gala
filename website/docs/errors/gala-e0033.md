---
layout: default
title: "GALA-E0033 — Lambda Parameter Has No Type and None Can Be Inferred"
description: "\"lambda parameter has no type and none can be inferred from context\" — GALA-E0033. See the triggering code, the real compiler output, and the typed contexts that make annotations unnecessary."
keywords: "gala-e0033, lambda parameter has no type, gala untyped lambda, gala lambda type inference, gala closure parameter error, gala type annotation"
permalink: /docs/errors/gala-e0033/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0033</p>

# GALA-E0033 — Untyped lambda parameter

**What it means.** A lambda parameter has no type annotation *and* the surrounding context supplies no expected type to infer one from. The canonical case is a lambda bound to a `val` with no declared type.

---

## Code that triggers it

```gala
func main() {
    val f = (x) => x + 1          // nothing tells the compiler what x is
    Println(f(2))
}
```

There is nothing to infer `x` from. The only way to give it a type would be to emit `any`, which violates GALA's concrete-types invariant and produces Go that either fails to compile or silently erases the type.

---

## Compiler message

```
error[GALA-E0033]: lambda parameter "x" has no type and none can be inferred from context
  --> e0033.gala:4:14
  |
4 |     val f = (x) => x + 1
  |              ^ annotate it
  |
  = hint: annotate it (e.g. `(x int) => …`) or use the lambda in a typed context (typed val, function argument, or return)
```

---

## How to fix it

Annotate the parameter:

```gala
val f = (x int) => x + 1
```

…or put the lambda in a typed context, which threads the declared signature into it so the parameter types are inferred:

```gala
val f func(int) int = (x) => x + 1        // declared val type
arr.Map((x) => x * 2)                     // method argument
func mk() func(int) int = (x) => x + 1    // function return
```

The third form is why idiomatic GALA rarely annotates lambdas: pass them straight to `Map`, `Filter`, `FoldLeft`, or a typed parameter and the types flow in.

---

## Why the rule exists

A lambda in a typed slot draws its parameter and return types from that slot. A bare lambda initializer has no such slot, so an annotation is the only way to keep the generated Go concrete. This parameter used to default to `any` with a warning; the error surfaces the problem at its source instead of deferring it to a confusing Go compile error — or a silent `any`.

**Scope.** Only lambda parameters in contexts that supply no expected type.

---

## Related

- [Type Inference](/features/type-inference/) — how expected types propagate into lambdas
- [GALA-E0034](/docs/errors/gala-e0034/) — the same rule for function and struct parameters
- [All GALA error codes](/docs/errors/)
