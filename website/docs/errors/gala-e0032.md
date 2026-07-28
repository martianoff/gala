---
layout: default
title: "GALA-E0032 — Dot-Import Symbol Collision"
description: "\"dot-import symbol collision(s) detected\" — GALA-E0032 fires when two dot-imported packages export the same identifier. See the real compiler output, and how a qualified or aliased import resolves it."
keywords: "gala-e0032, dot-import symbol collision, gala dot import, gala import alias, gala redeclared in this block, gala package import error"
permalink: /docs/errors/gala-e0032/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0032</p>

# GALA-E0032 — Dot-import symbol collision

**What it means.** Two or more **dot-imported** packages export the same identifier. A dot import (`import . "path"`) drops every exported name of the package into the current file's namespace; when two of them supply the same name, the generated Go contains two declarations of that identifier and Go rejects it with "redeclared in this block".

The check needs at least two dot imports to run, and considers every exported name the analyzer knows about: GALA types (including sealed-variant companions), functions, companion objects, and the exports of dot-imported Go packages. It runs early in transformation, before any expression is transformed.

---

## Code that triggers it

The shortest real case needs no fixture packages at all. `concurrent` re-exports `go_interop`'s execution-context helpers as a convenience facade, so dot-importing both is always a collision:

```gala
package main

import (
    . "martianoff/gala/concurrent"
    . "martianoff/gala/go_interop"
)

func main() {
    Println("hi")
}
```

---

## Compiler message

```
error[GALA-E0032]: dot-import symbol collision(s) detected:
  - symbol "Cancel" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "CancelToken" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "ExecutionContext" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "FixedPoolExecutionContext" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "GlobalEC" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "IsCancelled" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "NewCancelToken" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "NewFixedPoolEC" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "NewSingleThreadEC" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "SetGlobalEC" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "SingleThreadExecutionContext" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "Spawn" is exported by multiple dot-imported packages: concurrent, go_interop
  - symbol "UnboundedExecutionContext" is exported by multiple dot-imported packages: concurrent, go_interop
Use a qualified or aliased import for one of the packages to resolve the conflict.
(Some stdlib packages intentionally re-export names from another package as a convenience facade — e.g. `concurrent` re-exports `go_interop`'s execution-context helpers — so dot-importing both is never meaningful. Pick the facade you want.)
  --> main.gala:3:1
  |
3 | import (
  | ^^^^^^ qualify or alias one of the dot-imports to disambiguate
  |
  = hint: qualify or alias one of the dot-imports to disambiguate
```

Every colliding symbol is listed, one bullet per name, sorted, with the packages that supply it. The caret points at the import block rather than a single import, because the collision is a property of the set.

Your own packages report the same way. Two packages that each export `Greet`, both dot-imported:

```
error[GALA-E0032]: dot-import symbol collision(s) detected:
  - symbol "Greet" is exported by multiple dot-imported packages: pkg_a, pkg_b
Use a qualified or aliased import for one of the packages to resolve the conflict.
(Some stdlib packages intentionally re-export names from another package as a convenience facade — e.g. `concurrent` re-exports `go_interop`'s execution-context helpers — so dot-importing both is never meaningful. Pick the facade you want.)
  --> main.gala:3:1
  |
3 | import (
  | ^^^^^^ qualify or alias one of the dot-imports to disambiguate
  |
  = hint: qualify or alias one of the dot-imports to disambiguate
```

---

## How to fix it

Demote one of the dot imports to a qualified import and prefix its uses:

```gala
package main

import (
    . "example.com/demo/pkg_a"
    "example.com/demo/pkg_b"
)

func main() {
    Println(Greet())            // pkg_a, via the dot import
    Println(pkg_b.Greet())      // pkg_b, explicitly qualified
}
```

An alias works too when you want both short:

```gala
import (
    . "example.com/demo/pkg_a"
    b "example.com/demo/pkg_b"
)
```

When the two packages are a facade and the package it re-exports — `concurrent` and `go_interop` — the answer is simpler: **pick one**. Dot-importing both is never meaningful.

---

## Why the rule exists

Without this check the collision surfaced from `go build`, against generated code, naming a file the author never wrote. Detecting it at the GALA level names the symbol, names every package that supplies it, and points at the import block that has to change. It was previously reported as an uncoded semantic error; the stable code lets tooling distinguish an import conflict from other semantic failures.

**Scope.** Dot imports only. A qualified import can never collide, because its names live behind the package selector. A collision between a dot-imported name and a *local* declaration is not flagged here — local declarations shadow dot imports by design.

---

## Related

- [GALA-E0026](/docs/errors/gala-e0026/) — sealed-variant ambiguity, which this check catches first
- [GALA-E0020](/docs/errors/gala-e0020/) — the package cannot be found at all
- [GALA-E0025](/docs/errors/gala-e0025/) — the symbol exists, but this file never imported it
- [Dependency Management](/docs/dependency-management/) — import forms and aliasing
- [All GALA error codes](/docs/errors/)
