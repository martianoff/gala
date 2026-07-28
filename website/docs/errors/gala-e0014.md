---
layout: default
title: "GALA-E0014 — Default Value Type Does Not Match the Parameter Type"
description: "GALA-E0014 fires when a parameter's default literal is incompatible with its declared type. See the triggering signature, the compiler message, and both ways to reconcile the mismatch."
keywords: "gala-e0014, default for parameter has type, gala default value type mismatch, gala default arguments, gala type error"
permalink: /docs/errors/gala-e0014/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0014</p>

# GALA-E0014 — Default expression type mismatch

**What it means.** A parameter's default-value literal is statically incompatible with the parameter's declared type.

---

## Code that triggers it

```gala
package main

func create(a int = "not-an-int") int = a
//                  ^^^^^^^^^^^^ string literal, int parameter

func main() {
    Println(create())
}
```

---

## Compiler message

```
error[GALA-E0014]: default for parameter "a" has type string, expected int
  --> e0014.gala:3:21
  |
3 | func create(a int = "not-an-int") int = a
  |                     ^^^^^^^^^^^^ fix the default expression or change the parameter type
  |
  = hint: fix the default expression or change the parameter type
```

The caret underlines the default expression itself — this check records an exact span rather than letting the renderer derive one.

---

## How to fix it

Adjust the default to match the parameter type, or change the type to match the intended default:

```gala
func create(a int = 0) int = a
func create(a string = "not-an-int") string = a
```

---

## Why the rule exists

Default-argument injection happens at the call site, so a mismatched default would either fail in Go with a less actionable message or silently round-trip through an implicit conversion. Catching it in the analyzer keeps the error next to the declaration, where the fix is obvious.

Note that the check runs for **literal** defaults only. Non-literal expressions (`f()`, `x + 1`) are skipped because their types depend on resolution context; the literal subset covers the cases most likely to be typos.

---

## Related

- [GALA-E0013](/docs/errors/gala-e0013/) — default parameter ordering
- [GALA-E0021](/docs/errors/gala-e0021/) — general type mismatch during inference
- [All GALA error codes](/docs/errors/)
