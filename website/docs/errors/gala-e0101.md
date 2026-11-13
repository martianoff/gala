---
layout: default
title: "GALA-E0101 — Error Return Discarded in a Void Lambda"
description: "GALA-E0101 fires when a lambda that returns nothing calls a function returning only an error as a statement, as in ForEach((n) => save(n)). Wrap the call in FromError."
keywords: "gala-e0101, gala discard error, gala void lambda error, gala fromerror, gala foreach error"
permalink: /docs/errors/gala-e0101/
last_modified_at: 2026-11-11
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0101</p>

# GALA-E0101 — Error return discarded in a void lambda

**When it fires.** A lambda that returns nothing, such as the callback of
`ForEach`, has a statement that calls a function returning only an `error`,
at any depth: in an `if`, a `match` arm or a block of the lambda too.
The callee can be a Go function, a GALA function or method, or a function
value. The lambda has no result to pass the error on in, so it would be
dropped without anyone seeing it.

**Minimal repro.**

```gala
package main

import "errors"

func save(n int) error = if (n < 0) errors.New(s"negative: $n") else nil

func main() {
    Some(-1).ForEach((n) => save(n))
}
```

**Error output.**

```
error[GALA-E0101]: cannot discard error return from save in void lambda
  --> main.gala:8:22
  |
8 |     Some(-1).ForEach((n) => save(n))
  |                      ^ use FromError(save(...)) to handle the error
  |
  = hint: use FromError(save(...)) to handle the error
```

**Fix.** Turn the error into a `Try[Void]` with `FromError` and handle it:

```gala
package main

import "errors"

func save(n int) error = if (n < 0) errors.New(s"negative: $n") else nil

func main() {
    Some(-1).ForEach((n) => {
        FromError(save(n)).OnFailure((err) => Println(s"save failed: $err"))
    })
}
```

**Rationale.** An `error` result is how a call reports that it failed. A
statement that drops it hides the failure, whichever language the callee is
written in. A generic method whose result is its type parameter, such as
`FoldLeft` with an `error` accumulator, returns a value rather than a failure
and is not rejected.

## Related

- [Discarding a value](/docs/language-reference/#discarding-a-value)
- [All GALA error codes](/docs/errors/)
