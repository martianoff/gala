# GALA-E0015 — Bare `return` inside a match branch used as a value

**When it fires.** A `match` expression is used as a value (passed as an
argument, used as an operand, returned from a function as an expression) and
one of its branches ends with a bare `return` — a `return` statement with no
value. A match a local `val` or `var` is initialized with, or a variable is
assigned, is not such a value:
it is lowered as statements, and a bare `return` in it leaves the enclosing
function. That is fine when the function returns nothing; when it returns a
value, a bare `return` there is GALA-E0015 too — give the `return` a value.

**Minimal repro.** (`main.gala`)

```gala
package main

import "os"

func run(path string) {
    Println(Try(os.ReadFile(path)) match {
        case Success(b)   => string(b)
        case Failure(err) => {
            Println(s"error: ${err.Error()}")
            return
        }
    })
}

func main() {
    run("missing.txt")
}
```

**Error output.** The caret sits on the head of the match subject (`Try`),
not on the offending `return`: the diagnosis is about the match expression as
a whole, and the hint names the type the wrapping function must return.

```text
error[GALA-E0015]: bare `return` inside a match branch whose result is used as a value
  --> main.gala:6:13
  |
6 |     Println(Try(os.ReadFile(path)) match {
  |             ^^^ the match is wrapped in a function that must return string
  |
  = hint: the match is wrapped in a function that must return string; in a function that returns nothing, initialize a `val` with the match first (a `return` in it then leaves the function), restructure to early-exit before the match, or use combinators like .Recover / .GetOrElse. See docs/errors/GALA-E0015.md
```

The `-->` line echoes the source path as the compiler resolved it; the CLI
prints it absolute.

**Why it is an error.** GALA transpiles a match-as-value into a Go
immediately-invoked function expression (IIFE):

```go
fmt.Println(func(obj ...) string {
    switch ... {
    case Success:  return string(b)
    case Failure:  fmt.Println(...); return   // <- invalid: IIFE must return string
    }
}(...))
```

A bare `return` inside that IIFE does not exit the enclosing function — it
exits the IIFE — and because the IIFE has a non-void return type, the Go
compiler rejects it with *"not enough return values"*.

**Fix.** Pick whichever restructuring best matches the intent.

1.  **Initialize a `val` with the match** — the match is then lowered as
    statements, and the bare `return` leaves `run`:

    ```gala
    func run(path string) {
        val data = Try(os.ReadFile(path)) match {
            case Success(b)   => string(b)
            case Failure(err) => {
                Println(s"error: ${err.Error()}")
                return
            }
        }
        Println(data)
    }
    ```

2.  **Early-exit before the match** — test the failure case first and
    `return` from the outer function, then destructure the success case:

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

3.  **Use combinators** — `Recover`, `Map`, `GetOrElse`, etc. avoid the IIFE
    entirely and compose cleanly:

    ```gala
    func run(path string) {
        val data = Try(os.ReadFile(path))
            .Map((b) => string(b))
            .GetOrElse("")
        if (data == "") { return }
        Println(data)
    }
    ```

4.  **Return from a `match` used as the function body** — if the match
    itself is the function's last expression, each branch can legitimately
    `return` a value (including `return` on its own when the function is
    void).

**Rationale.** GALA's match-as-value form has expression semantics: every
branch must contribute a value of the match's result type. A bare `return`
is a *statement-level* control-flow operation whose scope (outer function
vs. enclosing IIFE) is ambiguous where the match is lowered to an IIFE; a
match a `val` is initialized with is lowered as statements instead. Rather than pick
one interpretation silently and generate surprising Go, the transpiler
rejects the construct and points to the explicit rewrites above.
