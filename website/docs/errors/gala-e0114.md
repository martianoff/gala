---
layout: default
title: "GALA-E0114 — inout Method Called on a Value That Is Not a Mutable Place"
description: "GALA-E0114 rejects a call of a method with an inout receiver on a val, a parameter, a val field, a call result or a literal: an inout receiver always borrows a place."
keywords: "gala-e0114, gala inout receiver, gala inout method val, gala mutable place, gala ownership"
permalink: /docs/errors/gala-e0114/
last_modified_at: 2026-11-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0114</p>

# GALA-E0114 — `inout` method called on a value that is not a mutable place

**When it fires.** A method with an `inout` receiver, `func (inout c T) m()`,
lends the receiver's place for the call. It is called on a mutable place: a
`var`, an `inout` parameter, a `var` field of one, or a `Ptr[T]`. The error
fires when it is called on anything else: a `val`, a parameter not declared
`var`, a `val` field, a call result or a literal. It fires even when the
method only reads its receiver.

**Minimal repro.**

```gala
package main

struct Counter(var n int)

func (inout c Counter) bump() {
    c.n = c.n + 1
}

func main() {
    val c = Counter(0)
    c.bump()
    Println(c.n)
}
```

**Error output.**

```
error[GALA-E0114]: cannot call bump on this Counter: bump has an inout receiver, and this Counter is not a mutable place
  --> main.gala:11:7
   |
11 |     c.bump()
   |       ^^^^ call it on a `var`, an inout parameter or a `Ptr`
   |
   = hint: call it on a `var`, an inout parameter or a `Ptr`; to change a copy, copy the value into a `var` first
```

**Fix.** Call it on a place: declare the value `var`, or copy it into one.

```gala
package main

struct Counter(var n int)

func (inout c Counter) bump() {
    c.n = c.n + 1
}

func main() {
    var c = Counter(0)
    c.bump()
    Println(c.n) // 1
}
```

**Rationale.** A `Ptr` receiver called on a `val` runs on a hidden copy when
it only reads its receiver, and is [GALA-E0110](/docs/errors/gala-e0110/) when
it can change it. An `inout` receiver is a contract that the method borrows a
place, so it is never called on a copy. A method that only reads its
receiver needs no `inout`: give it a value receiver, `func (c T) m()`.

---

## Related

- [`inout` parameters](/docs/language-reference/#inout-parameters)
- [GALA-E0111](/docs/errors/gala-e0111/): an `inout` argument that is not `&` of a place
- [Ownership guide](/docs/ownership/#the-rules-and-how-to-fix-each-error): every `inout` rule, with its fix
- [All GALA error codes](/docs/errors/)
