---
layout: default
title: "GALA-E0064 — Opaque Type Used Without a Conversion"
description: "GALA-E0064 fires when an opaque type would convert implicitly to or from its underlying type, or to another opaque type. Write UserID(n) or int64(id)."
keywords: "gala-e0064, gala opaque type mismatch, gala cannot use as, gala opaque type implicit conversion, gala typed id"
permalink: /docs/errors/gala-e0064/
last_modified_at: 2026-10-03
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0064</p>

# GALA-E0064 — Opaque type used without a conversion

**When it fires.** A value would have to convert implicitly between an opaque
type and its underlying type, or between two opaque types, to fill a typed
slot: a function or method argument, a `val` / `var` declaration with a type, an
assignment, a return value, or a field of a constructor call. An opaque type
never converts implicitly, in either direction.

**Minimal repro.**

```gala
package main

opaque type UserID int64

func loadUser(id UserID) string = s"user ${int64(id)}"

func main() {
    val raw int64 = 42
    Println(loadUser(raw))
}
```

**Error output.**

```
error[GALA-E0064]: cannot use raw (int64) as UserID: an opaque type never converts implicitly
  --> main.gala:9:22
  |
9 |     Println(loadUser(raw))
  |                      ^^^ convert explicitly: UserID(raw)
  |
  = hint: convert explicitly: UserID(raw)
```

**Fix.** Convert explicitly:

```gala
package main

opaque type UserID int64

func loadUser(id UserID) string = s"user ${int64(id)}"

func main() {
    val raw int64 = 42
    Println(loadUser(UserID(raw)))
}
```

**The other direction.** An opaque value where its underlying type is
expected needs the conversion too — here in a return:

```gala
package main

opaque type Cents int64

func total(a Cents, b Cents) int64 = a + b

func main() {
    Println(total(Cents(250), Cents(100)))
}
```

```
error[GALA-E0064]: cannot use a + b (Cents) as int64: an opaque type never converts implicitly
  --> main.gala:5:38
  |
5 | func total(a Cents, b Cents) int64 = a + b
  |                                      ^^^^^ convert explicitly: int64(a + b)
  |
  = hint: convert explicitly: int64(a + b)
```

Write `int64(a + b)`, or return a `Cents`.

**Two opaque types.** Passing a `UserID` where an `OrderID` is expected is the
same error; the hint converts through the underlying type,
`OrderID(int64(id))`.

**What still works.** Untyped constants mix with an opaque type exactly as
they do with a Go defined type: `loadUser(42)`, `val zero UserID = 0`,
`id == 42` and `id + 1` need no conversion. A slot of type `any`, an interface
or a type parameter takes an opaque value as it is.

**Rationale.** An opaque type is a distinct type over an existing
representation; the point is that a bare `int64` cannot pass for a `UserID`,
nor a `UserID` for an `OrderID`. Go rejects these assignments too, but its
message describes the generated code, including the `.Get()` the transpiler
inserts to read a `val`. GALA reports it in the source's own terms,
with the conversion to write. `go build` remains the backstop for a position
this check does not cover.

**Related.** [Opaque Types](/docs/language-reference/#opaque-types),
[GALA-E0063](/docs/errors/gala-e0063/) for an explicit conversion between two opaque
types.
