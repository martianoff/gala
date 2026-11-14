---
layout: default
title: "GALA-E0110 — Mutating Pointer Method on a Value That Cannot Be Addressed"
description: "GALA-E0110 rejects a call of a pointer-receiver method that can change its receiver on a val, a val field, a call result or a literal, because the change would be made to a copy and lost."
keywords: "gala-e0110, gala pointer receiver val, gala Ptr receiver, gala mutating method val, gala lost mutation, gala var"
permalink: /docs/errors/gala-e0110/
last_modified_at: 2026-11-13
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0110</p>

# GALA-E0110 — mutating pointer method on a value that cannot be addressed

**When it fires.** A method with a pointer receiver (a `Ptr[T]` or `*T`
receiver, or a Go method declared on `*T`) that can change its receiver is
called on a value Go cannot address: a `val`, a field reached through a `val`
(fields are `val` unless marked `var`), a call result, a literal or a type
assertion. GALA can call a pointer method on such a value only through a fresh
copy, so whatever the method writes to its receiver would be written to the
copy and lost.

Printing calls a method too: `Println`, `Print`, `s"..."` and `f"..."` print a
value whose `String()` is declared on a pointer receiver by calling that
`String`, so printing such a value is rejected the same way when its `String`
changes its receiver.

**Minimal repro.**

```gala
package main

struct Counter(var n int)

func (c Ptr[Counter]) Bump() {
    c.n = c.n + 1
}

func main() {
    val c = Counter(0)
    c.Bump()
    Println(c.n)
}
```

**Error output.**

```
error[GALA-E0110]: cannot call Bump on this Counter: Bump can change its receiver, and this Counter cannot be addressed here, so the change would be made to a copy and lost
  --> main.gala:11:7
   |
11 |     c.Bump()
   |       ^^^^ declare it with `var`, or hold a `Ptr`
   |
   = hint: declare it with `var`, or hold a `Ptr`
```

**Fix.** Keep the value where Go can address it, so the method changes the
value itself. Declare it with `var`, or hold a `Ptr` to it, as `&Counter(0)`
gives:

```gala
package main

struct Counter(var n int)

func (c Ptr[Counter]) Bump() {
    c.n = c.n + 1
}

func main() {
    var c = Counter(0)
    c.Bump()
    Println(c.n) // 1

    val p = &Counter(0)
    p.Bump()
    Println(p.n) // 1
}
```

For a field, mark the field `var` and reach it through a `var` or a pointer.

**What counts as changing the receiver.** A method can change its receiver
when its body, or a lambda in it:

- assigns to the receiver or a place in it (`c.n = 1`, `c.n++`, `*c = T()`),
  or takes the address of one (`&c.n`);
- uses the receiver pointer itself as a value: passes it to a function,
  returns it, stores it or captures it in a lambda (comparing it with `==` or
  `!=` only reads it);
- calls, on its receiver or on a field it holds by value, a method that can
  change its receiver.

A write through a pointer, slice or map that a field holds is not a change to
the receiver: the copy holds the same pointer. A method that only reads its
receiver, such as `func (c Ptr[Counter]) Get() int = c.n`, or Go's
`(*url.URL).String` and `(*strings.Reader).Len`, is still called on a copy,
and that call is accepted. For a Go method the analysis reads the method's Go
source; a pointer method whose source is not found counts as changing its
receiver.

**Rationale.** A `val` never changes. Before this check, a pointer method that
changes its receiver ran on a hidden copy of the `val`, so the program
compiled and the change silently vanished: the repro above printed `0`.

**Related.** A value that must not be copied at all (a `sync.Mutex`, a
`strings.Builder`) is [GALA-E0053](/docs/errors/gala-e0053/) whatever the
method does.

---

## Related

- [Pointer-receiver methods on a `val`](/docs/language-reference/#pointer-receiver-methods-on-a-val)
- [All GALA error codes](/docs/errors/)
