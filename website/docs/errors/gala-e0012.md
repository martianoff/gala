---
layout: default
title: "GALA-E0012 — Method Redefined on the Same Type"
description: "\"method \"Greet\" on type \"User\" in package \"main\" redefined\" — GALA-E0012 fires when the same method name is declared twice on one type. See the compiler message and how to fix it."
keywords: "gala-e0012, method redefined, gala duplicate method, gala method redefinition, gala receiver method error"
permalink: /docs/errors/gala-e0012/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0012</p>

# GALA-E0012 — Method redefined

**What it means.** A method with the same name is declared twice on the same type, across any combination of files in the package.

---

## Code that triggers it

```gala
package main

type User struct { Name string }

func (u User) Greet() string = "hello"

func (u User) Greet() string = "hola"   // duplicate

func main() {
    Println(User("x").Greet())
}
```

---

## Compiler message

```
error[GALA-E0012]: method "Greet" on type "User" in package "main" redefined (also declared at line 5)
  --> e0012same.gala:7:1
  |
7 | func (u User) Greet() string = "hola"
  | ^^^^ remove the duplicate method or rename it
  |
  = hint: remove the duplicate method or rename it
```

The check spans the whole package, so splitting the duplicates across files does not hide them. With the first `Greet` in `a.gala` and the second in `b.gala`, the header names the other file and the caret narrows to the method name:

```
error[GALA-E0012]: method "Greet" on type "User" in package "main" redefined (also declared at a.gala:5)
  --> b.gala:3:15
  |
3 | func (u User) Greet() string = "hola"
  |               ^^^^^ remove the duplicate method or rename it
  |
  = hint: remove the duplicate method or rename it
```

---

## How to fix it

Delete one definition, or rename the second to reflect what it actually does:

```gala
func (u User) GreetFormal() string = "hola"
```

---

## Why the rule exists

Method resolution uses a single map keyed by method name. A second declaration would silently replace the first, making behavior depend on file-loading order — the hardest kind of bug to reproduce. This also mirrors Go's own rule, which forbids duplicate methods on the same receiver.

---

## Related

- [GALA-E0011](/docs/errors/gala-e0011/) — the same rule for types
- [GALA-E0027](/docs/errors/gala-e0027/) — the same rule for top-level functions
- [GALA-E0029](/docs/errors/gala-e0029/) — the same rule for interface method *specs*
- [All GALA error codes](/docs/errors/)
