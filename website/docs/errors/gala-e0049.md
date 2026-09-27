---
layout: default
title: "GALA-E0049 — A Go Call's Try or Tuple Used as Its Plain Value"
description: "\"os.ReadFile(...) can fail, so it produces Try[[]byte]\" — a Go function returning several results is one GALA value, a Try or a Tuple; GALA-E0049 names the call when that value is used where the plain result is expected."
keywords: "gala-e0049, gala go multiple return values, gala try go error, gala os.ReadFile try, gala tuple go function, gala (T, error)"
permalink: /docs/errors/gala-e0049/
last_modified_at: 2026-09-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0049</p>

# GALA-E0049 — a Go call's Try or Tuple used as its plain value

**When it fires.** A Go function that returns several results is one GALA
value: `(T, error)` is a `Try[T]`, `(A, B)` a `Tuple[A, B]`, `(A, B, error)` a
`Try[Tuple[A, B]]` (see [Go functions that return several
results](/docs/language-reference/#go-functions-that-return-several-results)). The error fires
when that value — the call itself, or a name bound to it — is used where the
call's plain first result is expected:

```gala
import (
    "net/http"
    "os"
    "strconv"
)

func count(data []byte) int = data.Size()

val data = os.ReadFile("notes.txt")   // data is a Try[[]byte]
count(data)                           // count takes a []byte
string(data)                          // conversion
data[0]                               // index
val n = strconv.Atoi("42")
n + 1                                 // operand
val resp = http.Get("https://example.com")
resp.StatusCode                       // member of the plain value
val (bytes, err) = os.ReadFile("notes.txt") // a Try is not a Tuple
```

It also fires for a Go call returning more than ten values, which no Tuple
holds.

**Minimal repro.**

```gala
package main

import "os"

func count(data []byte) int = data.Size()

func main() {
    val data = os.ReadFile("notes.txt")
    Println(count(data))
}
```

**Error output.**

```
error[GALA-E0049]: `data` holds the result of `os.ReadFile(...)`, which can fail, so it is a `Try[[]byte]`; `[]byte` is expected here
  --> main.gala:9:19
  |
9 |     Println(count(data))
  |                   ^^^^ take the value with `.Get()`
  |
  = hint: take the value with `.Get()` (panics on failure), `.GetOrElse(default)`, or `match { case Success(v) => ... case Failure(e) => ... }`; or bind the results Go-style: `val v, err = os.ReadFile(...)`
```

**Fix.** Decide what a failure means, and say it with the Try:

```gala
import "os"

func count(data []byte) int = data.Size()

// Handle both outcomes
val total = os.ReadFile("notes.txt") match {
    case Success(data) => count(data)
    case Failure(_) => 0
}

// A fallback value
val orZero = os.ReadFile("notes.txt").Map((data) => count(data)).GetOrElse(0)

// Stop the program on failure — the error is the panic
val data = os.ReadFile("notes.txt").Get()
```

To handle the results the Go way, bind them by name without parentheses; each
name gets the plain Go result:

```gala
val data, err = os.ReadFile("notes.txt")
```

For a Go call without an error result, the value is a Tuple: destructure it
with `val (a, b, c) = strings.Cut(line, "=")`, or read `.V1`, `.V2`.

**Rationale.** GALA expressions are one value. Before, a single name over a
`(T, error)` call silently took the value and panicked on the error, and every
other single-value position either failed in `go build` on generated code or
typed the call as its first result. Presenting the call as a Try (or a Tuple)
everywhere gives one meaning in every position, and makes the possibility of
failure part of the type. Code written for the old reading fails here, at
compile time and with the call named, wherever the plain value is needed.

**Where it stands down.** A slot that can hold the Try or Tuple — a
`Try[[]byte]` parameter, `any`, an interface, a type parameter — takes it as
is: `fmt.Println(strconv.Atoi("7"))` prints `Success(7)`. The sole argument of
a Go function whose parameters take the results one for one
(`template.Must(tmpl.Parse(text))`) is passed as Go's raw results, and so are
the results of a multi-name binding. A statement whose value is not used calls
the Go function as it is.

**Scope.** A Go function returning only an `error` gives that error as a
value; wrap the call in `Try(...)` (or `FromError(...)`) to treat a non-nil
error as a failure.
