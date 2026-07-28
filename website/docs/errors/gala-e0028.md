---
layout: default
title: "GALA-E0028 — Type Alias Redeclared in the Same Package"
description: "\"type alias \"Handler\" already declared in package \"main\"\" — GALA-E0028 fires when two type aliases share a name. See the compiler output and why the silent overwrite was worse than a lost declaration."
keywords: "gala-e0028, type alias already declared, gala duplicate type alias, gala alias redeclared, gala func type alias, gala declarations error"
permalink: /docs/errors/gala-e0028/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0028</p>

# GALA-E0028 — Type alias redeclared

**What it means.** A type alias (`type Handler func(string) string`) with the same name is declared more than once in the same package. The transformer keeps one lookup table of alias name → underlying type, so a second alias under the same name would replace the first.

Like [GALA-E0027](/docs/errors/gala-e0027/), this covers duplicates within one file *and* duplicates split across sibling files of the same package — the alias table is seeded from siblings when transformation begins.

---

## Code that triggers it

```gala
package main

type Handler func(string) string
type Handler func(int) int

func main() {
    Println("unused")
}
```

---

## Compiler message

```
error[GALA-E0028]: type alias "Handler" already declared in package "main"
  --> main.gala:4:6
  |
4 | type Handler func(int) int
  |      ^^^^^^^ remove the duplicate declaration or rename one of the aliase…
  |
  = hint: remove the duplicate declaration or rename one of the aliases
```

The caret points at the *second* alias's name — the one to rename or remove. The annotation beside the caret is the hint truncated to fit; the `= hint:` footer carries it in full.

---

## How to fix it

Give each alias a distinct name that says what it aliases:

```gala
package main

type StringHandler func(string) string
type IntHandler func(int) int

func main() {
    val upper StringHandler = (s) => s + "!"
    val double IntHandler = (n) => n * 2
    Println(upper("hi"))
    Println(double(21))
}
```

Note that both lambdas here are unannotated: the declared alias supplies the expected type, so the parameter types flow in. That is the typed-context rule from [GALA-E0033](/docs/errors/gala-e0033/) working in your favour.

---

## Why the rule exists

The silent overwrite was worse than a lost declaration. Because the alias table is consulted whenever a type name needs resolving, the *second* alias's underlying type would be substituted at call sites written against the first. The resulting Go either failed to compile at some unrelated line, or — when both underlying types happened to be structurally compatible — compiled and did the wrong thing. Rejecting at the declaration keeps the failure local.

**Scope.** Type aliases (`type Foo = Bar` / `type Foo func(...)`). Duplicate *type declarations* — structs, sealed types, interfaces — are [GALA-E0011](/docs/errors/gala-e0011/).

---

## Related redeclaration codes

[GALA-E0011](/docs/errors/gala-e0011/) types · [GALA-E0012](/docs/errors/gala-e0012/) methods · [GALA-E0027](/docs/errors/gala-e0027/) functions · **GALA-E0028** type aliases · [GALA-E0029](/docs/errors/gala-e0029/) interface method specs · [GALA-E0030](/docs/errors/gala-e0030/) struct fields · [GALA-E0031](/docs/errors/gala-e0031/) sealed cases

---

## Related

- [GALA-E0011](/docs/errors/gala-e0011/) — duplicate type *declarations*
- [GALA-E0033](/docs/errors/gala-e0033/) — typed contexts that make lambda annotations unnecessary
- [All GALA error codes](/docs/errors/)
