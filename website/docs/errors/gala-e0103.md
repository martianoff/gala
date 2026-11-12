---
layout: default
title: "GALA-E0103 — Function Type Used as a Value"
description: "GALA-E0103 fires when a function type such as func(int) string is written where a value is expected. Write a lambda for a function value."
keywords: "gala-e0103, gala function type, gala lambda, gala func type not a value, gala explicit type argument"
permalink: /docs/errors/gala-e0103/
last_modified_at: 2026-11-11
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0103</p>

# GALA-E0103 — Function type used as a value

**When it fires.** A function type, such as `func(int) string`, is written
where a value is expected: as the initializer of a `val`, an argument, an
operand. A function type is a type, not a value. It can be written as an
expression only as an explicit type argument, as in
`EmptyArray[func() int]()`.

**Minimal repro.**

```gala
package main

func main() {
    val f = func(int) string
    Println(f)
}
```

**Error output.**

```
error[GALA-E0103]: function type func(int) string is not a value
  --> main.gala:4:13
  |
4 |     val f = func(int) string
  |             ^^^^^^^^^^^^^^^^ write a function type only where a type is expected, such as…
  |
  = hint: write a function type only where a type is expected, such as an explicit type argument (`EmptyArray[func() int]()`); a function value is a lambda, `(x int) => x + 1`
```

**Fix.** For a function value, write a lambda. Give the variable the function
type if it should have one:

```gala
package main

func main() {
    val f func(int) string = (n) => s"#$n"
    Println(f(3))
}
```

**Rationale.** GALA writes a function value as a lambda, `(x int) => x + 1`,
and a function type only where a type goes. The grammar reads the list of an
explicit type argument as expressions, so a function type is accepted in
expression position there and rejected everywhere else.

---

## Related

- [Lambda expressions](/docs/language-reference/#lambda-expressions)
- [All GALA error codes](/docs/errors/)
