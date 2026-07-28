---
layout: default
title: "GALA-E0011 — Type Redefined in the Same Package"
description: "GALA-E0011 fires when a type, struct, or sealed type with the same name is declared twice in one package. See the triggering code, the compiler message, and how to resolve the collision."
keywords: "gala-e0011, type redefined, gala duplicate type, gala type redefinition error, gala package declarations"
permalink: /docs/errors/gala-e0011/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0011</p>

# GALA-E0011 — Type redefined

**What it means.** A `type`, struct shorthand, or `sealed type` with the same name is declared more than once in the same package, across any combination of files.

---

## Code that triggers it

```gala
package main

type User struct { Name string }

type User struct { Email string }   // redefines User

func main() {
    Println("hi")
}
```

The two declarations can also live in different files of the same package — the check spans the whole package.

---

## Compiler message

```
error[GALA-E0011]: type "User" in package "main" redefined (first defined in e0011same.gala)
  --> e0011same.gala:5:1
  |
5 | type User struct { Email string }
  | ^^^^ remove the duplicate declaration or rename one of the types
  |
  = hint: remove the duplicate declaration or rename one of the types
```

The header names the file holding the first definition, so you can jump straight to the other half of the collision.

---

## How to fix it

Delete one declaration, merge their fields into one type, or rename one so both can coexist:

```gala
// b.gala
package main
type UserContact struct { Email string }
```

---

## Why the rule exists

The transpiler builds a single canonical metadata entry per type. A second declaration would silently overwrite the first, producing surprising method dispatch and generated Go that references fields which no longer exist. Catching the duplicate in the analyzer makes the collision obvious.

---

## Related

- [GALA-E0012](/docs/errors/gala-e0012/) — the same rule for methods
- [GALA-E0016](/docs/errors/gala-e0016/) — a field name that shadows a type name
- [All GALA error codes](/docs/errors/)
