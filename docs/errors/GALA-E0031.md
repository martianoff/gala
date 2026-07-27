# GALA-E0031 — Sealed variant case redeclared

**When it fires.** A `sealed type` declaration lists two `case` variants with
the same name.

Every sealed case generates a companion type plus `Apply`, `Unapply`, and an
`isXxx` predicate. Two cases sharing a name generate the same set twice, so the
later case's definitions overwrite the earlier's and only one variant remains
reachable — code the author wrote silently disappears.

**Minimal repro.**

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

**Error output.**

```
error[GALA-E0031]: sealed case "Box" already declared in sealed type "Shape"
  --> main.gala:5:10
  |
5 |     case Box(H int)
  |          ^^^ rename or remove the duplicate case
  |
  = hint: rename or remove the duplicate case
```

**Fix.** Give each case a name that describes the shape it carries. Cases that
differ in their fields are different cases:

```gala
package main

sealed type Shape {
    case Rect(W int, H int)
    case Square(Side int)
}

func area(s Shape) int = s match {
    case Rect(w, h) => w * h
    case Square(side) => side * side
}

func main() {
    Println(area(Rect(2, 3)))
    Println(area(Square(4)))
}
```

If both cases genuinely carry the same data and you only wanted one, delete the
duplicate.

**Rationale.** This is the sealed-type analogue of
[GALA-E0030](GALA-E0030.md): a silent overwrite that removes reachable code. It
was especially hard to spot because the resulting program still compiled — a
`match` over the sealed type would simply never select the lost variant, and
exhaustiveness checking ([GALA-E0002](GALA-E0002.md)) saw only the surviving
one, so it did not complain either. Rejecting at the declaration is the only
place the mistake is visible.

**Scope.** Cases within a single `sealed type` declaration. Two *different*
sealed types in the same package that each declare a case of the same name are
**not** covered by this check — they are caught later by the Go compiler,
because both cases generate a companion type under the same name:

```
Box redeclared in this block
method Box.Apply already declared at ...
method Box.Unapply already declared at ...
```

That still fails the build, but the message is phrased against the generated Go
and cites two line numbers per conflict (exact wording tracks your Go toolchain
version), so prefer distinct case names across every sealed type in a package.

**Related redeclaration codes.** [GALA-E0011](GALA-E0011.md) types · [GALA-E0012](GALA-E0012.md) methods · [GALA-E0027](GALA-E0027.md) functions · [GALA-E0028](GALA-E0028.md) type aliases · [GALA-E0029](GALA-E0029.md) interface method specs · [GALA-E0030](GALA-E0030.md) struct fields · [GALA-E0031](GALA-E0031.md) sealed cases.
