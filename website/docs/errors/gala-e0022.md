---
layout: default
title: "GALA-E0022 — Occurs Check Failed (Infinite Type)"
description: "GALA-E0022 fires when type inference would have to substitute a type variable with a type containing itself. See the compiler message, what causes it, and how an annotation resolves it."
keywords: "gala-e0022, occurs check failed, gala infinite type, hindley-milner occurs check, gala recursive type error, gala type inference"
permalink: /docs/errors/gala-e0022/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0022</p>

# GALA-E0022 — Occurs check failure

**What it means.** During unification, the inference engine tried to substitute a type variable `T` with a type that *contains* `T`. That substitution would produce an infinite type (`T = List[T]` with nothing to bottom out at), so Hindley-Milner rejects it.

In practice it surfaces from:

- a recursive value with no fixed point — the function calls itself with a value of the wrong shape and the inferer cannot pin a finite type;
- a pattern that destructures the whole value into one of its own fields.

---

## Compiler message

The header reads `error[GALA-E0022]: occurs check failed: <var> in <type>`, naming the type variable and the type it would have to expand into, with a hint suggesting an annotation or a restructured recursion.

This diagnostic comes from the Hindley-Milner unification step and is raised without a source position, so the CLI prints no framed snippet.

---

## How to fix it

Add an explicit annotation at the recursion point so inference does not have to discover the fixpoint itself:

```gala
func length[T any](xs List[T]) int = xs.Size()
```

Or restructure the value so the recursion has a base case the inferer can see. If you meant to build a genuinely recursive data shape, model it as a [sealed type](/features/sealed-types/) with an explicit terminating variant — the type then names itself through a declared constructor rather than through an inferred variable.

---

## Why the rule exists

Occurs-check failures are rare in straight-line GALA, and the underlying diagnostic is opaque unless you know what "occurs check" means. A dedicated code lets the documentation carry the explanation instead of cramming type theory into the error message.

**Scope.** Hindley-Milner unification only. The transpiler's own recursive-type guard for `Immutable[Immutable[T]]` emits [GALA-E0001](/docs/errors/gala-e0001/).

---

## Related

- [Type Inference](/features/type-inference/)
- [GALA-E0021](/docs/errors/gala-e0021/) — ordinary unification failures
- [All GALA error codes](/docs/errors/)
