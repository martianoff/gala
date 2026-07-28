---
layout: default
title: "GALA-E0025 — Unresolved Cross-Package Symbol (Missing Import)"
description: "GALA-E0025 fires when an unqualified name resolves to a package this file never imported. See the compiler message and both import forms that fix it — sibling files' imports do not propagate."
keywords: "gala-e0025, unresolved cross-package symbol, gala is not imported in this file, gala dot import, gala missing import error, gala undefined symbol"
permalink: /docs/errors/gala-e0025/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0025</p>

# GALA-E0025 — Unresolved cross-package symbol

**What it means.** A type or function name was used without a package qualifier (bare `Array`, `ArrayTabulate`, `MyType`) and resolves to a symbol in a GALA package this file does not import. GALA mirrors Go's rule: cross-package symbols require an explicit `import` in the file that uses them. **Sibling files' imports do not propagate.**

---

## Compiler message

Two files in one package — `a.gala` dot-imports `collection_immutable`, `b.gala` uses `Array` without importing it:

```gala
// b.gala — no import of its own
package main

func BuildLabels(n int) Array[string] = ArrayTabulate(n, (i) => s"row=$i")
```

```
error[GALA-E0025]: undefined: Array (used in BuildLabels) — 'collection_immutable' is not imported in this file
  --> b.gala:3:6
  |
3 | func BuildLabels(n int) Array[string] = ArrayTabulate(n, (i) => s"row=$i")
  |      ^^^^^^^^^^^ add an explicit import to this file
  |
  = hint: add an explicit import to this file. For unqualified usage: `import . "<path-ending-in-collection_immutable>"`. For qualified usage: `import "<path>"` and call it as `collection_immutable.Array`. Sibling files' imports do not propagate.
```

This check runs over the whole package, so it fires during `gala build` rather than a single-file `gala transpile`.

**The `used in …` part has two shapes.** In a named package the enclosing function is qualified with the package name; in `main` and test packages it is not. The same file under `package effects` reports:

```
error[GALA-E0025]: undefined: Array (used in effects.BuildLabels) — 'collection_immutable' is not imported in this file
  --> b.gala:3:6
  |
3 | func BuildLabels(n int) Array[string] = ArrayTabulate(n, (i) => s"row=$i")
  |      ^^^^^^^^^^^ add an explicit import to this file
  |
  = hint: add an explicit import to this file. For unqualified usage: `import . "<path-ending-in-collection_immutable>"`. For qualified usage: `import "<path>"` and call it as `collection_immutable.Array`. Sibling files' imports do not propagate.
```

Search for the code, not the exact `used in` text, if you are grepping across packages.

---

## How to fix it

Add the import. A dot import brings the symbols into scope unqualified:

```gala
package effects

import . "martianoff/gala/collection_immutable"

func BuildLabels(n int) Array[string] = ArrayTabulate(n, (i) => s"row=$i")
```

A plain import keeps them qualified:

```gala
package effects

import "martianoff/gala/collection_immutable"

func BuildLabels(n int) collection_immutable.Array[string] =
    collection_immutable.ArrayTabulate(n, (i) => s"row=$i")
```

---

## Why the rule exists

The analyzer used to fall back to "qualify it with the current package" when a bare name did not resolve. That silently mis-qualified cross-package types as local ones and broke type-parameter inference downstream — `func(i int) any` where you wrote something returning `string` was the canonical failure. Enforcing the import declaration matches Go's compile-time safety: every cross-package reference is checked where it is declared, not guessed at during call-site inference.

---

## Related

- [GALA-E0020](/docs/errors/gala-e0020/) — the package itself cannot be found
- [GALA-E0023](/docs/errors/gala-e0023/) — a name with no binding at all
- [Dependency Management](/docs/dependency-management/) — module and import layout
- [All GALA error codes](/docs/errors/)
