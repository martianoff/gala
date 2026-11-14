---
layout: default
title: "GALA-E0115 — An inout Place Used Inside Try"
description: "GALA-E0115 rejects reading or writing an inout place inside the thunk of Try, where a caught panic would leave the caller's place half updated."
keywords: "gala-e0115, gala inout, gala ownership, gala exclusivity"
permalink: /docs/errors/gala-e0115/
last_modified_at: 2026-11-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0115</p>

# GALA-E0115 — an `inout` place used inside `Try`

**When it fires.** The thunk of `Try(...)` (a lambda, or an expression
passed by name) reads or writes an `inout` place. `Try` catches a panic, so a
write made before the panic would stay in the caller's place while the call
reports a failure: the caller would see a half-updated value.

**Minimal repro.**

```gala
package main

struct Counter(var n int)

func step(inout c Counter) = Println(Try(() => {
    c.n = c.n + 1
    c.n
}))

func main() {
    var c = Counter(1)
    step(&c)
    Println(c.n)
}
```

**Error output.**

```
error[GALA-E0115]: inout place c is used inside Try: a panic Try catches would leave the caller's place half updated
  --> main.gala:6:5
  |
6 |     c.n = c.n + 1
  |     ^ copy it into a local `var` first, and assign c back once Try…
  |
  = hint: copy it into a local `var` first, and assign c back once Try has succeeded
```

**Fix.** Work on a copy inside `Try`, and write the place once `Try` has
succeeded:

```gala
package main

struct Counter(var n int)

func step(inout c Counter) {
    val start = c.n
    Try(() => start + 1) match {
        case Success(n) => c.n = n
        case Failure(_) => Println("unchanged")
    }
}

func main() {
    var c = Counter(1)
    step(&c)
    Println(c.n) // 2
}
```

**Rationale.** A caller sees an `inout` place change all at once, when the
call returns. Ruling out writes inside a region that catches panics keeps
that true, and keeps the translation of the function to value semantics,
which returns the new value with the result, faithful.

---

## Related

- [`inout` parameters](/docs/language-reference/#inout-parameters)
- [All GALA error codes](/docs/errors/)
