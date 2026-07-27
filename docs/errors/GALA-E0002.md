# GALA-E0002 — Non-exhaustive sealed match

**When it fires.** A `match` expression on a sealed type omits one or more
variants and has no default (`case _ =>`) fallback.

**Minimal repro.** (`main.gala`)

```gala
package main

sealed type Color {
    case Red()
    case Green()
    case Blue()
}

func name(c Color) string = c match {
    case Red()   => "red"
    case Green() => "green"
}

func main() {
    Println(name(Red()))
}
```

**Error output.** The caret sits on the *first* case clause: coverage is
reported against the match as a whole, and the omitted variant has no source
location of its own to point at. The inline annotation next to the caret is
the hint truncated to one line; the full hint follows below the frame.

```text
error[GALA-E0002]: non-exhaustive match: missing cases: Blue
  --> main.gala:10:5
   |
10 |     case Red()   => "red"
   |     ^^^^ add the missing variant cases, or add a `case _ => ...` defa…
   |
   = hint: add the missing variant cases, or add a `case _ => ...` default to cover them
```

The `-->` line echoes the source path as the compiler resolved it; the CLI
prints it absolute.

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

**Related work.** The same exhaustiveness machinery emits GALA-E0004 when the
*arity* of a variant pattern is wrong. GALA-E0003 is the open-type
counterpart of this check.
