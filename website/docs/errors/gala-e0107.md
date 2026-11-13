---
layout: default
title: "GALA-E0107 — Declaration Named Like a Go Builtin That Hand-Written Go Refers To"
description: "GALA-E0107 fires when a package mixes GALA and hand-written Go, and a GALA declaration named like a Go builtin is a name the Go refers to."
keywords: "gala-e0107, gala go builtin name, gala mixed go package, gala hand-written go, gala len max print"
permalink: /docs/errors/gala-e0107/
last_modified_at: 2026-11-12
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0107</p>

# GALA-E0107 — Declaration named like a Go builtin that hand-written Go refers to

**When it fires.** A package mixes GALA with hand-written `.go` files, a
top-level function, `val`, `var` or `embed val` of its GALA code is named like a
Go predeclared identifier (`len`, `max`, `print`, `copy`, …), and the package's
hand-written Go refers to that name. The transpiler emits such a declaration
under a reserved name, so the generated Go that calls Go's builtin still reaches
the builtin. The hand-written Go reaches Go's builtin too, not the GALA
declaration, and the compiler cannot tell which of the two it meant.

**Minimal repro.** `bridge.go`, beside `main.gala`:

```go
package main

func larger(a, b int) int { return max(a, b) }
```

`main.gala`:

```gala
package main

func max(a int, b int) int = if (a > b) a else b

func main() {
    Println(larger(1, 2))
}
```

**Error output.**

```
error[GALA-E0107]: `max` is named like Go's predeclared `max`, which this package's hand-written Go refers to
  --> main.gala:3:6
  |
3 | func max(a int, b int) int = if (a > b) a else b
  |      ^^^ rename it
  |
  = hint: rename it; the generated Go names it differently, so the package's .go files reach Go's `max`, not this one
```

**Fix.** Rename the GALA declaration, and have the hand-written Go call it by
the new name if it meant the GALA one:

```gala
package main

func maxOf(a int, b int) int = if (a > b) a else b

func main() {
    Println(maxOf(1, 2))
}
```

**Rationale.** In Go, a package-level declaration named `max` hides the builtin
in every file of the package, generated and hand-written alike, so code the
transpiler generates that calls the builtin would break. GALA keeps the builtin
reachable by renaming its own declaration in the generated Go. Hand-written Go
spells the name it sees, and that is now always the builtin: a call meant for
the GALA declaration would compile and quietly run Go's. A package with no
hand-written Go, or whose Go never spells the name, is not affected.

---

## Related

- [Go interop](/docs/go-interop/)
- [All GALA error codes](/docs/errors/)
