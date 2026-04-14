# GALA-E0003 — Missing default case

**When it fires.** A `match` expression over a non-sealed type (e.g., a
primitive or a plain struct) has no default branch. Unlike sealed matches,
the compiler cannot prove exhaustiveness for open types, so a default is
mandatory.

**Minimal repro.**

```gala
func name(n int) string = n match {
    case 1 => "one"
    case 2 => "two"
    // missing: case _ => ...
}
```

**Error output.**

```
[SemanticError GALA-E0003] main.gala:1:28 match expression must have a default case (hint: add `case _ => ...`)
```

**Fix.** Add a default case that produces a sensible fallback value:

```gala
func name(n int) string = n match {
    case 1 => "one"
    case 2 => "two"
    case _ => "other"
}
```

If you genuinely want the program to crash on unknown values, be explicit:

```gala
case _ => panic("unexpected n")
```

**Rationale.** Non-sealed types have unbounded instance sets — there is no
way for the transpiler to verify every value is covered. Without a default,
the generated Go code would be forced to synthesize one (usually a runtime
panic), hiding the missing-branch bug from the author. Requiring the default
to be written explicitly surfaces the decision at the call site.

**Related work.** Introduced alongside B6 / A8 (PRs #166, #167). Sealed
types get GALA-E0002 instead; GALA-E0003 is the open-type counterpart.
