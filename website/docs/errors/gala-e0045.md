---
layout: default
title: "GALA-E0045 — Missing Required Field in Struct Construction"
description: "\"missing required field in construction\" — GALA-E0045 fires when a shorthand struct is constructed without a field that has no default. See the compiler output, the two fixes, and why Go-shaped structs are exempt."
keywords: "gala-e0045, gala missing required field, gala struct default, gala struct construction, gala constructor, gala named arguments, gala zero value"
permalink: /docs/errors/gala-e0045/
last_modified_at: 2026-09-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0045</p>

# GALA-E0045 — missing required field in struct construction

**What it means.** A shorthand struct was constructed with call syntax, and the call omitted a field that declares no default. Both call forms are covered — named (`Cfg(Name = "a")`) and positional (`Cfg("a")`) — because both are constructor calls.

---

## Code that triggers it

```gala
package main

struct Cfg(Name string, Tries int)

func main() {
    val c = Cfg(Name = "a")
    Println(c.Tries)
}
```

---

## Compiler message

```
error[GALA-E0045]: missing required field "Tries" in construction of "Cfg"
  --> main.gala:6:17
  |
6 |     val c = Cfg(Name = "a")
  |                 ^^^^ pass "Tries", or give the field a default in the declaration
  |
  = hint: pass "Tries", or give the field a default in the declaration (e.g. Tries int = 0)
```

Every omitted field is named at once, so a call missing several takes one round trip rather than several.

---

## How to fix it

Either pass the field:

<!-- doc-check: fragment -->
```gala
val c = Cfg(Name = "a", Tries = 3)
```

or declare a default, which makes it optional at every call site:

```gala
struct Cfg(Name string, Tries int = 3)

val c = Cfg(Name = "a")   // Tries = 3
```

A field default is re-evaluated at each construction, not once at declaration — the same contract function parameter defaults have.

---

## Why the rule exists

A shorthand struct's field list *is* a constructor signature, and `= value` on a field means what it means on a function parameter. Before this check, an omitted field silently took Go's zero value — a defaulted `rune` became NUL and a defaulted `int` became 0, with no diagnostic. Since a zero-valued `rune`, `int` or `bool` is often a legal value, nothing downstream could tell "the caller omitted it" from "the caller meant 0".

---

## A named argument that names no field

The same code reports a named argument matching no field of the struct being built. This half applies to *every* struct called with call syntax — shorthand, block form, and a Go struct (imported, or declared in a hand-written `.go` file of the package itself) — because the literal is built from the arguments that name a field, so one naming none would be dropped before the Go compiler could reject it.

```gala
package main

type Cfg struct {
    Name string
    Tries int
}

func main() {
    val c = Cfg(Name = "a", Retries = 2)
    Println(c.Tries)
}
```

```
error[GALA-E0045]: unknown field "Retries" in construction of "Cfg"
  --> main.gala:9:29
  |
9 |     val c = Cfg(Name = "a", Retries = 2)
  |                             ^^^^^^^ Cfg declares: Name, Tries
  |
  = hint: Cfg declares: Name, Tries
```

Fix the name to one the hint lists. For a Go struct the hint lists the fields the package may set: its exported ones, and the unexported ones too when the struct is declared in the package's own `.go` files.

---

## Where it stands down

The required-field check fires only for **shorthand-declared GALA structs constructed with call syntax**. Everything that is or mirrors a Go struct keeps Go's partial-literal semantics:

- **Go-imported types** — `url.URL(Scheme = "x")` constructs partially and raises nothing.
- **Go-style literals** — `Cfg{Name: "a"}` is a composite literal, not a constructor call; it stays partial.
- **Block-form structs** — `type Cfg struct { ... }` mirrors a Go declaration and has no syntax for a field default.
- **`Copy`** — `c.Copy(Name = "b")` carries every other field forward from the receiver.

---

## Related

- [GALA-E0043](/docs/errors/gala-e0043/) — a type name called where it constructs nothing
- [Language Reference: Types and Structs](/docs/language-reference/#4-types-and-structs)
- [All GALA error codes](/docs/errors/)
