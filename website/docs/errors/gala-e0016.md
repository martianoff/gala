---
layout: default
title: "GALA-E0016 — Generic Struct Field Name Collides With a Type Name"
description: "GALA-E0016 fires when a generic struct's field shares its name with another generic type in the package. See the triggering code, the compiler message, and the rename that fixes it."
keywords: "gala-e0016, field shares its name with generic type, gala field name collision, gala generic struct, gala shadowing type name"
permalink: /docs/errors/gala-e0016/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0016</p>

# GALA-E0016 — Struct field name collides with a type name

**What it means.** A *generic* struct declares a field whose name matches another *generic* type (struct or sealed type) in the same package. Both sides must be generic for the rule to fire — non-generic shapes like `struct Route(Handler Handler)` remain accepted.

---

## Code that triggers it

```gala
package main

sealed type Mode[T any] {
    case A(Fn func(int) T)
    case B(Fn func(string) T)
}

// Field `Mode` collides with the sealed type `Mode`.
struct Box[T any](Mode Mode[T])

func main() {
    Println("hi")
}
```

---

## Compiler message

```
error[GALA-E0016]: field "Mode" in generic "Box" shares its name with generic type "Mode" in package "main"
  --> e0016.gala:8:19
  |
8 | struct Box[T any](Mode Mode[T])
  |                   ^^^^ rename the field
  |
  = hint: rename the field (e.g. "Mode" → "M") so it does not shadow the type name
```

---

## How to fix it

Rename the field (or the type) so the two identifiers are distinct:

```gala
struct Box[T any](M Mode[T])
```

Idiomatic GALA picks field names that hint at the role rather than restating the type — `mode HarnessMode[T]`, or a short `M` when the type already says everything.

---

## Why the rule exists

When a `match` scrutinee is a field access whose field shares a name with a type in scope, the generated wrapper's parameter type can come out with duplicated type arguments (`func(obj Mode[T][T]) T {…}`), which is not valid Go. The collision is also a readability smell on its own: `b.Mode` on a `Box[T any]` where `Mode` is also a type forces every reader to disambiguate field from type.

**Scope.** Limited to struct and shorthand-struct fields whose name matches another *type* in the same package. Field-vs-function, field-vs-variable, and field-name-equals-own-struct-name are not flagged.

---

## Related

- [Sealed Types](/features/sealed-types/)
- [GALA-E0011](/docs/errors/gala-e0011/) — duplicate type declarations
- [All GALA error codes](/docs/errors/)
