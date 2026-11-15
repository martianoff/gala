---
layout: default
title: "GALA-E0117 — Go Function Literal"
description: "GALA-E0117 fires when a function value is written as a Go function literal, func(x int) int { ... }. GALA writes a function value as a lambda: (x int) int => { ... }."
keywords: "gala-e0117, gala func literal, gala anonymous function, gala closure, gala lambda, gala no viable alternative func, gala go function literal"
permalink: /docs/errors/gala-e0117/
last_modified_at: 2026-11-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0117</p>

# GALA-E0117 — Go function literal

**When it fires.** A function value is written as a Go function literal,
`func(params) result { body }`: as a struct literal's field, an argument, a
`val` or `var` initializer, a `return` value, or called in place. GALA writes
a function value as a lambda.

**Minimal repro.**

```gala
package main

type Command struct {
    Use string
    RunE func(*Command, []string) error
}

func main() {
    val cmd = &Command{
        Use: "checksum",
        RunE: func(*Command, []string) error {
            return nil
        },
    }
    Println(cmd.Use)
}
```

**Error output.**

```
error[GALA-E0117]: Go function literal: a function value in GALA is a lambda
  --> main.gala:11:15
   |
11 |         RunE: func(*Command, []string) error {
   |               ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ write it as a lambda
   |
   = hint: write it as a lambda; here, `(_ *Command, _ []string) error => { ... }` with the body unchanged
```

**Fix.** Drop `func`, keep the parameters and the result type, and put `=>`
before the body. The body carries over unchanged: a block lambda may `return`,
and it returns its last expression without one. Each parameter has a name; an
unnamed Go parameter becomes `_`:

```gala
package main

type Command struct {
    Use string
    RunE func(*Command, []string) error
}

func main() {
    val cmd = &Command{
        Use: "checksum",
        RunE: (_ *Command, _ []string) error => {
            return nil
        },
    }
    Println(cmd.Use)
}
```

The hint spells that lambda from the literal's own signature. Go's grouped
parameters, `func(a, b int)`, are spelled one by one, `(a int, b int)`. A Go
result list such as `(int, error)` has no lambda form and is left out of the
hint: a lambda passed where Go expects one returns a `Try` or a tuple
([Go interop](/docs/go-interop/)).

**Rationale.** GALA has one way to write a function value. A literal used to
stop the parser inside its body, with messages that never named the lambda,
such as `no viable alternative at input '{return'`, followed by one
`extraneous input` error for each token the parser skipped until the next
declaration. A literal whose body is a single expression, such as
`func() { Println("hi") }`, did parse, as a composite literal of a function
type, and was then rejected for its body. The grammar now parses a literal,
the function type of [GALA-E0103](/docs/errors/gala-e0103/) followed by a
block, only so that every shape is this one error at the `func`. Every literal
in the file is reported, and a syntax error inside a literal's body keeps its
own message.

**Scope.** A function type with no body where a value is expected,
`val f = func(int) string`, is [GALA-E0103](/docs/errors/gala-e0103/). A named
`func` declared inside a function body is [GALA-E0052](/docs/errors/gala-e0052/).
In Go's `go func() { ... }()` and `defer func() { ... }()` the literal is
reported first; once it is a lambda, [GALA-E0036](/docs/errors/gala-e0036/)
names the GALA form of the whole statement. A function type is still written
as a type: a parameter's or a field's type, a `val`'s annotation, a result
type.

---

## Related

- [Lambda expressions](/docs/language-reference/#lambda-expressions)
- [All GALA error codes](/docs/errors/)
