---
layout: default
title: "GALA-E0074 — Invalid Default Case"
description: "GALA-E0074 fires when a sealed type marks more than one case default, or its default case holds a field of the type itself. The default case is the type's zero value."
keywords: "gala-e0074, gala sealed default case, gala sealed type zero value, gala default case, gala option none zero value"
permalink: /docs/errors/gala-e0074/
last_modified_at: 2026-10-10
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0074</p>

# GALA-E0074 — Invalid default case

**When it fires.** A sealed type's default case is invalid:

- more than one case is marked `default`. The default case is the type's zero
  value — what a value nothing constructed holds — so a sealed type has at
  most one; or
- the default case has a field of the sealed type itself. Such a field is held
  through a pointer, which a zero value leaves nil, so the zero value could not
  be read as that case.

**Minimal repro.**

```gala
package main

sealed type Light {
    default case Off()
    case Dim(Level int)
    default case Unknown()
}

func main() {
    Println(Off())
}
```

**Error output.**

```
error[GALA-E0074]: sealed type "Light" marks both "Off" and "Unknown" as its default case
  --> main.gala:6:5
  |
6 |     default case Unknown()
  |     ^^^^^^^ keep `default` on one case: the default case is the type's z…
  |
  = hint: keep `default` on one case: the default case is the type's zero value
```

**Fix.** Keep `default` on the case a value should be when nothing set it,
and make it a case without a field of the type itself:

```gala
package main

sealed type Light {
    default case Off()
    case Dim(Level int)
    case Unknown()
}

func main() {
    Println(Off())
}
```

**Rationale.** A Go zero value of a sealed type — an uninitialized `var`, a
field a struct literal leaves out, a slot of a fresh slice — is its case with
tag 0, and the default case takes that tag. Two default cases would need the
same tag. A sealed type with no default case has no zero value: its cases are
numbered from 1, and a match on a zero value panics instead of reading it as
one of the cases. See [Sealed Types: Default Case](/features/sealed-types/#default-case).
