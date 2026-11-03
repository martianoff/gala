---
layout: default
title: "GALA-E0090 — Anonymous Go Struct Type Would Have to Be Written"
description: "GALA-E0090 fires when the generated Go would have to write an anonymous Go struct type, such as the struct{} of ctx.Done()'s <-chan struct{}, as in Some(ctx.Done()). Use go_interop.Signal and its helpers, or name the type on the Go side."
keywords: "gala-e0090, gala chan struct{}, gala ctx.Done, gala anonymous struct type, gala signal channel, go_interop.Signal"
permalink: /docs/errors/gala-e0090/
last_modified_at: 2026-11-02
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0090</p>

# GALA-E0090 — Anonymous Go struct type would have to be written

**When it fires.** A Go API hands GALA a value whose type is, or holds, an
anonymous Go struct type, such as the `struct{}` of a signal channel
(`<-chan struct{}` from `ctx.Done()`), and the generated Go would have to
write that type out. That happens wherever GALA spells a type:

- a type argument, as in `Some(ctx.Done())` (`std.Some[<-chan struct{}]`)
  or a collection of such values;
- a type GALA infers for a generic call, a method such as `Map`, a lambda's
  parameter or result, or a var it declares to hold a branching value
  (`if`/`match` as an expression).

GALA has no syntax for an anonymous struct type, so it cannot be written in
GALA source either. A value of one that only passes through is accepted:
`val done = ctx.Done()`, a Go call's result passed straight to another Go
call, a receive such as `<-done`, and reading a field of a returned struct.

**Minimal repro.**

```gala
package main

import "context"

func main() {
    val ctx = context.Background()
    val done = Some(ctx.Done())
    Println(done.IsDefined())
}
```

**Error output.**

```
error[GALA-E0090]: type `<-chan struct{}` holds an anonymous Go struct type, which GALA cannot write
  --> main.gala:7:21
  |
7 |     val done = Some(ctx.Done())
  |                     ^^^ give the type a name on the Go side
  |
  = hint: give the type a name on the Go side; for a signal channel use go_interop.Signal and go_interop.WaitClosed, IsClosed or WaitClosedTimeout; for ctx.Done() use go_interop.WaitDone, IsDone or WaitDoneTimeout
```

**Fix.** Keep the value out of positions that write its type, or give the
type a name:

- for `ctx.Done()`, use the context helpers `go_interop.WaitDone(ctx)`,
  `go_interop.IsDone(ctx)` and `go_interop.WaitDoneTimeout(ctx, d)`;
- for any other signal channel, use `go_interop.WaitClosed(ch)`,
  `go_interop.IsClosed(ch)` and `go_interop.WaitClosedTimeout(ch, d)`, and
  make your own signal channels with `go_interop.NewSignal()`: a
  `go_interop.Signal` is a named type, so it fits in an `Option` or a
  collection, and Go APIs that take a `chan struct{}` or a `<-chan struct{}`
  accept it;
- for a struct with fields that your own Go code returns, declare it as a
  named type on the Go side (`type Result struct { ... }`).

```gala
package main

import (
    "context"

    "martianoff/gala/go_interop"
)

func main() {
    val ctx = context.Background()
    Println(go_interop.IsDone(ctx))
}
```

**Rationale.** GALA generates concrete Go types and never erases one to
`any`. An anonymous struct type has no GALA spelling to generate from, and
one with an unexported field cannot be written outside the package that
declares it at all, so GALA reports the use instead of writing a type the
program never named.

---

## Related

- [Go Interop: signal channels and anonymous struct types](/docs/go-interop/#signal-channels-and-anonymous-struct-types)
- [All GALA error codes](/docs/errors/)
