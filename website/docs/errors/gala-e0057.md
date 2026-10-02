---
layout: default
title: "GALA-E0057 — Validate Method With the Wrong Signature"
description: "GALA-E0057 fires when a codec would decode a struct with private fields whose Validate method is not func (x T) Validate() Try[T] — a pointer receiver, a parameter, or a bool or error result."
keywords: "gala-e0057, gala validate method, gala json codec private fields, gala yaml codec, gala decode validation, gala encapsulated struct"
permalink: /docs/errors/gala-e0057/
last_modified_at: 2026-10-02
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0057</p>

# GALA-E0057 — Validate method with the wrong signature

**What it means.** A codec would decode a struct that has an unexported field, and the struct's `Validate` method does not have the one signature decoding calls, `func (x T) Validate() Try[T]`. A pointer receiver, a parameter, a type parameter, or a result other than `Try[T]` (`bool`, `error`, `Option[T]`) all fire it, wherever the struct sits in the codec: the root, a nested field, or inside an `Option`, `Array`, `List` or `HashMap`.

---

## Code that triggers it

```gala
package main

import (
    "martianoff/gala/json"
    "strings"
)

struct Email(address string)

func (e Email) Validate() bool = strings.Contains(e.address, "@")

func main() {
    val codec = json.Codec[Email](json.AsIs())
    Println(codec.Decode("{\"address\":\"not an address\"}"))
}
```

---

## Compiler message

```
error[GALA-E0057]: cannot generate a codec for Email: Email has private fields, and its Validate method is `func (e Email) Validate() bool`, not `func (e Email) Validate() Try[Email]`
  --> main.gala:13:35
   |
13 |     val codec = json.Codec[Email](json.AsIs())
   |                                   ^^^^ Validate needs a value receiver and a Try[Email] result
   |
   = hint: Validate needs a value receiver and a Try[Email] result; declare it as `func (e Email) Validate() Try[Email]`: decoding builds the raw value and returns what Validate returns, so a Failure rejects the input with its error
```

---

## How to fix it

Return the value itself on success and the reason it is invalid on failure:

```gala
package main

import (
    "errors"
    "martianoff/gala/json"
    "strings"
)

struct Email(address string)

func (e Email) Validate() Try[Email] =
    if (strings.Contains(e.address, "@")) Success(e) else Failure(errors.New(s"not an email address: ${e.address}"))

func main() {
    val codec = json.Codec[Email](json.AsIs())
    Println(codec.Decode("{\"address\":\"not an address\"}"))
}
```

`Decode` now returns `Failure(not an email address: not an address)` — the error your `Validate` returned, unchanged.

---

## Why the rule exists

A struct with an unexported field is an encapsulated value: its package builds it only through a constructor that checks it. Decoding builds the struct from whatever the input holds, so the codec builds the raw value and returns what `Validate` returns, and a value the constructor would refuse is refused by the codec too. That needs one fixed signature: a `bool` or `error` result would leave the codec to invent the failure, and a pointer receiver or a parameter cannot be called on the freshly built value. A method with the right name and the wrong shape is reported rather than ignored, since ignoring it would leave the struct undecodable for a reason the author cannot see.

---

## Related

- [GALA-E0050](/docs/errors/gala-e0050/) — a struct with private fields and no `Validate` method at all
- [Structs With Private Fields](/docs/json/#structs-with-private-fields)
- [All GALA error codes](/docs/errors/)
