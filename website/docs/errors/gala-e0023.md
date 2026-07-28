---
layout: default
title: "GALA-E0023 — Undefined Variable"
description: "GALA-E0023 means a name has no binding in the type environment — a typo, a missing import, or a reference outside the scope where it is bound. See the compiler message and each fix."
keywords: "gala-e0023, undefined variable, gala unknown identifier, gala missing import, gala scope error, gala type inference error"
permalink: /docs/errors/gala-e0023/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0023</p>

# GALA-E0023 — Undefined variable

**What it means.** The inference engine hit a name with no binding in the current type environment. Three usual causes:

- the name is misspelled;
- it lives in another package that this file does not import;
- it is bound by a pattern that did not actually fire, or referenced outside its arm.

---

## Compiler message

The header reads `error[GALA-E0023]: undefined variable: <name>`, with a hint covering spelling, imports, and scope. It is raised without a source position, so the CLI prints no framed snippet.

In practice a plain undefined name usually surfaces from the Go compiler instead (`undefined: mysteryValue`) rather than as GALA-E0023 — the GALA-level code is reserved for the inference engine's own environment lookups. If the missing name belongs to a package this file never imported, you get [GALA-E0025](/docs/errors/gala-e0025/) instead, which is framed and far more specific.

---

## How to fix it

Fix the typo, add the import that introduces the name, or move the reference inside the scope where it is bound. Pattern bindings only exist inside their own arm:

```gala
val label = shape match {
    case Circle(r) => s"circle r=$r"     // r is bound here
    case Square(s) => s"square s=$s"     // ...and s only here
}
```

If the name is meant to come from a dot-imported package, confirm the import is present in *this* file — sibling files' imports do not propagate. That case usually reports as [GALA-E0025](/docs/errors/gala-e0025/) instead.

---

## Why the rule exists

Undefined names are the most common inference failure during early development. A stable code lets editors and CI tools attach extra context — for example "did you mean…" suggestions drawn from the type environment.

**Scope.** Inference engine only. The transformer's scope walker emits its own diagnostics for undefined identifiers in non-inference contexts.

---

## Related

- [GALA-E0025](/docs/errors/gala-e0025/) — the symbol exists, but its package is not imported here
- [Type Inference](/features/type-inference/)
- [All GALA error codes](/docs/errors/)
