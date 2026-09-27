---
layout: default
title: "GALA-E0047 — If Takes No Initializer Statement"
description: "\"`if` takes no initializer statement\" — GALA-E0047 rejects Go's `if v, err := f(); err != nil` form and points at Try, which wraps a (T, error) call for you."
keywords: "gala-e0047, gala if initializer, gala if err != nil, gala try, gala error handling, golang if statement initializer"
permalink: /docs/errors/gala-e0047/
last_modified_at: 2026-09-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0047</p>

# GALA-E0047 — `if` takes no initializer statement

**What it means.** An `if` is written with Go's initializer statement — a binding, then `;`, then the condition. The construct is Go's, not GALA's.

---

## Code that triggers it

```gala
package main

import "strconv"

func isParsable(text string) bool {
    if n, err := strconv.Atoi(text); err != nil {
        Println(n)
        return false
    }
    return true
}

func main() {
    Println(isParsable("42"))
}
```

---

## Compiler message

```
error[GALA-E0047]: `if` takes no initializer statement
  --> main.gala:6:5
  |
6 |     if n, err := strconv.Atoi(text); err != nil {
  |     ^^ wrap the call in `Try(...)` and `match` on `Success(v)` / `F…
  |
  = hint: wrap the call in `Try(...)` and `match` on `Success(v)` / `Failure(e)` — a Go `(T, error)` return is auto-wrapped
```

---

## How to fix it

The shape this slot is reached for is almost always a Go `(T, error)` call being nil-tested, and `Try` wraps exactly that shape:

```gala
func parseOr(text string, fallback int) int =
    Try(strconv.Atoi(text)) match {
        case Success(n) => n
        case Failure(_) => fallback
    }
```

`GetOrElse` says the same thing when the failure is simply a fallback:

```gala
func parseOr(text string, fallback int) int =
    Try(strconv.Atoi(text)).GetOrElse(fallback)
```

A differently-placed nil comparison — `val err = f()` on the line above, then `if err != nil` — is not the fix: it trades one Go idiom for another and leaves the naked error pair in place. See [Error Handling](/features/error-handling/).

---

## Why the rule exists

Nothing ever lowered the initializer correctly. The binding was transformed after the condition that references it, so the generated Go compared the `std.Immutable[T]` wrapper rather than the value — an error naming a wrapper the author never wrote. A multi-value initializer lowered to a `var (...)` block, which Go does not accept there at all.

Only the initializer slot is rejected. A plain condition, `else`, `else if` chains, and the if-*expression* `if (cond) a else b` are untouched.

---

## Related

- [GALA-E0036](/docs/errors/gala-e0036/) — Go statement keywords such as `defer` and `switch`
- [GALA-E0035](/docs/errors/gala-e0035/) — Go builtins such as `len(...)`
- [Error Handling](/features/error-handling/)
- [All GALA error codes](/docs/errors/)
