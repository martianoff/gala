# GALA-E0006 — Multiple default cases

**When it fires.** A `match` expression has more than one default branch —
either multiple `case _ =>` clauses, or a mix of `case _ =>` and a plain
binding pattern (`case n =>`) that also catches everything.

**Minimal repro.** (`main.gala`)

```gala
package main

func name(n int) string = n match {
    case 1 => "one"
    case _ => "other"
    case _ => "also-other"
}

func main() {
    Println(name(1))
}
```

**Error output.** The caret sits on the *second* default — the first one is
legal, so the offending token is the duplicate.

```text
error[GALA-E0006]: multiple default cases in match expression
  --> main.gala:6:5
  |
6 |     case _ => "also-other"
  |     ^^^^ keep one default case
  |
  = hint: keep one default case; combine logic with guards or nested matches if you need sub-cases
```

The `-->` line echoes the source path as the compiler resolved it; the CLI
prints it absolute.

**Fix.** Keep exactly one default. If you need to distinguish several
"other" groups, move the extra logic into the default body with an `if`
(GALA's `if` condition is parenthesized, and the `if` is an expression that
yields the branch value):

```gala
func name(n int) string = n match {
    case 1 => "one"
    case _ => if (n > 100) "big" else "other"
}
```

**Rationale.** The second default is unreachable — GALA's match emits the
default as the final `else` of the generated if-else chain, so any branch
after it would be dead code. Making it a compile error means authors notice
the duplication instead of wondering why a branch never fires.

**Related work.** The check fires on the second default encountered while
walking the case clauses; both the expression-position and statement-position
match lowerings emit identical text for it.
