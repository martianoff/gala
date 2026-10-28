---
layout: default
title: "GALA-E0085 — Struct Tag on a Field"
description: "GALA-E0085 fires when a struct field is followed by a Go struct tag such as json:\"name\". GALA has no struct tags: set JSON and YAML keys on Codec[T], or declare the type in a .go file of the package."
keywords: "gala-e0085, gala struct tag, gala json tag, gala yaml tag, gala extraneous input struct field, gala codec rename, gala omitempty"
permalink: /docs/errors/gala-e0085/
last_modified_at: 2026-10-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0085</p>

# GALA-E0085 — Struct tag on a field

**When it fires.** A struct field's type is followed by a string literal, in
either form: `Name string "json:\"name\""` or ``Name string `json:"name"` ``.
That is a Go struct tag, and GALA has no struct tags. It fires on a field of a
block-form struct (`type T struct { ... }`), of a shorthand struct
(`struct T(...)`) and of a sealed case (`case C(...)`). Every tagged field of
a block-form struct is reported; in a parenthesized field list, the first one.

**Minimal repro.**

```gala
package main

type Location struct {
    City string
    CountryCode string `json:"country_code"`
}

func main() {
    Println(Location(City = "Oslo", CountryCode = "NO").CountryCode)
}
```

**Error output.**

```
error[GALA-E0085]: struct field CountryCode has a tag: GALA has no struct tags
  --> main.gala:5:24
  |
5 |     CountryCode string `json:"country_code"`
  |                        ^^^^^^^^^^^^^^^^^^^^^ remove the tag
  |
  = hint: remove the tag; set JSON and YAML keys on Codec[T] with .Rename(field, key), .Omit and .OmitEmpty, and write a type that a Go library reads through its tags in a .go file of the package
```

**Fix.** Remove the tag. A JSON or YAML wire shape is configured on the codec
of the `json` or `yaml` package: a naming strategy covers most keys,
`.Rename(field, key)` sets one,
`.Omit(field)` leaves a field out, and `.OmitEmpty(field)` leaves it out when
it is empty:

```gala
package main

import . "martianoff/gala/json"

struct Location(City string, CountryCode string, Region string)

func main() {
    val codec = Codec[Location](SnakeCase()).Rename("CountryCode", "country").OmitEmpty("Region")
    Println(codec.Encode(Location("Oslo", "NO", "")).Get()) // {"city":"Oslo","country":"NO"}
}
```

A type that a Go library must read through its tags, such as an ORM or a
validator that reflects on the struct, is declared in a hand-written `.go`
file of the same package. GALA code uses it like any Go type, without an
import; see [One package, two languages](/docs/go-interop/#part-3-one-package-two-languages).

**Rationale.** In GALA the wire shape of a type is configured on its codec,
where it is checked at compile time and can differ per use, rather than in a
string the compiler cannot read. A block-form field used to accept an
interpreted-string tag and copy it into the generated Go, while a raw-string
tag and any tag on a shorthand struct field were parse errors that only said
`extraneous input`. A tag is now this error in every form.
