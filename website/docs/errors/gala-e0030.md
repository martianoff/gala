---
layout: default
title: "GALA-E0030 — Struct Field Redeclared"
description: "\"field \"X\" already declared in struct \"Point\"\" — GALA-E0030 fires when a struct declares two fields with the same name. Covers both the shorthand and block forms, with the real compiler output for each."
keywords: "gala-e0030, field already declared in struct, gala duplicate struct field, gala struct shorthand, gala field name collision, gala declarations error"
permalink: /docs/errors/gala-e0030/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0030</p>

# GALA-E0030 — Struct field redeclared

**What it means.** A struct declares two fields with the same name. Both struct syntaxes are covered:

- **Shorthand form** — `struct Point(X int, X int)`
- **Block form** — `type Point struct { val x int; val x int }`

---

## Code that triggers it

```gala
package main

struct Point(X int, X int)

func main() {
    val p = Point(1, 2)
    Println(p.X)
}
```

---

## Compiler message

```
error[GALA-E0030]: field "X" already declared in struct "Point"
  --> main.gala:3:21
  |
3 | struct Point(X int, X int)
  |                     ^ rename or remove the duplicate field
  |
  = hint: rename or remove the duplicate field
```

The caret points at the second occurrence of the name. The block form reports identically:

```
error[GALA-E0030]: field "x" already declared in struct "Point"
  --> main.gala:5:9
  |
5 |     val x int
  |         ^ rename or remove the duplicate field
  |
  = hint: rename or remove the duplicate field
```

---

## How to fix it

Rename the duplicate, or delete it if it was a copy-paste artefact:

```gala
package main

struct Point(X int, Y int)

func main() {
    val p = Point(1, 2)
    Println(s"${p.X}, ${p.Y}")
}
```

If you wanted several values under one conceptual name, hold a collection instead of repeating the field:

```gala
import . "martianoff/gala/collection_immutable"

struct Path(Points Array[int])
```

---

## Why the rule exists

The duplicate was doubly damaging. The field-type map kept only the later type, so the earlier field's type was lost; and the ordered field-name list contained the name *twice*, which the generator emitted verbatim — producing Go with a duplicated struct field that could not compile. Because the resulting failure surfaced in generated code, it pointed at a line the author never wrote. Rejecting in the analyzer keeps the error on the declaration.

**Scope.** Fields within one struct declaration. A field name that collides with a *type* name in the same package is [GALA-E0016](/docs/errors/gala-e0016/); duplicate sealed-variant case names are [GALA-E0031](/docs/errors/gala-e0031/).

---

## Related redeclaration codes

[GALA-E0011](/docs/errors/gala-e0011/) types · [GALA-E0012](/docs/errors/gala-e0012/) methods · [GALA-E0027](/docs/errors/gala-e0027/) functions · [GALA-E0028](/docs/errors/gala-e0028/) type aliases · [GALA-E0029](/docs/errors/gala-e0029/) interface method specs · **GALA-E0030** struct fields · [GALA-E0031](/docs/errors/gala-e0031/) sealed cases

---

## Related

- [GALA-E0016](/docs/errors/gala-e0016/) — a field name that shadows a type name
- [Collections](/features/collections/) — `Array` and friends instead of repeated fields
- [All GALA error codes](/docs/errors/)
