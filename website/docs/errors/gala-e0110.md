---
layout: default
title: "GALA-E0110 — Mutating Pointer Method on a Value That Cannot Be Addressed"
description: "GALA-E0110 rejects a call of a pointer-receiver method that can change its receiver on a val, a val field, a call result or a literal, because the change would be made to a copy and lost."
keywords: "gala-e0110, gala pointer receiver val, gala Ptr receiver, gala mutating method val, gala lost mutation, gala var"
permalink: /docs/errors/gala-e0110/
last_modified_at: 2026-11-14
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
changes its receiver. The `String` GALA generates for a struct or a sealed type
prints each field through a copy of the field, so printing a value with a field
whose pointer-receiver `String` changes its receiver is rejected too, whether
the value printed is a `var`, a `val` or a pointer (see "Fields printed through
a copy" below). A method that prints a field of its receiver this way, as
`Println(r.c)` or `s"${r.c}"` does, runs that `String` on the receiver's own
field, so it counts as changing its receiver. fmt prints a value by its
`Format`, then its `Error`, then its `String`: a value with a `Format` or an
`Error` of its own prints by that, and a pointer-receiver `Format` or `Error`
runs before a pointer-receiver `String`, so the method checked is the one
that runs.

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

**Fields printed through a copy.** A shorthand struct or a sealed type prints
by the `String` GALA generates for it, `Holder(shown 1)`. That `String` has a
value receiver and prints each field through a copy of the field, so a field
whose pointer-receiver `String` changes its receiver would have the change made
to the copy, even when the struct is a `var`:

```gala
package main

struct Counter(var shown int)

func (c Ptr[Counter]) String() string {
    c.shown = c.shown + 1
    s"shown ${c.shown}"
}

struct Holder(var c Counter)

func main() {
    var h = Holder(Counter(0))
    Println(h)
    Println(h.c.shown)
}
```

```
error[GALA-E0110]: cannot print this Holder: its field c, a Counter, prints by its String on a copy, and that String can change its receiver, so the change would be made to the copy and lost
  --> main.gala:14:13
   |
14 |     Println(h)
   |             ^ hold field c as a `Ptr[Counter]`, so its String runs on the…
   |
   = hint: hold field c as a `Ptr[Counter]`, so its String runs on the value it points to
```

The error is reported where `Println`, `Print` or interpolation prints the
value, and follows fields into nested structs and sealed cases. A value fmt
prints by its own `Error` or `Format` never runs its generated `String`, so it
is not checked. Hold the field as a `Ptr`, so its `String` runs on the value it
points to:

```gala
package main

struct Counter(var shown int)

func (c Ptr[Counter]) String() string {
    c.shown = c.shown + 1
    s"shown ${c.shown}"
}

struct Tracked(c Ptr[Counter])

func main() {
    var shared = Counter(0)
    val t = Tracked(&shared)
    Println(t)            // Tracked(shown 1)
    Println(shared.shown) // 1
}
```

**Not checked.** The check runs where `Println`, `Print` or interpolation
prints a value of a struct or sealed type. It does not cover the other ways the
generated `String` runs, each of which already holds a copy: an element a
collection or a tuple prints, a value of a type parameter, of `any` or of
another interface (storing a value in an interface copies it), an explicit
`h.String()` call (the generated `String` has a value receiver) and Go's `fmt`.
A change a field's `String` makes there is lost too. A `String` that only reads
its receiver, or a field held by `Ptr`, avoids it everywhere.

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
- [Ownership guide](/docs/ownership/#the-rules-and-how-to-fix-each-error): the related `inout` rules
- [All GALA error codes](/docs/errors/)
