---
layout: default
title: "GALA-E0015 — Bare return Inside a Value-Producing Match"
description: "GALA-E0015 fires when a match used as a value has a branch ending in a bare `return`. Learn why the generated function cannot return nothing, and three ways to restructure the code."
keywords: "gala-e0015, bare return inside match, gala match as value, gala return in match branch, gala match expression error, gala early exit"
permalink: /docs/errors/gala-e0015/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0015</p>

# GALA-E0015 — Bare `return` inside a value-producing match

**What it means.** A `match` expression is used as a value — assigned to a variable, returned as an expression, or passed as an argument — and one of its branches ends with a bare `return` (a `return` with no value).

---

## Code that triggers it

```gala
package main

import "os"
import . "martianoff/gala/std"

func run(path string) {
    val data = Try(os.ReadFile(path)) match {
        case Success(b)   => string(b)
        case Failure(err) => {
            Println(s"error: ${err.Error()}")
            return                     // bare return in a value-producing branch
        }
    }
    Println(data)
}
```

---

## Compiler message

```
error[GALA-E0015]: bare `return` inside a match branch whose result is used as a value
  --> e0015.gala:7:16
  |
7 |     val data = Try(os.ReadFile(path)) match {
  |                ^^^ the match is wrapped in a function that must return string
  |
  = hint: the match is wrapped in a function that must return string; restructure to early-exit before the match, or use combinators like .Recover / .GetOrElse. See docs/errors/GALA-E0015.md
```

The caret marks the match being used as a value, and the hint names the result type its wrapper must produce.

---

## Why the generated Go cannot work

A match-as-value lowers to an immediately-invoked function expression:

```go
data := func(obj ...) string {
    switch ... {
    case Success: return string(b)
    case Failure: fmt.Println(...); return   // invalid: must return string
    }
}(...)
```

A bare `return` there does not exit the enclosing function — it exits the wrapper. Because the wrapper has a non-void return type, Go rejects it with *"not enough return values"*.

---

## How to fix it

**1. Early-exit before the match.** Handle the failure first, then destructure the success case:

```gala
func run(path string) {
    val result = Try(os.ReadFile(path))
    if (result.IsFailure()) {
        Println(s"error: ${result.GetError().Error()}")
        return
    }
    Println(string(result.Get()))
}
```

**2. Use combinators.** `Map`, `Recover`, and `GetOrElse` avoid the wrapper entirely:

```gala
func run(path string) {
    val data = Try(os.ReadFile(path))
        .Map((b) => string(b))
        .GetOrElse("")
    if (data == "") { return }
    Println(data)
}
```

**3. Make the match the function body.** When the match *is* the function's result, each branch legitimately returns a value.

---

## Why the rule exists

GALA's match-as-value form has expression semantics: every branch must contribute a value of the match's result type. A bare `return` is a statement-level control-flow operation whose scope — outer function or enclosing wrapper — is ambiguous. Rather than silently pick one reading and generate surprising Go, the transpiler rejects the construct and points at the explicit rewrites.

---

## Related

- [Error Handling](/features/error-handling/) — `Try`, `Option`, and `Either` combinators
- [Pattern Matching](/features/pattern-matching/) — match as an expression
- [All GALA error codes](/docs/errors/)
