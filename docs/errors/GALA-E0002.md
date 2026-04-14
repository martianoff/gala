# GALA-E0002 — Non-exhaustive sealed match

**When it fires.** A `match` expression on a sealed type omits one or more
variants and has no default (`case _ =>`) fallback.

**Minimal repro.**

```gala
sealed type Color {
    case Red()
    case Green()
    case Blue()
}

func name(c Color) string = c match {
    case Red()   => "red"
    case Green() => "green"
    // missing: Blue
}
```

**Error output.**

```
[SemanticError GALA-E0002] main.gala:7:28 non-exhaustive match: missing cases: Blue
```

**Fix.** Either cover every variant explicitly:

```gala
func name(c Color) string = c match {
    case Red()   => "red"
    case Green() => "green"
    case Blue()  => "blue"
}
```

Or add a default branch when the remaining variants all collapse to the
same result:

```gala
func name(c Color) string = c match {
    case Red() => "red"
    case _     => "other"
}
```

**Rationale.** Sealed types exist specifically so the compiler can verify
you've thought about every variant. Allowing a silent fall-through on an
uncovered case would defeat the point of sealing — you'd get the same risk
as a `switch` with a forgotten case. GALA therefore requires either full
coverage or an explicit acknowledgment (`case _ =>`) that some variants
should be grouped.

**Related work.** Exhaustiveness introduced by the boolean / sealed match
checker in B6 / A8 (PRs #166, #167). The same machinery emits GALA-E0004
when the *arity* of a variant pattern is wrong.
