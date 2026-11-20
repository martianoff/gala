---
layout: default
title: "GALA-E0134 — Go-Results Body of the Wrong Type"
description: "GALA-E0134 fires when a function, method or lambda Go calls with (T, error) results gives a value that is neither a Try[T] nor the Tuple[T, error] that go_interop's ResultsOf makes, or gives both kinds in one body."
keywords: "gala-e0134, gala (T, error) method, gala implement io.Reader, gala ResultsOf, gala go_interop ResultsOf, gala Try returned to Go, gala cannot use std.Tuple as std.Try"
permalink: /docs/errors/gala-e0134/
last_modified_at: 2026-11-16
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0134</p>

# GALA-E0134 — Go-results body of the wrong type

**When it fires.** A function, method, lambda or by-name argument that Go calls with a result
list ending in `error`, such as `(int, error)`, gives a value that cannot
become those results. Its body gives one of two values:

- a `Try[T]`, the default: a success gives Go the value and a `nil` error, a
  failure gives Go the zero value with the error;
- a `Tuple[T, error]` of the results as they are, which `ResultsOf` from
  `go_interop` makes from a Go call: Go receives both, a value returned with
  an error included.

A value of another type is this error — a `Try` or `Tuple` whose element
types the results cannot hold among them, or a `Try` of another element
type (`Try` has no covariance: a `Try[*os.File]` is no `Try[io.Reader]`,
though a Go call that is the whole expression body or by-name argument passes
its results through, so `= os.Open(p)` fills `(io.Reader, error)`) —
and so is a body that gives a `Try` in one place and the `Tuple` in another.
A result list without an `error`, such as `(int, int)`, has one value, the
`Tuple` of its results, and a value of another type is this error too. Each
value is checked where it is written (a `return`, the trailing value, each
`if` branch and `match` arm), and the same holds for a lambda or an argument
passed where Go expects a `func() (T, error)`. That includes a generic Go
function whose results are still open (`sync.OnceValues`, a
`func() (T, error)` parameter of a generic callee): the first value fixes
the open results, and the other values are checked against them.

A call of the function itself is its `Try`, like any call of it: in a body
that gives the `Tuple`, write it `ResultsOf(down(k - 1))`, and the hint says
so, also for a `val` bound to such a call.

**Minimal repro.**

```gala
package main

import "strconv"

func parse(s string) (int, error) {
    val n = strconv.Atoi(s).GetOrElse(0)
    n * 2
}

func main() {
    Println(parse("21"))
}
```

**Error output.**

```
error[GALA-E0134]: function parse gives Go the results (int, error), so its value is a Try[int] (on a failure Go receives the zero value with the error) or a Tuple[int, error] of the results as they are; this value is of type int
  --> main.gala:7:5
  |
7 |     n * 2
  |     ^^^^^ give a Try[int] or a Tuple[int, error]
  |
  = hint: give a Try[int] or a Tuple[int, error]; `Success(v)` or `Failure(err)` make the Try, and `ResultsOf(call)` from go_interop keeps both results of a Go call
```

**Fix.** Give the `Try`. A Go call with the same results already is one, and
`Map` keeps it one:

```gala
package main

import "strconv"

func parse(s string) (int, error) = strconv.Atoi(s).Map((n) => n * 2)

func main() {
    Println(parse("21"))
}
```

When Go's value matters with its error, as for an `io.Reader` that returns
the last bytes with `io.EOF`, give the `Tuple` of both results with
`ResultsOf`:

```gala
package main

import (
    "io"
    "strings"
    . "martianoff/gala/go_interop"
)

struct Wrap(inner io.Reader)

func (w Wrap) Read(p []byte) (int, error) {
    ResultsOf(w.inner.Read(p))
}

func main() {
    Println(io.ReadAll(Wrap(strings.NewReader("hello"))).Map((b) => string(b)))
}
```

A body gives one kind of value: `return Success(0)` in a body that ends in
`ResultsOf(...)` is this error too. Write the early return as a tuple,
`return (0, io.EOF)`.

**Rationale.** The body is lowered to Go against the value the results make,
so a value of another type used to fail `go build` against the generated
code: `cannot use std.Tuple[int, error]{…} as std.Try[int] value in return
statement`. It is now this error, at the value.

**Scope.** The check reads the value's type. A value whose type the
transpiler cannot resolve, an interface or a type parameter is left to Go,
and so is a function value passed as a by-name argument. A constructor takes
its type arguments from the result it fills: `Success(s)` in a body giving
Go `(int, error)` is a `Try[int]`, and Go reports its string argument, as it
does in any function returning `Try[int]`.

---

## Related

- [Go Interop](/features/go-interop/)
- [Error Handling](/features/error-handling/)
- [All GALA error codes](/docs/errors/)
