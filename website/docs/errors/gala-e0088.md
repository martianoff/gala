---
layout: default
title: "GALA-E0088 — Parameter with No Name"
description: "GALA-E0088 fires when a function, method or struct-shorthand parameter is written as a type alone, such as func sum(...int). Name the parameter: func sum(xs ...int)."
keywords: "gala-e0088, gala parameter has no name, gala nameless parameter, func sum(...int), gala variadic parameter name"
permalink: /docs/errors/gala-e0088/
last_modified_at: 2026-11-01
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0088</p>

# GALA-E0088 — Parameter with no name

**When it fires.** A parameter of a function, a method or a struct shorthand
is written as a type alone, as in `func sum(...int)` or `func sum(val ...int)`.
The parameters of a function *type* are types alone (`func(...int) int`), so
the grammar accepts the form, but a declared parameter is bound by its name:
the body has no other way to reach it.

**Minimal repro.**

```gala
package main

func sum(...int) int = 0

func main() = Println(sum(1, 2))
```

**Error output.**

```
error[GALA-E0088]: parameter `...int` has no name
  --> main.gala:3:10
  |
3 | func sum(...int) int = 0
  |          ^ name it, as in `x ...int`
  |
  = hint: name it, as in `x ...int`
```

**Fix.** Name the parameter. A `val` or `var` keyword comes before the name:

```gala
package main

func sum(xs ...int) int {
    var total = 0
    for _, x := range xs {
        total += x
    }
    total
}

func main() = Println(sum(1, 2))
```

**Rationale.** The parameter has to be named for the body to read it, and a
made-up name would be one the body cannot know. The declaration used to stop
the build with an internal error ([GALA-E0017](/docs/errors/gala-e0017/))
instead of an error at the parameter.

---

## Related

- [GALA-E0034](/docs/errors/gala-e0034/) — a parameter with a name but no type
- [Language Reference](/docs/language-reference/) — declaration syntax
- [All GALA error codes](/docs/errors/)
