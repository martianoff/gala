# GALA-E0015 — Bare `return` inside a match branch used as a value

**When it fires.** A `match` expression is used as a value (its result is
assigned to a variable, returned from a function as an expression, or passed
as an argument) and one of its branches ends with a bare `return` — a
`return` statement with no value.

**Minimal repro.**

```gala
package main

import "os"

func run(path string) {
    val data = Try(() => os.ReadFile(path)) match {
        case Success(b)   => string(b)
        case Failure(err) => {
            Println(s"error: ${err.Error()}")
            return                     // <- bare return in a value-match branch
        }
    }
    Println(data)
}
```

**Error output.**

```
[SemanticError GALA-E0015] main.gala:10:31 bare `return` inside a match branch whose result is used as a value
```

**Why it is an error.** GALA transpiles a match-as-value into a Go
immediately-invoked function expression (IIFE):

```go
data := func(obj ...) string {
    switch ... {
    case Success:  return string(b)
    case Failure:  fmt.Println(...); return   // <- invalid: IIFE must return string
    }
}(...)
```

A bare `return` inside that IIFE does not exit the enclosing function — it
exits the IIFE — and because the IIFE has a non-void return type, the Go
compiler rejects it with *"not enough return values"*.

**Fix.** Pick whichever restructuring best matches the intent.

1.  **Early-exit before the match** — test the failure case first and
    `return` from the outer function, then destructure the success case:

    ```gala
    func run(path string) {
        val result = Try(() => os.ReadFile(path))
        if (result.IsFailure()) {
            Println(s"error: ${result.GetError().Error()}")
            return
        }
        Println(string(result.Get()))
    }
    ```

2.  **Use combinators** — `Recover`, `Map`, `GetOrElse`, etc. avoid the IIFE
    entirely and compose cleanly:

    ```gala
    func run(path string) {
        val data = Try(() => os.ReadFile(path))
            .Map((b) => string(b))
            .GetOrElse("")
        if (data == "") { return }
        Println(data)
    }
    ```

3.  **Return from a `match` used as the function body** — if the match
    itself is the function's last expression, each branch can legitimately
    `return` a value (including `return` on its own when the function is
    `Unit`/void).

**Rationale.** GALA's match-as-value form has expression semantics: every
branch must contribute a value of the match's result type. A bare `return`
is a *statement-level* control-flow operation whose scope (outer function
vs. enclosing IIFE) is ambiguous without extra machinery. Rather than pick
one interpretation silently and generate surprising Go, the transpiler
rejects the construct and points to the explicit rewrites above.
