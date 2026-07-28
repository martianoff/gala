---
layout: default
title: "GALA-E0027 — Function Redeclared in the Same Package"
description: "\"function \"greet\" in package \"main\" redeclared\" — GALA-E0027 fires when two top-level functions share a name. GALA has no overloading; see the compiler output and the default-parameter and sealed-type alternatives."
keywords: "gala-e0027, function redeclared, gala duplicate function, gala no overloading, gala default parameters, gala package namespace error"
permalink: /docs/errors/gala-e0027/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0027</p>

# GALA-E0027 — Function redeclared

**What it means.** A top-level function with the same name is declared more than once in the same package, across any combination of files. GALA mirrors Go's "redeclared in this block" rule: a package has one namespace for its top-level functions, and **there is no overloading** — differing parameter lists do not make two `greet` functions distinct.

---

## Code that triggers it

```gala
package main

func greet(name string) string = "hello " + name

func greet(other string) string = "hi " + other

func main() {
    Println(greet("world"))
}
```

---

## Compiler message

```
error[GALA-E0027]: function "greet" in package "main" redeclared (also declared at line 3)
  --> main.gala:5:1
  |
5 | func greet(other string) string = "hi " + other
  | ^^^^ remove the duplicate declaration or rename one of the functi…
  |
  = hint: remove the duplicate declaration or rename one of the functions
```

The annotation beside the caret is the hint truncated to fit; the `= hint:` footer carries it in full.

### Across two files

The check spans the whole package, so splitting the duplicates does not hide them. With `greet` in both `a.gala` and `b.gala`:

```
error[GALA-E0027]: function "greet" in package "main" redeclared (also declared at a.gala:3)
  --> b.gala:3:6
  |
3 | func greet(other string) string = "hi " + other
  |      ^^^^^ remove the duplicate declaration or rename one of the functi…
  |
  = hint: remove the duplicate declaration or rename one of the functions
```

When the other declaration lives in a different file, the header names that file and line; when it is in the same file, it gives just the line. Analysis order is not source order, so the message never claims either declaration came first — it points at the *other* site and leaves the choice of which to delete to you.

---

## How to fix it

Delete the redundant declaration, or rename one so each name describes what it does:

```gala
package main

func greet(name string) string = "hello " + name
func greetInformally(other string) string = "hi " + other

func main() {
    Println(greet("world"))
    Println(greetInformally("world"))
}
```

If you reached for a second definition because you wanted to accept different argument shapes, GALA's answer is not overloading but **default parameters**:

```gala
package main

func greet(name string, formal bool = true) string =
    if (formal) s"hello $name" else s"hi $name"

func main() {
    Println(greet("world"))
    Println(greet("world", false))
}
```

…or a **sealed type** matched inside one function, when the shapes genuinely differ.

---

## Why the rule exists

The analyzer keys top-level functions by name in a single per-package map, so a second declaration silently overwrote the first. The later definition won, the earlier was lost, and call sites that expected the first got the second's body — a whole function disappearing with no diagnostic. Because the map is populated across the whole package, the same silent overwrite applied whether the duplicate sat in one file or in two.

Rejecting it in the analyzer names both sites in GALA source terms instead of leaving it to the Go compiler's `greet redeclared in this block` against generated code.

**Scope.** Top-level functions anywhere in one package. Methods on a type are [GALA-E0012](/docs/errors/gala-e0012/); interface method specs are [GALA-E0029](/docs/errors/gala-e0029/).

---

## Related redeclaration codes

[GALA-E0011](/docs/errors/gala-e0011/) types · [GALA-E0012](/docs/errors/gala-e0012/) methods · **GALA-E0027** functions · [GALA-E0028](/docs/errors/gala-e0028/) type aliases · [GALA-E0029](/docs/errors/gala-e0029/) interface method specs · [GALA-E0030](/docs/errors/gala-e0030/) struct fields · [GALA-E0031](/docs/errors/gala-e0031/) sealed cases

---

## Related

- [Language Reference](/docs/language-reference/) — function declarations and default arguments
- [Sealed Types](/features/sealed-types/) — one function over several input shapes
- [All GALA error codes](/docs/errors/)
