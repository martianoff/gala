---
layout: default
title: "GALA-E0097 — Identifier Uses a Reserved Prefix"
description: "GALA-E0097 fires when an identifier starts with _gala_ or GALA_, prefixes reserved for names the transpiler generates. Rename it."
keywords: "gala-e0097, gala reserved prefix, gala _gala_ identifier, gala GALA_ identifier, gala reserved name"
permalink: /docs/errors/gala-e0097/
last_modified_at: 2026-11-13
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0097</p>

# GALA-E0097 — Identifier uses a reserved prefix

**When it fires.** An identifier starts with `_gala_` or `GALA_`. Both prefixes
are reserved for names the transpiler generates, so no GALA name may use them:
a local, a parameter, a top-level function, val or var, an embed val, a field,
a type or a type parameter. The match is exact and case-sensitive: `Gala_x`,
`GALAX` and `gala_x` are ordinary names, and a string such as `"GALA_HOME"` is
just text. A Go package may export a `GALA_` name, so a member reached through
a dot (`pkg.GALA_X`, `v.GALA_Field`) or a named argument stays usable.

**Minimal repro.**

```gala
package main

func main() {
    val _gala_total = 3
    Println(_gala_total)
}
```

**Error output.**

```
error[GALA-E0097]: identifier _gala_total starts with _gala_, a prefix reserved for names the transpiler generates
  --> main.gala:4:9
  |
4 |     val _gala_total = 3
  |         ^^^^^^^^^^^ rename it so it does not start with _gala_
  |
  = hint: rename it so it does not start with _gala_
```

The `GALA_` prefix is reported the same way:

```gala
package main

func GALA_Parser_Advance() int = 1

func main() = Println(GALA_Parser_Advance())
```

```
error[GALA-E0097]: identifier GALA_Parser_Advance starts with GALA_, a prefix reserved for names the transpiler generates
  --> main.gala:3:6
  |
3 | func GALA_Parser_Advance() int = 1
  |      ^^^^^^^^^^^^^^^^^^^ rename it so it does not start with GALA_
  |
  = hint: rename it so it does not start with GALA_
```

**Fix.** Rename it:

```gala
package main

func main() {
    val total = 3
    Println(total)
}
```

**Rationale.** The generated Go uses `_gala_` names of its own. A local named
like a Go predeclared identifier or `std`, such as `len` or `string`, is
emitted as `_gala_len` or `_gala_string`, so code the transpiler generates
around it still reaches Go's `len` and `string`. Go's `fmt` may be imported as
`_gala_fmt`. `GALA_` is the exported counterpart: Go exports only names that
start with an upper-case letter, so a generated name that other packages reach
is spelled with `GALA_`. A name of yours with either prefix could meet one of
these and quietly mean something else.

---

## Related

- [Language reference: names starting with `_gala_` or `GALA_` are reserved](/docs/language-reference/)
- [All GALA error codes](/docs/errors/)
