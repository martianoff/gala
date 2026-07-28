---
layout: default
title: "GALA-E0012 — Method Redefined on the Same Type"
description: "GALA-E0012 fires when the same method name is declared twice on one type. See the triggering code, the compiler message naming the first definition, and how to fix it."
keywords: "gala-e0012, method redefined, gala duplicate method, gala method redefinition, gala receiver method error"
permalink: /docs/errors/gala-e0012/
last_modified_at: 2026-07-26
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
error[GALA-E0012]: method "Greet" on type "User" in package "main" redefined (first defined in e0012same.gala)
  --> e0012same.gala:7:1
  |
7 | func (u User) Greet() string = "hola"
  | ^^^^ remove the duplicate method or rename it
  |
  = hint: remove the duplicate method or rename it
```

When the duplicate lives in a *different* file of the same package, the collision currently surfaces from the Go compiler instead (`method User.Greet already declared`) rather than as GALA-E0012.

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
- [All GALA error codes](/docs/errors/)
