---
layout: default
title: "GALA-E0021 — Type Mismatch (Unification Failure)"
description: "GALA-E0021 is GALA's general type-mismatch code: cannot unify X and Y, if branches must have same type, if condition must be bool. See the messages and the three standard fixes."
keywords: "gala-e0021, cannot unify, gala type mismatch, if branches must have same type, if condition must be bool, gala type inference error"
permalink: /docs/errors/gala-e0021/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0021</p>

# GALA-E0021 — Type mismatch (unification failure)

**What it means.** Two types the inference engine required to agree could not be unified. One code covers every failure of the Hindley-Milner unification step, including:

- a call argument's type not matching the parameter type,
- the two arms of an `if` expression producing different types,
- the condition of an `if` expression not being `bool`,
- a constructor's positional argument not matching the declared field type.

A single code means you grep for one identifier; the message describes the specific mismatch.

---

## Compiler messages

The header takes one of three shapes, each followed by the same hint:

- `error[GALA-E0021]: cannot unify <T1> and <T2>`
- `error[GALA-E0021]: if branches must have same type: <T1> and <T2>`
- `error[GALA-E0021]: if condition must be bool, got <T>`

The hint reads: *the expression's type does not match what the surrounding context expects — annotate the binding or convert the value explicitly*.

These come from the inference engine and are raised without a source position, so the CLI prints no framed snippet. The header shapes above are quoted from the emit site rather than from a compiler run: no output is shown on this page because the everyday mismatches you would expect to trigger it — a wrong argument type, an `if` with a non-`bool` condition, mixed branch types, a constructor argument of the wrong type — all transpile cleanly and surface from the **Go compiler** afterwards instead, pointing back at the `.gala` line through GALA's line directives. The type-checking half of GALA's inference was deliberately left to Go.

What that means in practice: passing a `string` to `func twice(n int) int` gives you a Go-shaped message against your `.gala` file, not a GALA-E0021 header:

```
# gala-build-workspace/gen
main.gala:6: cannot use "hello" (untyped string constant) as int value in argument to twice
go build: exit status 1
```

---

## How to fix it

**1. Convert one side.** Make the coercion explicit rather than hoping the compiler picks a side.

**2. Annotate the binding.** When inference has too little context, a declared type forces it to commit, and the real mismatch surfaces against a fixed reference type:

```gala
val ids Array[int] = parse(input)
```

**3. Restructure.** If both branches of an `if` honestly produce different types, you usually want a sum type — `Option[A]` or `Either[A, B]` — instead of mixing values:

```gala
val result = if (ok) Right[string, int](42) else Left[string, int]("bad input")
```

---

## Why one shared code

Promoting these out of ad-hoc errors gives tools and CI a stable identifier, and mirrors how the Go compiler buckets its own mismatches under `cannot use X as Y`. You learn the code once and recognize it at every unification site.

**Scope.** Inference-engine failures. Type errors emitted elsewhere in the transpiler — sealed-variant arity, default-value mismatch — keep their own codes.

---

## Related

- [Type Inference](/features/type-inference/) — what GALA infers and what it needs told
- [GALA-E0022](/docs/errors/gala-e0022/) — occurs check (infinite type)
- [GALA-E0014](/docs/errors/gala-e0014/) — default-value type mismatch
- [Error Handling](/features/error-handling/) — `Option` and `Either` instead of mixed branch types
- [All GALA error codes](/docs/errors/)
