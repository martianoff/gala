---
layout: default
title: "GALA-E0076 — Address of an Immutable Value"
description: "GALA-E0076 fires when &x takes the address of a val, a parameter, a receiver or an immutable field. Pass the value itself, or declare it var where a *T is needed."
keywords: "gala-e0076, gala address of val, gala cannot take the address, gala pointer to val, gala val vs var pointer, gala pointer receiver"
permalink: /docs/errors/gala-e0076/
last_modified_at: 2026-10-12
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0076</p>

# GALA-E0076 — Address of an immutable value

**When it fires.** `&x` takes the address of something that cannot change:

- a `val`, local or package-level;
- a function, method or lambda parameter not declared `var`;
- a receiver;
- an immutable field (`n` in `struct Obj(n int)`);
- a field reached through a `val` (`&o.n` where `o` is a `val`).

`&x` is a `*T`, and a `*T` can write, so a pointer to any of these would be a
way to change it. Only something that can change has an address: a `var`, a
`var` field of a `var`, a field of a pointee, a composite literal.

**Minimal repro.**

```gala
package main

struct Counter(var N int)

func bump(c *Counter) {
    c.N = c.N + 1
}

func main() {
    val c = Counter(0)
    bump(&c)
    Println(c.N)
}
```

**Error output.**

```
error[GALA-E0076]: cannot take the address of c: c is a val
  --> main.gala:11:10
   |
11 |     bump(&c)
   |          ^^ pass c itself
   |
   = hint: pass c itself; declare it `var c` where a pointer is needed
```

**Fix.** A function that only reads takes the value:

```gala
package main

struct Counter(var N int)

func show(c Counter) string = s"count ${c.N}"

func main() {
    val c = Counter(0)
    Println(show(c))
}
```

A function that changes it, or a Go API that wants a `*T`, gets the address of
a `var`:

```gala
package main

struct Counter(var N int)

func bump(c *Counter) {
    c.N = c.N + 1
}

func main() {
    var c = Counter(0)
    bump(&c)
    Println(c.N) // 1
}
```

For a parameter, declare it `var n`. For a receiver, declare the method on a
pointer receiver (`func (c *Counter) ...`), which is already a pointer. For an
immutable field, declare the field `var`.

**Rationale.** A `val`, a parameter, a receiver and an immutable field are
immutable, and a pointer is the one way to change a value without naming it.
Earlier versions gave `&x` of such a value a read-only `ConstPtr[T]`, which no
Go API accepted where a `*T` was expected, copied the value on every field read
and could be nil. A value that is only read is passed itself: it is already
immutable.
