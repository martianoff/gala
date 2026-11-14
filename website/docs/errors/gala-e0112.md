---
layout: default
title: "GALA-E0112 — One Call Lends Overlapping inout Places"
description: "GALA-E0112 rejects a call that lends one place twice: two inout arguments naming overlapping places, an inout receiver lent again, or a lambda argument reading a lent place."
keywords: "gala-e0112, gala inout, gala ownership, gala exclusivity"
permalink: /docs/errors/gala-e0112/
last_modified_at: 2026-11-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0112</p>

# GALA-E0112 — one call lends overlapping `inout` places

**When it fires.** A call lends each `inout` place exclusively: for the
duration of the call, nothing else reads or writes it. The error fires when
one call lends the same place twice, or a place and a field of it:

- two `inout` arguments name overlapping places: `both(&c, &c)`,
  `f(&p, &p.lex)`;
- a method with an `inout` receiver gets an `inout` argument that overlaps
  the receiver: `p.settle(&p.left)`;
- a lambda argument of the call reads a place the call lends:
  `withFn(&c, () => c.n)`.

Disjoint fields of one place may be lent to one call: `both(&p.left,
&p.right)`.

**Minimal repro.**

```gala
package main

struct Counter(var n int)

func both(inout a Counter, inout b Counter) {
    a.n = a.n + b.n
}

func main() {
    var c = Counter(1)
    both(&c, &c)
    Println(c.n)
}
```

**Error output.**

```
error[GALA-E0112]: &c and &c overlap: a call cannot lend one place twice
  --> main.gala:11:14
   |
11 |     both(&c, &c)
   |              ^^ lend disjoint places, or copy one of them into a local `var`…
   |
   = hint: lend disjoint places, or copy one of them into a local `var` first
```

**Fix.** Lend disjoint places. To pass the same value twice, copy it into a
local `var` first:

```gala
package main

struct Counter(var n int)

func both(inout a Counter, inout b Counter) {
    a.n = a.n + b.n
}

func main() {
    var c = Counter(1)
    var other = c
    both(&c, &other)
    Println(c.n) // 2
}
```

**Rationale.** The callee may assume that its `inout` parameters are
distinct places, as Swift's law of exclusivity guarantees. With two paths to
one place, a write through one is seen through the other, which neither the
callee nor its translation to value semantics expects.

---

## Related

- [`inout` parameters](/docs/language-reference/#inout-parameters)
- [All GALA error codes](/docs/errors/)
