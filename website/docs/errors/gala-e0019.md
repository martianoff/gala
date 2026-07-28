---
layout: default
title: "GALA-E0019 — Empty Parenthesized Expression () Cannot Be Used as a Value"
description: "GALA-E0019 fires when `()` appears where an expression is required, usually as a no-op match arm. See the triggering code, the compiler message, and what to write instead."
keywords: "gala-e0019, empty parenthesized expression, gala unit value, gala void match arm, gala () not allowed, gala match arm error"
permalink: /docs/errors/gala-e0019/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0019</p>

# GALA-E0019 — Empty parenthesized expression

**What it means.** The source contains `()` where an expression is required — a match-arm body, an argument, or the right-hand side of a declaration. The grammar admits the form because the inner expression list is optional, but GALA has no unit/void literal to lower it to.

The most common offender is a no-op match arm:

```gala
package main

func handle(code int) {
    code match {
        case 0 => Println("zero")
        case _ => ()                  // GALA-E0019
    }
}

func main() {
    handle(0)
}
```

---

## Compiler message

```
error[GALA-E0019]: empty parenthesized expression "()" cannot be used as a value
  --> e0019.gala:6:19
  |
6 |         case _ => ()
  |                   ^ use a real statement
  |
  = hint: use a real statement (e.g. `Println("…")`) or remove the arm if it cannot occur
```

---

## How to fix it

Write what the arm actually does:

```gala
package main

func handle(code int) {
    code match {
        case 0 => Println("zero")
        case _ => Println("other")
    }
}

func main() {
    handle(0)
}
```

Note the *statement* body (`func handle(code int) { … }`) rather than the expression body (`func handle(code int) = …`). A match whose arms only produce side effects has no value, so it belongs in a statement position. Written with `=`, the same match is used as a value and the Go compiler rejects the arms for returning nothing.

If the arm is genuinely unreachable, say so loudly rather than silently. A bare `panic(...)` is [GALA-E0035](/docs/errors/gala-e0035/) — GALA's form is `go_builtins.Panic`:

```gala
package main

import "martianoff/gala/go_builtins"

func describe(code int) string = code match {
    case 0 => "zero"
    case _ => go_builtins.Panic(s"unreachable: unexpected code $code")
}

func main() {
    Println(describe(0))
}
```

---

## Why the rule exists

GALA's grammar reuses an optional expression list inside `'(' … ')'` so that paired and single-value cases share one rule. The empty branch has no value to emit: before this code existed it produced a nil AST node that crashed Go's printer far from the source. Surfacing the failure at its real site gives you an actionable hint instead of a transpiler stack trace.

**Scope.** Only the literal expression `()`. Calls written `f()` are unaffected — an empty argument list is a different grammar production.

---

## Related

- [Pattern Matching](/features/pattern-matching/) — match arms and their result types
- [GALA-E0003](/docs/errors/gala-e0003/) — missing default case
- [All GALA error codes](/docs/errors/)
