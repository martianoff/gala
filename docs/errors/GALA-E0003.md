# GALA-E0003 — Missing default case

**When it fires.** A `match` expression over a non-sealed type (e.g., a
primitive or a plain struct) has no default branch. Unlike sealed matches,
the compiler cannot prove exhaustiveness for open types, so a default is
mandatory.

**Minimal repro.** (`main.gala`)

```gala
package main

func name(n int) string = n match {
    case 1 => "one"
    case 2 => "two"
}

func main() {
    Println(name(1))
}
```

**Error output.** The caret sits on the first case clause — it is the match
as a whole that lacks a default, so there is no later offending token to
point at.

```text
error[GALA-E0003]: match expression must have a default case
  --> main.gala:4:5
  |
4 |     case 1 => "one"
  |     ^^^^ add `case _ => ...`
  |
  = hint: add `case _ => ...`
```

The `-->` line echoes the source path as the compiler resolved it; the CLI
prints it absolute.

**Fix.** Add a default case that produces a sensible fallback value:

```gala
func name(n int) string = n match {
    case 1 => "one"
    case 2 => "two"
    case _ => "other"
}
```

If you genuinely want the program to abort on unknown values, be explicit.
A bare `panic(...)` is a Go builtin that GALA rejects (GALA-E0035), so use
the sanctioned interop wrapper:

```gala
import "martianoff/gala/go_builtins"

func strict(n int) string = n match {
    case 1 => "one"
    case _ => {
        go_builtins.Panic(s"unexpected n: $n")
        ""
    }
}
```

**Rationale.** Non-sealed types have unbounded instance sets — there is no
way for the transpiler to verify every value is covered. Without a default,
the generated Go code would be forced to synthesize one (usually a runtime
panic), hiding the missing-branch bug from the author. Requiring the default
to be written explicitly surfaces the decision at the call site.

**Note on wording.** Two places in the transformer lower a `match` — one for
expression position, one for statement position. They now emit identical text
for this code; if you are reading an older build, the expression-position
lowering appended a redundant `(case _ => ...)` to the message.

**Related work.** Sealed types get GALA-E0002 instead; GALA-E0003 is the
open-type counterpart.
