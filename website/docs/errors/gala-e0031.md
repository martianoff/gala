---
layout: default
title: "GALA-E0031 — Sealed Variant Case Redeclared"
description: "\"sealed case \"Box\" already declared in sealed type \"Shape\"\" — GALA-E0031 fires when a sealed type lists two cases with the same name. See the compiler output and why the old silent overwrite still compiled."
keywords: "gala-e0031, sealed case already declared, gala duplicate sealed variant, gala sealed type cases, gala variant name collision, gala pattern matching error"
permalink: /docs/errors/gala-e0031/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0031</p>

# GALA-E0031 — Sealed variant case redeclared

**What it means.** A `sealed type` declaration lists two `case` variants with the same name.

Every sealed case generates a companion type plus `Apply`, `Unapply`, and an `isXxx` predicate. Two cases sharing a name generate the same set twice, so the later case's definitions overwrite the earlier's and only one variant remains reachable — code the author wrote silently disappears.

---

## Code that triggers it

```gala
package main

sealed type Shape {
    case Box(W int)
    case Box(H int)
}

func main() {
    val b = Box(1)
    Println(b)
}
```

---

## Compiler message

```
error[GALA-E0031]: sealed case "Box" already declared in sealed type "Shape"
  --> main.gala:5:10
  |
5 |     case Box(H int)
  |          ^^^ rename or remove the duplicate case
  |
  = hint: rename or remove the duplicate case
```

---

## How to fix it

Give each case a name that describes the shape it carries. Cases that differ in their fields are different cases:

```gala
package main

sealed type Shape {
    case Rect(W int, H int)
    case Square(Side int)
}

func area(s Shape) int = s match {
    case Rect(w, h)   => w * h
    case Square(side) => side * side
}

func main() {
    Println(area(Rect(2, 3)))
    Println(area(Square(4)))
}
```

If both cases genuinely carry the same data and you only wanted one, delete the duplicate.

---

## Why the rule exists

This is the sealed-type analogue of [GALA-E0030](/docs/errors/gala-e0030/): a silent overwrite that removes reachable code. It was especially hard to spot because the resulting program still compiled. A `match` over the sealed type would simply never select the lost variant, and exhaustiveness checking ([GALA-E0002](/docs/errors/gala-e0002/)) saw only the surviving one, so it did not complain either. Rejecting at the declaration is the only place the mistake is visible.

**Scope.** Cases within a single `sealed type` declaration. Two *different* sealed types in the same package that each declare a case of the same name are **not** covered by this check — they are caught later by the Go compiler, because both cases generate a companion type under the same name:

```
Box redeclared in this block
method Box.Apply already declared at ...
method Box.Unapply already declared at ...
```

That still fails the build, but the message is phrased against generated Go and cites two line numbers per conflict (exact wording tracks your Go toolchain version), so prefer distinct case names across every sealed type in a package.

---

## Related redeclaration codes

[GALA-E0011](/docs/errors/gala-e0011/) types · [GALA-E0012](/docs/errors/gala-e0012/) methods · [GALA-E0027](/docs/errors/gala-e0027/) functions · [GALA-E0028](/docs/errors/gala-e0028/) type aliases · [GALA-E0029](/docs/errors/gala-e0029/) interface method specs · [GALA-E0030](/docs/errors/gala-e0030/) struct fields · **GALA-E0031** sealed cases

---

## Related

- [Sealed Types](/features/sealed-types/) — closed hierarchies and generated companions
- [GALA-E0002](/docs/errors/gala-e0002/) — exhaustiveness checking over the surviving cases
- [All GALA error codes](/docs/errors/)
