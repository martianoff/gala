---
layout: default
title: "GALA-E0116 — inout Interface Method Implemented Without an inout Receiver"
description: "GALA-E0116 rejects a value filling an interface that declares an inout method when its type implements it without an inout receiver: a Ptr or value receiver, or a Go method not on the no-retain allow-list."
keywords: "gala-e0116, gala inout, gala ownership, gala exclusivity"
permalink: /docs/errors/gala-e0116/
last_modified_at: 2026-11-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0116</p>

# GALA-E0116 — `inout` interface method implemented without an `inout` receiver

**When it fires.** An interface may declare a method `inout`:

```gala
type Stepper interface {
    inout Step() int
}
```

A value fills such an interface only when its type implements each `inout`
method with an `inout` receiver, `func (inout c Counter) Step() int`. The
error fires when the method has another receiver (a `Ptr`, a `*T` or a value
receiver), or when the type is declared in Go. A Go type is accepted only
when its method is on the compiler's no-retain allow-list (`bytes.Buffer`'s
`WriteString`, for example), whose entries are checked against the Go
compiler's escape analysis.

An interface value that is already an interface, built in GALA or received
from Go, fills it as it is: its methods are trusted, not checked.

**Minimal repro.**

```gala
package main

type Stepper interface {
    inout Step() int
}

struct Counter(var n int)

func (c Ptr[Counter]) Step() int {
    c.n = c.n + 1
    c.n
}

func run(s Stepper) int = s.Step()

func main() {
    Println(run(&Counter(0)))
}
```

**Error output.**

```
error[GALA-E0116]: Counter cannot fill Stepper: Stepper declares Step inout, and Counter.Step has no inout receiver
  --> main.gala:17:17
   |
17 |     Println(run(&Counter(0)))
   |                 ^^^^^^^^^^^ declare it `func
   |
   = hint: declare it `func (inout r Counter) Step(...)`
```

**Fix.** Declare the method with an `inout` receiver:

```gala
package main

type Stepper interface {
    inout Step() int
}

struct Counter(var n int)

func (inout c Counter) Step() int {
    c.n = c.n + 1
    c.n
}

func run(s Stepper) int = s.Step()

func main() {
    Println(run(&Counter(0))) // 1
}
```

To use a Go type, wrap it in a GALA type whose method has an `inout`
receiver.

**Rationale.** An `inout` method borrows its receiver for the call: it keeps
no reference to it and nothing else reaches it meanwhile. A `Ptr`-receiver
method or a Go method makes no such promise, so code calling the interface's
`inout` method could not rely on it.

---

## Related

- [`inout` parameters](/docs/language-reference/#inout-parameters)
- [All GALA error codes](/docs/errors/)
