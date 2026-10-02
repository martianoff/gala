---
layout: default
title: "GALA-E0023 — Undefined Variable"
description: "\"undefined: x\" — GALA-E0023 means a name has no binding: a typo, a missing import, or a reference outside the scope where it is bound. See the real compiler output and each fix."
keywords: "gala-e0023, undefined variable, gala undefined identifier, gala unknown name, gala missing import, gala scope error, gala type inference error"
permalink: /docs/errors/gala-e0023/
last_modified_at: 2026-10-01
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0023</p>

# GALA-E0023 — Undefined variable

**What it means.** A name has no binding in the current environment. Three usual causes:

- the name is misspelled;
- it lives in another package that this file does not import;
- it is bound by a pattern that did not actually fire, or referenced outside its arm.

---

## Code that triggers it

```gala
package main

func main() {
    Println(x + 1)
}
```

---

## Compiler message

```
error[GALA-E0023]: undefined: x
  --> e0023.gala:4:13
  |
4 |     Println(x + 1)
  |             ^ check the spelling, add the import that introduces this name…
  |
  = hint: check the spelling, add the import that introduces this name, or declare it — every identifier must resolve to a binding, a declaration in this package, or an imported symbol
```

The diagnostic is framed and carries the offending identifier's own span, so the caret lands on the name itself.

---

## How to fix it

Fix the typo, add the import that introduces the name, or move the reference inside the scope where it is bound. Pattern bindings only exist inside their own arm:

```gala
val label = shape match {
    case Circle(r) => s"circle r=$r"     // r is bound here
    case Square(s) => s"square s=$s"     // ...and s only here
}
```

If the name is meant to come from a dot-imported package, confirm the import is present in *this* file — sibling files' imports do not propagate.

A bare GALA name must come from this file's package, a package this file dot-imports, or the `std` prelude. A package that is loaded only because one of your imports uses it does not count. The GALA `strings` package loads the collection packages, but a file that imports only `strings` still cannot call `ArrayOf` or write `Array[int]` unqualified. A package imported by name is reached through its qualifier:

```gala
import "martianoff/gala/collection_immutable"

val xs = ArrayOf(1, 2)                       // GALA-E0023
val ys = collection_immutable.ArrayOf(1, 2)  // ok
```

Type names are checked the same way. A name written as a parameter, result, struct field, type argument or `val` annotation must be a predeclared Go type, a type parameter, or a type that this package, a dot-imported package or the `std` prelude declares. Without the import, `func total(xs Array[int])` is reported at `Array`, and the hint names the packages that declare it. It used to compile as far as `go build` and fail there.

---

## Why the rule exists

Undefined names are the most common failure during early development, and the alternative was letting them fall through to the Go compiler, which reports them against generated code. Resolving every identifier at the GALA level keeps the error on the line you wrote, and the stable code lets editors and CI tools attach extra context.

**Scope.** Identifier resolution, in value and type position. A signature type that the analyzer resolved to a package this file did not import gets the more specific [GALA-E0025](/docs/errors/gala-e0025/). Type *mismatches* between names that both resolve are [GALA-E0021](/docs/errors/gala-e0021/), most of which are still left to the Go compiler.

---

## Related

- [GALA-E0025](/docs/errors/gala-e0025/) — the symbol exists, but its package is not imported here
- [GALA-E0021](/docs/errors/gala-e0021/) — the name resolves, but its type does not fit
- [Type Inference](/features/type-inference/)
- [All GALA error codes](/docs/errors/)
