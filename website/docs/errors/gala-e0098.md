---
layout: default
title: "GALA-E0098 — Field Default Reads a Field"
description: "GALA-E0098 fires when the default value of a struct or sealed case field reads a field of the same type, as in struct Rect(W int, H int = W). Compute such a field in a constructor function."
keywords: "gala-e0098, gala field default, gala struct default value, gala default reads field"
permalink: /docs/errors/gala-e0098/
last_modified_at: 2026-11-11
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0098</p>

# GALA-E0098 — Field default reads a field

**When it fires.** The default value of a shorthand struct's field or a
sealed case's field reads a field of the same struct or case: another field,
or the field itself, directly or anywhere in the expression. A field default
is evaluated with no field of its type in scope. A name spelled like a field
that resolves to nothing else is this error, and so is one that resolves to a
package-level val or var: the default would read the val where the field was
almost certainly meant. A type, a function or an import spelled like a field
keeps its meaning: `Retry Retry = Retry(3)` calls the type's constructor. A function's default, by contrast, may read the parameters
before it (see [GALA-E0094](/docs/errors/gala-e0094/)).

**Minimal repro.**

```gala
package main

struct Rect(W int, H int = W)

func main() {
    Println(Rect(2).H)
}
```

**Error output.**

```
error[GALA-E0098]: the default value of field H reads field W of Rect
  --> main.gala:3:28
  |
3 | struct Rect(W int, H int = W)
  |                            ^ a field default may not read a field of its type
  |
  = hint: a field default may not read a field of its type; give H a default that reads no field, or compute it in a constructor function instead
```

**Fix.** Compute the field in a constructor function:

```gala
package main

struct Rect(W int, H int)

func square(side int) Rect = Rect(side, side)

func main() {
    Println(square(2).H)
}
```

**A package-level val spelled like a field.** No field is in scope in a field
default, so a name spelled like a field that resolves to a package-level `val`
or `var` reads that val. The field was almost certainly meant, so this is the
same error:

```gala
package main

val W = 7

struct Rect(W int, H int = W)

func main() {
    Println(Rect(2).H)
}
```

```
error[GALA-E0098]: the default value of field H of Rect reads package-level val W, which has the same name as field W
  --> main.gala:5:28
  |
5 | struct Rect(W int, H int = W)
  |                            ^ rename the val or the field
  |
  = hint: rename the val or the field; a field default cannot read a field, so W here is the val
```

Rename the val (or the field), so the default says what it reads:

```gala
package main

val defaultHeight = 7

struct Rect(W int, H int = defaultHeight)

func main() {
    Println(Rect(2).H)
}
```

A type, a function or an import spelled like a field is no val: it keeps its
meaning, and `Retry Retry = Retry(3)` calls the type's constructor.

**Rationale.** Go code builds the struct through its generated `New<T>`
constructor, which sets each field to its default before it applies the
caller's options, so a default that read another field could not see the value
an option gives that field. Scala rejects the same declaration: `case class Rect(w: Int, h: Int = w)` does not compile.

## Related

- [Default Parameter Values](/docs/language-reference/#default-parameter-values)
- [All GALA error codes](/docs/errors/)
