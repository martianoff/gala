---
layout: default
title: "GALA-E0132 — Go Multiple Return"
description: "GALA-E0132 fires on a Go multiple return, return a, b. A GALA function returns one value: a tuple, or a Try for (T, error)."
keywords: "gala-e0132, gala multiple return, gala return a b, gala tuple return, gala try return, gala Success Failure"
permalink: /docs/errors/gala-e0132/
last_modified_at: 2026-11-15
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0132</p>

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
([porting guide](/docs/porting-from-go/#t-error-try-or-gos-result-list)).

**Rationale.** The parser used to read `return n / 2` and then stop at the
comma, and its recovery lost the function's closing brace:
`mismatched input '<EOF>'` at the end of the file. It is now this error, at
the `return`.

**Scope.** Named results, `func f() (n int, err error)`, are
[GALA-E0130](/docs/errors/gala-e0130/).

---

## Related

- [Porting from Go](/docs/porting-from-go/)
- [All GALA error codes](/docs/errors/)
