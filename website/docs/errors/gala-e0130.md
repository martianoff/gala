---
layout: default
title: "GALA-E0130 — Go Named Results"
description: "GALA-E0130 fires on Go named results, func f() (n int, err error). GALA results have no names."
keywords: "gala-e0130, gala named results, gala named return, gala naked return, gala result list"
permalink: /docs/errors/gala-e0130/
last_modified_at: 2026-11-15
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0130</p>

# GALA-E0130 — Go named results

**When it fires.** A function names its results, as Go allows:
`func f() (n int, err error)`, or one named result, `func f() (n int)`. GALA
results have no names.

**Minimal repro.**

```gala
package main

func half(n int) (q int) {
    q = n / 2
}

func main() {
    Println(half(8))
}
```

**Error output.**

```
error[GALA-E0130]: Go named results `(q int)`: GALA results have no names
  --> main.gala:3:18
  |
3 | func half(n int) (q int) {
  |                  ^^^^^^^ write the result types alone
  |
  = hint: write the result types alone; here, `int`, with the body giving the value rather than setting names
```

**Fix.** Write the result types alone, and have the body give the result as
its value rather than set a name: the last expression of the body, or a
`return`:

```gala
package main

func half(n int) int = n / 2

func main() {
    Println(half(8))
}
```

Several results are a Go result list, `(int, error)`. Inside GALA such a
function's result is one value: a `Try` for `(T, error)`, returned as
`Success(v)` or `Failure(err)`, or a tuple for `(A, B)`
([porting guide](/docs/porting-from-go/#t-error-try-or-gos-result-list)).

**Rationale.** The parser used to stop at the result list with `mismatched
input '(' expecting {'=', '{'}`. It is now this error, whose hint spells the
list without its names.

**Scope.** A Go `return a, b` in the body is
[GALA-E0132](/docs/errors/gala-e0132/).

---

## Related

- [Porting from Go](/docs/porting-from-go/)
- [All GALA error codes](/docs/errors/)
