# GALA-E0006 — Multiple default cases

**When it fires.** A `match` expression has more than one default branch —
either multiple `case _ =>` clauses, or a mix of `case _ =>` and a plain
binding pattern (`case n =>`) that also catches everything.

**Minimal repro.**

```gala
func name(n int) string = n match {
    case 1 => "one"
    case _ => "other"
    case _ => "also-other"   // error: second default
}
```

**Error output.**

```
[SemanticError GALA-E0006] main.gala:3:9 multiple default cases in match expression
```

**Fix.** Keep exactly one default. If you need to distinguish several
"other" groups, move the extra logic into the default body with an `if`:

```gala
func name(n int) string = n match {
    case 1 => "one"
    case _ => {
        if n > 100 {
            "big"
        } else {
            "other"
        }
    }
}
```

**Rationale.** The second default is unreachable — GALA's match emits the
default as the final `else` of the generated if-else chain, so any branch
after it would be dead code. Making it a compile error means authors notice
the duplication instead of wondering why a branch never fires.

**Related work.** Introduced as part of A8 / structured error codes
(PR #167). The check lives in `transformMatchClauses` in `match.go` and
fires on the second default encountered while walking the case clauses.
