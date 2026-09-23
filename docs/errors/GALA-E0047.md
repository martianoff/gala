# GALA-E0047 — `if` takes no initializer statement

**When it fires.** An `if` is written with Go's initializer statement — a
binding, then `;`, then the condition:

```gala
if n, err := strconv.Atoi(text); err != nil {
    Println(n)
    return false
}
```

**Minimal repro.**

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

**Error output.**

```
error[GALA-E0047]: `if` takes no initializer statement
  --> main.gala:6:5
  |
6 |     if n, err := strconv.Atoi(text); err != nil {
  |     ^^ wrap the call in `Try(...)` and `match` on `Success(v)` / `F…
  |
  = hint: wrap the call in `Try(...)` and `match` on `Success(v)` / `Failure(e)` — a Go `(T, error)` return is auto-wrapped
```

**Fix.** The shape this slot is reached for is almost always a Go `(T, error)`
call being nil-tested, and `Try` wraps exactly that shape — a `(T, error)`
return is auto-wrapped into `Success` / `Failure`:

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

For a call that returns a bare `error`, `Option` and `Either` carry the same
weight; see [Error Handling](../GALA.MD#8-error-handling). What the fix is
*not* is a differently-placed nil comparison — `val err = f()` on the line
above, then `if err != nil`, trades one Go idiom for another and leaves the
naked error pair in place.

**Rationale.** The initializer slot is in the grammar —
`ifStatement: 'if' (simpleStatement ';')? expression block` — but the construct
is Go's, not GALA's. It appears in no part of this language reference, no
stdlib source, no example and no test, and nothing ever lowered it correctly.
The binding was transformed *after* the condition and the body that reference
it, so the condition was built against a scope in which the name did not yet
exist and compared the `std.Immutable[T]` wrapper rather than the value:

```
main.gala:6: invalid operation: err != nil (mismatched types std.Immutable[error] and untyped nil)
```

That names a wrapper the author never wrote, in generated code they never read.
A multi-value initializer fared worse still: it lowers to a `var (...)` block,
which Go does not accept as an if-initializer at all, so the generated file
would not even parse.

Rejecting keeps the statement surface as narrow as it already is for `defer`,
`switch`, `goto` and the other Go-only forms GALA turns away
([GALA-E0036](GALA-E0036.md)).

**Where it stands down.** Only the initializer slot is rejected. Every other
`if` form is untouched — a plain condition, an `else` block, an `else if`
chain, and the if-*expression* `if (cond) a else b`, whose parenthesized
condition and mandatory `else` make it a value:

```gala
val status = if (score > 50) "pass" else "fail"
```

**Scope.** This code covers the `if` initializer. Bare Go statement keywords
(`defer`, `go`, `switch`, `goto`, `select`, `fallthrough`) are
[GALA-E0036](GALA-E0036.md); bare Go builtins such as `len(...)` are
[GALA-E0035](GALA-E0035.md).
