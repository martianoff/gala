---
layout: default
title: "GALA-E0070 — Read-Only Pointer Where an Interface Is Expected"
description: "GALA-E0070 fires when &x of a val, a read-only ConstPtr, is passed where an interface is expected. Declare the value var to pass a *T, or pass the value itself."
keywords: "gala-e0070, gala constptr, gala address of val, gala does not implement interface, gala pointer receiver interface, gala val vs var pointer"
permalink: /docs/errors/gala-e0070/
last_modified_at: 2026-10-04
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0070</p>

# GALA-E0070 — Read-only pointer where an interface is expected

**When it fires.** The address of an immutable binding — a `val`, a parameter
not declared `var`, a receiver — is a read-only `ConstPtr[T]`, not a `*T`
([ConstPtr](/docs/language-reference/#constptr---read-only-pointers)). `ConstPtr` has only its
own methods, `Deref` and `IsNil`, so it does not implement an interface whose
methods are declared on `T` or `*T`. The error fires when such a value fills a
slot of that interface type: a function or method argument (GALA or Go), a
typed `val` / `var`, an assignment, or a return value.

**Minimal repro.**

```gala
package main

struct Counter(var N int)

func (c *Counter) Notify(s string) {
    c.N = c.N + 1
}

type Notifier interface {
    Notify(s string)
}

func send(n Notifier) {
    n.Notify("x")
}

func main() {
    val c = Counter(0)
    send(&c)
    Println(c.N)
}
```

**Error output.**

```
error[GALA-E0070]: cannot use &c (ConstPtr[Counter]) as Notifier: a read-only ConstPtr does not implement Notifier (missing Notify)
  --> main.gala:19:10
   |
19 |     send(&c)
   |          ^^ &c is read-only because c is immutable
   |
   = hint: &c is read-only because c is immutable; declare it `var c` to pass a *Counter
```

**Fix.** A method with a pointer receiver may change the value, so it needs a
mutable one. Declare it `var`, and `&c` is a `*Counter`:

```gala
package main

struct Counter(var N int)

func (c *Counter) Notify(s string) {
    c.N = c.N + 1
}

type Notifier interface {
    Notify(s string)
}

func send(n Notifier) {
    n.Notify("x")
}

func main() {
    var c = Counter(0)
    send(&c)
    Println(c.N) // 1
}
```

When the type implements the interface with **value** receivers, the value
itself implements it too, and the hint says to pass `c` rather than `&c`.

**Rationale.** Before this code the call reached `go build`, which reported

```
cannot use std.NewConstPtr(c.Ptr()) (value of struct type std.ConstPtr[Counter]) as Notifier value in argument to send: std.ConstPtr[Counter] does not implement Notifier (missing method Notify)
```

naming a wrapper and a `Ptr()` call that are not in the source. Making
`ConstPtr` implement the interface is not an option: the methods in question
take a `*T` that may write through it, which is exactly what a read-only
pointer exists to prevent.

**Scope.** An empty interface (`any`) holds a `ConstPtr` like any other value,
and a `ConstPtr` passed where a `*T` is expected is left to Go's type check as
before.
