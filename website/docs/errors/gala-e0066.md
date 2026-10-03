---
layout: default
title: "GALA-E0066 — Declaration Collides with a Dot Import"
description: "\"type List is also exported by collection_immutable, which this package dot-imports\" — GALA-E0066 fires when a package declares a name that one of its dot imports also exports."
keywords: "gala-e0066, gala dot import collision, gala already declared through dot-import, gala shadow std type, gala type named like std"
permalink: /docs/errors/gala-e0066/
last_modified_at: 2026-10-03
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0066</p>

# GALA-E0066 — Declaration collides with a dot import

**When it fires.** The package declares a top-level type, sealed variant or
function under a name that a package it dot-imports also exports, as in
`struct List(...)` beside `import . "martianoff/gala/collection_immutable"`.

A type the package declares shadows the same name of an import: a package
that declares its own `Seq`, `Hashable` or `Ordered` means its own wherever it
writes the bare name, and std's stays reachable as `std.Seq` through
`import "martianoff/gala/std"`. A dot import is the one exception. GALA's
`import . "path"` is a Go dot import, and Go allows no package-level name
that a dot import also brings in — in any file of the package, since a
package-level name is visible in all of them. Rather than leave that to
`go build` (`List already declared through dot-import of package
collection_immutable`), the declaration is reported against the source.

The check covers the dot imports of every file of the package, GALA and Go
packages alike. The implicit std import is not a dot import and never
collides.

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

struct List(Head int)

func main() {
    Println(List(1).Head)
}
```

**Error output.**

```
error[GALA-E0066]: type List is also exported by collection_immutable, which this package dot-imports
  --> main.gala:5:8
  |
5 | struct List(Head int)
  |        ^^^^ rename List, or import collection_immutable under a name ins…
  |
  = hint: rename List, or import collection_immutable under a name instead of `.`; Go allows no package-level name that a dot import also brings in
```

**Fix.** Import the package under a name, so its names stay qualified and the
package's own `List` is free:

```gala
package main

import ci "martianoff/gala/collection_immutable"

struct List(Head int)

func main() {
    Println(List(1).Head)
    Println(ci.ListOf(1, 2).Size())
}
```

Or keep the dot import and rename the declaration:

```gala
package main

import . "martianoff/gala/collection_immutable"

struct Cell(Head int)

func main() {
    Println(Cell(1).Head)
    Println(ListOf(1, 2).Size())
}
```

**Related.** [GALA-E0032](/docs/errors/gala-e0032/) is the same Go rule between
two dot imports that export one name.
