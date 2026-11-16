# GALA-E0132 — Go multiple return

**When it fires.** A `return` gives several values, `return a, b`, as Go
returns a result list. A GALA function returns one value.

**Minimal repro.**

```gala
package main

func split(n int) (int, int) {
    return n / 2, n % 2
}

func main() {
    Println(split(7))
}
```

**Error output.**

```
error[GALA-E0132]: Go multiple return `return n / 2, n % 2`: a GALA function returns one value
  --> main.gala:4:5
  |
4 |     return n / 2, n % 2
  |     ^^^^^^^^^^^^^^^^^^^ return one value
  |
  = hint: return one value; here, `return (n / 2, n % 2)`, a tuple for a Go result list `(A, B)` and a `Try` for `(T, error)`
```

**Fix.** Return one value. For a Go result list `(A, B)` it is a tuple:

```gala
package main

func split(n int) (int, int) {
    return (n / 2, n % 2)
}

func main() {
    Println(split(7))
}
```

For `(T, error)` it is a `Try`: Go's `return v, nil` is `return Success(v)`
and `return 0, err` is `return Failure(err)`. The hint picks the form from
the values written: a last value `nil` gives `Success`, a last value that
reads as an error (`err`, `errors.New(...)`, `fmt.Errorf(...)`) gives
`Failure`, and any other gives a tuple
([porting guide](../PORTING_FROM_GO.MD#t-error-try-or-gos-result-list)).

**Rationale.** The parser used to read `return n / 2` and then stop at the
comma, and its recovery lost the function's closing brace:
`mismatched input '<EOF>'` at the end of the file. It is now this error, at
the `return`.

**Scope.** Named results, `func f() (n int, err error)`, are
[GALA-E0130](GALA-E0130.md).
