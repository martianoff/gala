---
layout: default
title: "GALA-E0081 — Field and Method of One Name"
description: "GALA-E0081 fires when a struct declares a method with the same name as one of its fields. Rename one of them, for example a private field behind a getter."
keywords: "gala-e0081, gala field and method same name, gala getter, gala struct method name, gala field method collision"
permalink: /docs/errors/gala-e0081/
last_modified_at: 2026-10-24
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0081</p>

# GALA-E0081 — Field and method of one name

**When it fires.** A struct declares a method with the same name as one of its
fields. `u.Name` would then name two things, and Go allows no field and method
of one name on a type.

**Minimal repro.**

```gala
package main

struct User(Name string)

func (u User) Name() string = "user " + u.Name

func main() {
    Println(User("ada").Name())
}
```

**Error output.**

```
error[GALA-E0081]: type User has both a field and a method named Name
  --> main.gala:5:15
  |
5 | func (u User) Name() string = "user " + u.Name
  |               ^^^^ rename the field or the method
  |
  = hint: rename the field or the method; Go allows no field and method of one name on a type
```

**Fix.** Rename one of them. A getter over a private field is the usual shape:
the field is `name`, the method `Name`:

```gala
package main

struct User(name string)

func (u User) Name() string = "user " + u.name

func main() {
    Println(User("ada").Name()) // user ada
}
```

A [sealed type](/docs/language-reference/#sealed-types-algebraic-data-types) is different: its case
fields belong to their case, so a method over them may share a field's name
(`case Atom(Target int)` with `func (t Transition) Target() int`).

**Rationale.** Without the check a call of the method was reported as calling
a field, or the pair reached `go build`, which rejected it as "field and method
with the same name" in the generated code.
