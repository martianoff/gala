---
layout: default
title: "GALA-E0094 — Default Value Reads a Later Parameter"
description: "GALA-E0094 fires when a parameter's default value reads its own parameter or a later one, as in func area(w int = h, h int = 1). A default may read only the parameters before it."
keywords: "gala-e0094, gala default parameter, gala default value later parameter, gala default argument scope"
permalink: /docs/errors/gala-e0094/
last_modified_at: 2026-11-11
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0094</p>

# GALA-E0094 — Default value reads a later parameter

**When it fires.** A parameter's default value reads its own parameter or one
declared after it. A default is evaluated in the callee's scope, left to
right, with only the parameters before it bound, so a later parameter has no
value yet. A field default of a struct or a sealed case may read no field at
all (GALA-E0098). The error is reported at the declaration, whether or not any
call leaves the argument out.

**Minimal repro.**

```gala
package main

func area(w int = h, h int = 1) int = w * h

func main() {
    Println(area())
}
```

**Error output.**

```
error[GALA-E0094]: the default value of parameter w reads parameter h, which is not bound until after it
  --> main.gala:3:19
  |
3 | func area(w int = h, h int = 1) int = w * h
  |                   ^ a default may read only the parameters before its own
  |
  = hint: a default may read only the parameters before its own; declare h before w, or pass w explicitly
```

**Fix.** Declare the parameter the default reads first, or pass the argument
explicitly:

```gala
package main

func area(h int = 1, w int = h) int = w * h

func main() {
    Println(area())
}
```

**Rationale.** GALA evaluates a call's arguments left to right, then the
defaults of the parameters it leaves out, as Scala and Kotlin do. A default
that read a later parameter would see a value that does not exist yet.

## Related

- [Default Parameter Values](/docs/language-reference/#default-parameter-values)
- [All GALA error codes](/docs/errors/)
