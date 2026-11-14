---
layout: default
title: "GALA-E0111 — inout Argument Is Not & of a Mutable Place"
description: "GALA-E0111 rejects an argument to an inout parameter that is missing its & or is not a mutable place, and an inout parameter declared variadic or with a default."
keywords: "gala-e0111, gala inout, gala inout argument, gala ampersand, gala mutable place, gala ownership"
permalink: /docs/errors/gala-e0111/
last_modified_at: 2026-11-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0111</p>

# GALA-E0111 — `inout` argument is not `&` of a mutable place

**When it fires.** An `inout` parameter lends the caller's place to the
callee for the call (see [`inout` parameters](/docs/language-reference/#inout-parameters)).
Its argument must say so: it is `&place`, where the place is

- a `var` (a local, a `var` parameter or a package-level `var`),
- an `inout` parameter of the calling function,
- a `var` field of one of these (`&p.lex`), or
- the pointee of a `Ptr`: a `Ptr[T]` the caller holds is passed as it is.

The error fires when the `&` is missing (`bump(c)`), or when the operand of
`&` is not a mutable place: a call result (`&mk()`) or a literal. (A `val` or
a `val` field is already [GALA-E0076](/docs/errors/gala-e0076/).) It also
fires on an `inout` parameter declared variadic or with a default, and on a
struct field declared `inout`: none of them would lend one place of the caller.

**Minimal repro.**

```gala
package main

struct Counter(var n int)

func bump(inout c Counter) {
    c.n = c.n + 1
}

func main() {
    var c = Counter(0)
    bump(c)
    Println(c.n)
}
```

**Error output.**

```
error[GALA-E0111]: the argument c of bump is passed to an inout parameter without `&`
  --> main.gala:11:10
   |
11 |     bump(c)
   |          ^ lend the place with `&c`
   |
   = hint: lend the place with `&c`; an inout argument is marked at the call site
```

**Fix.** Mark the argument with `&`. The call site then shows that `c` may
change:

```gala
package main

struct Counter(var n int)

func bump(inout c Counter) {
    c.n = c.n + 1
}

func main() {
    var c = Counter(0)
    bump(&c)
    Println(c.n) // 1
}
```

To lend a value that is not a place, copy it into a `var` first:
`var tmp = mk()`, then `bump(&tmp)`.

**Rationale.** `&` at the call site is Swift's rule for `inout`: a reader sees
which arguments a call may change. It is also the Go that is generated: an
`inout x T` is the Go parameter `x *T`.

---

## Related

- [`inout` parameters](/docs/language-reference/#inout-parameters)
- [GALA-E0114](/docs/errors/gala-e0114/): an `inout` method called on a value that is not a place
- [All GALA error codes](/docs/errors/)
