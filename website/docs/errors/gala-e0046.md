---
layout: default
title: "GALA-E0046 — Package Already Imported"
description: "\"package is already imported\" — GALA-E0046 fires when a file imports the same package twice under the same name, reported against your .gala line instead of the generated Go."
keywords: "gala-e0046, gala package already imported, gala duplicate import, gala import block, gala redeclared in this block, gala imported and not used"
permalink: /docs/errors/gala-e0046/
last_modified_at: 2026-09-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0046</p>

# GALA-E0046 — package already imported

**What it means.** A file imports the same package twice under the same local name. It usually happens by editing: an import block added at the top of a file that already has one further down.

---

## Code that triggers it

```gala
package main

import (
    "strings"
)

// A second block importing the SAME package.
import (
    "strings"
)

func main() {
    Println(strings.Repeat("-", 3))
}
```

---

## Compiler message

```
error[GALA-E0046]: package "strings" is already imported at line 4
  --> main.gala:9:5
  |
9 |     "strings"
  |     ^^^^^^^^^ remove this import — a file's import blocks are merged, so a…
  |
  = hint: remove this import — a file's import blocks are merged, so a package listed in one block is in scope for the whole file
```

---

## How to fix it

Remove the second import. A file may have as many import blocks as it likes — they are merged — so a package named in one is in scope for the whole file.

The same path under two *different* local names stays legal, as it is in Go:

```gala
import "strings"
import gostr "strings"

val a = strings.Repeat("-", 3)
val b = gostr.Repeat("+", 2)
```

---

## Why the rule exists

The rejection is not new; the *reporter* is. A repeated path used to reach `go build` as two identical import lines and was reported against the generated file — `strings redeclared in this block`, plus a misleading `"strings" imported and not used` for a package used exactly as often as it was imported. Neither message pointed at the source.

---

## Related

- [GALA-E0020](/docs/errors/gala-e0020/) — a package that cannot be found
- [GALA-E0032](/docs/errors/gala-e0032/) — two dot-imported packages exporting the same name
- [Dependency Management](/docs/dependency-management/)
- [All GALA error codes](/docs/errors/)
