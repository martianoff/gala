---
layout: default
title: "GALA Playground — Try Golang Sum Types and Pattern Matching Online"
description: "Try GALA in your browser — no installation required. Write and run code with sum types, pattern matching, Option/Either/Try, and functional collections. See what Go is missing, live."
keywords: "gala playground, try gala online, gala online compiler, try golang sum types online, go pattern matching playground, gala browser, golang functional programming online"
permalink: /playground/
last_modified_at: 2026-07-05
---

<div class="breadcrumb">
  <a href="{{ '/' | relative_url }}">Home</a> &raquo; Playground
</div>

# GALA Playground -- Try GALA in Your Browser

The GALA Playground lets you write, compile, and run GALA code directly in your browser. No installation, no setup -- just open it and start coding.

<p style="text-align: center; margin: 2rem 0;">
  <a href="https://gala-playground.fly.dev" class="cta">Open Playground</a>
</p>

The playground transpiles your GALA code to Go and runs it on the server. You get real compiler output, real error messages, and real program results. It is the same transpiler that runs locally when you install GALA.

---

## What You Can Try

The playground comes with 9 built-in examples that demonstrate GALA's core features. Select any example from the dropdown to load it instantly:

1. **Hello World** -- the simplest GALA program
2. **Structs and Pattern Matching** -- immutable structs with destructuring and guards
3. **Sealed Types** -- closed type hierarchies with exhaustive matching
4. **Option Monad** -- `Some`/`None` with `Map`, `FlatMap`, `GetOrElse`
5. **Either Type** -- `Left`/`Right` disjoint unions with monadic chaining
6. **Try Error Handling** -- failable computations with `Map`, `FlatMap`, `Recover`
7. **Functional Collections** -- `Array`, `List`, `HashMap` with `Map`, `Filter`, `FoldLeft`
8. **String Interpolation** -- `s"..."` and `f"..."` string templates
9. **Tuples and Destructuring** -- pair and triple values with pattern matching

---

## Quick Example

Paste this into the playground to see sealed types, pattern matching, and string interpolation working together:

```gala
package main

import . "martianoff/gala/collection_immutable"

sealed type Animal {
    case Dog(Name string, Tricks int)
    case Cat(Name string, Indoor bool)
    case Fish(Species string)
}

func describe(a Animal) string = a match {
    case Dog(name, tricks) if tricks > 5 => s"$name is a talented dog with $tricks tricks"
    case Dog(name, _)                    => s"$name is a good dog"
    case Cat(name, true)                 => s"$name is an indoor cat"
    case Cat(name, false)                => s"$name is an outdoor cat"
    case Fish(species)                   => s"a $species fish"
}

func main() {
    val animals = ArrayOf(
        Dog("Rex", 8),
        Cat("Whiskers", true),
        Fish("Goldfish"),
        Dog("Buddy", 3),
        Cat("Luna", false),
    )

    animals.ForEach((a) => Println(describe(a)))
}
```

Expected output:

```
Rex is a talented dog with 8 tricks
Whiskers is an indoor cat
a Goldfish fish
Buddy is a good dog
Luna is an outdoor cat
```

This example shows:

- **Sealed types** with three variants, each carrying different fields
- **Exhaustive pattern matching** -- every variant is handled, with guard conditions on `Dog` and boolean matching on `Cat`
- **String interpolation** -- `s"..."` with embedded variables, no imports needed
- **Expression functions** -- `func describe(...) string = ...` without braces or `return`

---

## Ready to Install?

If you like what you see in the playground, install GALA locally for full project support, Bazel integration, and multi-file packages.

<p style="text-align: center; margin: 2rem 0;">
  <a href="{{ '/getting-started/' | relative_url }}" class="cta">Get Started</a>
</p>

---

## Links

- [GALA Playground](https://gala-playground.fly.dev) -- open the playground
- [Playground Source Code](https://github.com/martianoff/gala-playground) -- the playground is itself open source
- [Getting Started]({{ '/getting-started/' | relative_url }}) -- install GALA and write your first program
- [GALA vs Go]({{ '/vs-go/' | relative_url }}) -- side-by-side comparison with idiomatic Go
- [Language Specification](https://github.com/martianoff/gala/blob/master/docs/GALA.MD) -- complete reference for GALA syntax
- [GitHub Repository](https://github.com/martianoff/gala) -- source code, issues, and contributions
