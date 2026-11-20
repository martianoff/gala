---
layout: default
title: "GALA-E0137 — Placeholder Inside an Interpolated String"
description: "GALA-E0137 fires when a _ is read as a value inside the ${...} of an s-string or f-string, as in xs.Map(s\"v${_}\"): a placeholder lambda takes only the _ written outside string literals. Write an explicit lambda."
keywords: "gala-e0137, gala placeholder lambda, gala underscore in string interpolation, gala s-string underscore, cannot use _ as value"
permalink: /docs/errors/gala-e0137/
last_modified_at: 2026-11-20
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0137</p>

# GALA-E0137 — `_` inside an interpolated string

**When it fires.** A `_` is read as a value inside the `${...}` of an
interpolated string, `s"..."` or `f"..."`. A placeholder lambda takes the `_`
written outside string literals only, so a `_` inside an interpolation is not
one of its parameters, and nothing else gives it a value. It fires wherever
the `_` sits in the interpolated expression (`${_}`, `${_ + 1}`,
`${_.Name}`), in nested strings too, and whether or not the argument the string
is passed as wants a function. Reading a `_` that a lambda parameter or a typed
pattern written in the same `${...}` binds, as in `s"${xs.Map((_) => _ + 1)}"`,
is reported too: such a `_` names no value.

**Minimal repro.**

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2)
    Println(xs.Map(s"v${_}"))
}
```

**Error output.**

```
error[GALA-E0137]: `_` inside an interpolated string is not a placeholder: a placeholder lambda takes only the `_` written outside the string
  --> main.gala:7:25
  |
7 |     Println(xs.Map(s"v${_}"))
  |                         ^ write an explicit lambda
  |
  = hint: write an explicit lambda; name its parameter and use the name inside the string: `(x) => s"v${x}"`
```

**Fix.** Write the lambda with a named parameter, and use the name inside the
string:

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    val xs = ArrayOf(1, 2)
    Println(xs.Map((x) => s"v${x}"))
}
```

A `_` that something inside the interpolation gives a meaning is fine: a
placeholder lambda written whole inside it, as in `s"${xs.Map(_ * 10)}"`, a
wildcard pattern, as in `s"${opt match { case Some(_) => "some" case _ => "none" }}"`,
and a `_` parameter or `case _: int` that is not read. Where a `_` that a
parameter or typed pattern in the same `${...}` binds is read, bind a name
instead of `_` and read that name: `(x) => s"v${x}"`, `case n: int => s"${n}"`.

**Rationale.** A placeholder lambda's parameters are the `_` of the argument
as written, and the text of a string literal is not part of that. Reading a
`_` inside `${...}` as a parameter of a lambda around the string would make
the shape of the lambda depend on what is inside string literals. GALA
reports the `_` instead, at the place it is written.

## Related

- [Placeholder Lambda Shorthand](/docs/language-reference/#placeholder-lambda-shorthand)
- [All GALA error codes](/docs/errors/)
