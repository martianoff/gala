---
layout: default
title: "GALA-E0013 — Non-Defaulted Parameter After a Defaulted Parameter"
description: "GALA-E0013 fires when a parameter without a default follows one that has a default. See the triggering signature, the compiler message, and how to reorder parameters so defaults trail."
keywords: "gala-e0013, parameter has no default but follows, gala default parameters, gala default arguments, gala function signature error"
permalink: /docs/errors/gala-e0013/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0013</p>

# GALA-E0013 — Non-defaulted parameter after a defaulted parameter

**What it means.** A function signature declares a parameter without a default *after* a parameter that has one. Defaults must be contiguous and at the tail of the parameter list.

---

## Code that triggers it

```gala
package main

func create(a int = 5, b int) int = a + b
//                     ^^^^^ no default, but follows a default

func main() {
    Println(create(1, 2))
}
```

---

## Compiler message

```
error[GALA-E0013]: parameter "b" in create has no default but follows a parameter with a default
  --> e0013.gala:3:1
  |
3 | func create(a int = 5, b int) int = a + b
  | ^^^^ move parameters with defaults to the end of the parameter li…
  |
  = hint: move parameters with defaults to the end of the parameter list
```

The caret marks the declaration; the header names the offending parameter.

---

## How to fix it

Reorder so every default sits at the end:

```gala
func create(b int, a int = 5) int = a + b
```

Or give the follower a default too:

```gala
func create(a int = 5, b int = 0) int = a + b
```

---

## Why the rule exists

With mixed defaults, positional calls become ambiguous: does `create(7)` mean `a = 7, b = default` or `a = default, b = 7`? Requiring contiguous trailing defaults keeps positional calls unambiguous, while named-argument calls can still pick any subset.

---

## Related

- [GALA-E0014](/docs/errors/gala-e0014/) — a default whose type does not match the parameter
- [Language Reference](/docs/language-reference/) — function declarations and default arguments
- [All GALA error codes](/docs/errors/)
