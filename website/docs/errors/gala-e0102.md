---
layout: default
title: "GALA-E0102 — Unused Local Val or Var"
description: "GALA-E0102 fires when a local val or var is declared and nothing reads it. Use it, remove it, or write _ for the unused part of a destructuring."
keywords: "gala-e0102, gala unused variable, gala unused val, gala declared and not used, gala destructuring underscore"
permalink: /docs/errors/gala-e0102/
last_modified_at: 2026-11-11
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0102</p>

# GALA-E0102 — Unused local val or var

**When it fires.** A local `val` or `var` is declared and nothing reads it.
A `var` that is only ever assigned counts as unused too, and so does one name
of a tuple destructuring (`val (a, b) = ...`) when the others are read.
Package-level declarations are not checked.

**Minimal repro.**

```gala
package main

func main() {
    val total = 2 + 3
    Println("done")
}
```

**Error output.**

```
error[GALA-E0102]: unused val 'total': it is declared but never used
  --> main.gala:4:9
  |
4 |     val total = 2 + 3
  |         ^^^^^ use it, or remove the declaration
  |
  = hint: use it, or remove the declaration; to run its initializer only for its effect, write the expression as a statement on its own
```

**Fix.** Use the value, or remove the declaration:

```gala
package main

func main() {
    val total = 2 + 3
    Println(s"done: $total")
}
```

When only the initializer's effect is wanted, write the expression as a
statement on its own, not `val _ = expr`. In a destructuring, write `_` for
the part that is not needed: `val (a, _) = pair`.

**Rationale.** Go rejects a local variable that nothing reads ("declared and
not used"). Such a program used to pass GALA's checks and fail in `go build`,
reported against the generated Go. GALA reports it at the name in the GALA
source instead.

---

## Related

- [Variable declarations](/docs/language-reference/#2-variable-declarations)
- [All GALA error codes](/docs/errors/)
