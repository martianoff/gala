---
layout: default
title: "GALA-E0067 — Type Argument Cannot Be Inferred"
description: "\"cannot infer type argument T of Try\" — GALA-E0067 fires when nothing at a generic construction determines a type parameter, often because a Go package's types were not loaded."
keywords: "gala-e0067, gala cannot infer type argument, gala generic type without instantiation, gala Try type argument, gala go package types not loaded, gala mod add --go"
permalink: /docs/errors/gala-e0067/
last_modified_at: 2026-10-03
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0067</p>

# GALA-E0067 — Type argument cannot be inferred

**When it fires.** A generic type is constructed and nothing at the call
determines one of its type parameters: not the arguments, not the binding's
declared type, not the enclosing function's result type. It covers a generic
struct built from its fields (`Tag("x")`), a type called through its companion
`Apply` (`Try(x)`), and a partial type-argument list (`Mk[int](2, "c")`) whose
remaining parameters the arguments do not fix.

**Minimal repro.**

```gala
package main

struct Tag[T any](Name string)

func main() {
    val t = Tag("x")
    Println(t.Name)
}
```

**Error output.**

```
error[GALA-E0067]: cannot infer type argument T of generic struct Tag from its fields or the expected type; annotate the binding (e.g. `val x Tag[int] = Tag(...)`) or write it explicitly (`Tag[int](...)`)
  --> main.gala:6:17
  |
6 |     val t = Tag("x")
  |                 ^^^
  |
```

**Fix.** Say which type you mean, on the binding or on the call:

```gala
package main

struct Tag[T any](Name string)

func main() {
    val t Tag[int] = Tag("x")
    Println(t.Name)
}
```

**An argument whose type is unknown.** The same error fires when an argument
should have fixed the type parameter but has no type itself. The usual cause
is a call into a Go package whose type information GALA could not load, so the
result type of the call is unknown:

```gala
package main

import "golang.org/x/term"

func main() {
    Println(Try(term.MakeRaw(0)).IsSuccess())
}
```

```
error[GALA-E0067]: cannot infer type argument T of Try: the type of its argument `term.MakeRaw(...)` is unknown
  --> main.gala:6:17
  |
6 |     Println(Try(term.MakeRaw(0)).IsSuccess())
  |                 ^^^^ the type information of Go package "golang.org/x/term" could…
  |
  = hint: the type information of Go package "golang.org/x/term" could not be loaded; require its module in gala.mod (`gala mod add --go <module>`)
```

Require the Go module in the `gala.mod` of the module whose code imports it:

```
require golang.org/x/term v0.25.0 // go
```

`gala build` and `gala test` download a required Go module before they read
its types, for the project and for every GALA library it depends on, so this
form of the error means the module is not required, or could not be
downloaded (offline, or a private module without credentials); a failed
download is printed as a warning above the error.

**Rationale.** Go cannot use a generic type without its type arguments. A
companion `Apply` call used to be emitted as written —
`std.Try{}.Apply(term.MakeRaw(0))` — and the program failed in `go build`
with `cannot use generic type std.Try[T any] without instantiation`, a
message about generated Go rather than about the GALA line that caused it.

**What still works.** Any construction whose type arguments the arguments,
the declared type of the binding or the enclosing function's result type
determine: `Tag[int]("x")`, `val t Tag[int] = Tag("x")`, and
`Try(term.MakeRaw(fd))` once `golang.org/x/term` is required.
