---
layout: default
title: "GALA-E0058 — Generated Codec Metadata Named in GALA Code"
description: "GALA-E0058 fires when GALA source names a StructMeta_X, _StructMeta_X or _ValueMeta_X type the transpiler generates for a codec; use Codec[T] or StructMeta[T]() instead."
keywords: "gala-e0058, gala structmeta, gala StructMeta_, gala codec metadata, gala json codec, gala private fields"
permalink: /docs/errors/gala-e0058/
last_modified_at: 2026-10-02
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0058</p>

# GALA-E0058 — Generated codec metadata named in GALA code

**What it means.** GALA source names a type the transpiler generates for a codec: `StructMeta_X` (a library struct's metadata, exported for its importers), `_StructMeta_X` (a `main` package struct's metadata) or `_ValueMeta_X`. It fires for a use, qualified (`billing.StructMeta_Email`) or not, and for a declaration of that name.

---

## Code that triggers it

```gala
package main

struct Email(address string)

func main() {
    Println(_StructMeta_Email{}.NumFields())
}
```

---

## Compiler message

```
error[GALA-E0058]: _StructMeta_Email is codec metadata the transpiler generates and cannot be named in GALA code
  --> main.gala:6:13
  |
6 |     Println(_StructMeta_Email{}.NumFields())
  |             ^^^^^^^^^^^^^^^^^ use json.Codec[T], yaml.Codec[T] or StructMeta[T]() instead
  |
  = hint: use json.Codec[T], yaml.Codec[T] or StructMeta[T]() instead; those check that a struct with private fields may be decoded
```

---

## How to fix it

Ask for the metadata through the intrinsic, or use a codec:

```gala
package main

struct Email(Address string)

func main() {
    Println(StructMeta[Email]().NumFields())
}
```

---

## Why the rule exists

A package's `StructMeta_X` reads and builds the struct's unexported fields — that is what lets another package encode it. `Codec[T]` and `StructMeta[T]()` reach the same metadata but first check, while building, that a struct with private fields declares `Validate` ([GALA-E0050](/docs/errors/gala-e0050/), [GALA-E0057](/docs/errors/gala-e0057/)). Named directly, the metadata skips that check — decoding a struct without `Validate` would fail only when the program runs — and its `EncodeFields` hands any caller the fields the package keeps to itself. A declaration of such a name is rejected too, since it could collide with the type the transpiler emits. Generated Go and hand-written Go siblings are not GALA source and are not checked.

---

## Related

- [Structs With Private Fields](/docs/json/#structs-with-private-fields)
- [All GALA error codes](/docs/errors/)
