# GALA-E0004 — Sealed variant arity mismatch

**When it fires.** A pattern in a sealed-type `match` binds a different
number of fields than the variant declares. Using `_` for unused fields is
fine; silently truncating the tail is not.

**Minimal repro.**

```gala
sealed type Shape {
    case Rect(W int, H int, Label string)
    case Circle(R int)
}

func area(s Shape) int = s match {
    case Rect(w, h)  => w * h       // Rect has 3 fields, pattern binds 2
    case Circle(r)   => r * r * 3
}
```

**Error output.**

```
[SemanticError GALA-E0004] main.gala:6:25 sealed variant "Rect" pattern binds 2 field(s) but declares 3 (hint: use `_` for unused fields)
```

**Fix.** Bind every field — use `_` for the ones you don't care about:

```gala
case Rect(w, h, _) => w * h
```

**Rationale.** A pattern like `Rect(w, h)` looks like it matches a two-field
struct, but `Rect` has a third `Label` field. Without the arity check, the
generated code would either misalign the field bindings (silently assigning
`h` to `Label` etc.) or fail at runtime. Making arity mismatches a compile
error at the pattern site means the author sees the problem while writing
the pattern, not when the test suite diverges.

The `_` placeholder is the explicit "I know this field exists and I don't
need it" acknowledgement — it looks different from forgetting the field
entirely, which is exactly the distinction that matters.

**Related work.** Introduced as B6 in PR #166 via `validateSealedVariantArity`
in `match.go`. The arity check runs alongside the exhaustiveness check
(GALA-E0002) during `transformMatchClauses`.
