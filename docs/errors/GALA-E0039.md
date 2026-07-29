# GALA-E0039 — Bare variant name in a match pattern

**When it fires.** A `case` pattern is a bare identifier that names a
field-bearing `case` variant of the type being matched — `case Circle` against a
`Shape`, or `case Some` against an `Option[T]`.

A bare identifier in pattern position is a *variable binding*, and a binding
matches every value. So the arm reads as a test for that variant but behaves as
a catch-all: it swallows every other variant, and the identifier inside the arm
body is bound to the whole match subject rather than to the variant's fields.

**Minimal repro.**

```gala
package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func main() {
    val s = Square(2.0)
    val r = s match {
        case Circle    => "circle"
        case Square(x) => s"square $x"
    }
    Println(r)
}
```

**Error output.**

```
error[GALA-E0039]: `Circle` is a variant of sealed type "Shape", so `case Circle` binds a new variable and matches every value instead of testing for Circle
  --> main.gala:11:14
   |
11 |         case Circle    => "circle"
   |              ^^^^^^ write `case Circle(_)` to match the variant, or rename the b…
   |
   = hint: write `case Circle(_)` to match the variant, or rename the binding if you meant to capture the whole value
```

**Fix.** Spell the variant with parentheses, using `_` for fields the arm does
not need:

```gala
package main

sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}

func main() {
    val s = Square(2.0)
    val r = s match {
        case Circle(_) => "circle"
        case Square(x) => s"square $x"
    }
    Println(r)
}
```

If you genuinely wanted a catch-all binding, rename it so it does not collide
with a variant of the matched type — `case other => …` — or use `case _ => …`
when the value is not needed.

**Rationale.** This is a silent wrong-answer bug, which is why it is an error
rather than a warning. The old behaviour compiled, exited 0, and printed the
result of the wrong arm; nothing in the generated Go looked suspicious, and a
reviewer reading `case Circle =>` in a diff had no visual cue that it meant
something entirely different from `case Circle(_) =>`.

Rejecting it cannot break a program that was not already misleading: a binding
named after a variant of the matched type was, by construction, acting as a
catch-all that appeared to be a variant test.

**Scope.** The check is scoped to the *matched* type. A bare identifier that
does not name a variant of the match subject's type is an ordinary binding and
is left alone, even if some unrelated type in the program declares a variant of
that name:

```gala
val n = 42
val d = n match {
    case Circle => s"bound $Circle"   // OK — `int` has no `Circle` variant
    case _      => "other"
}
```

**Zero-field variants are not affected.** For a variant that declares no fields,
the bare form is the documented shorthand and continues to work — `case Point`
is exactly `case Point()`, including when the parent sealed type is generic
(`case None` over an `Option[int]`). See
[§4 of the language reference](../GALA.MD#sealed-types).

**Related pattern codes.** [GALA-E0002](GALA-E0002.md) non-exhaustive sealed
match · [GALA-E0004](GALA-E0004.md) sealed variant arity mismatch ·
[GALA-E0005](GALA-E0005.md) missing `Unapply` on an extractor ·
[GALA-E0009](GALA-E0009.md) unknown pattern type.
