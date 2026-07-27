# GALA-E0009 — Unknown pattern type

> **This code indicates a transpiler defect, not a user error.** No valid GALA
> source can force it. If you see it, the correct response is to **file a bug
> report with the source that triggered it** — not to change your GALA code.

**When it fires.** The transformer reached a pattern AST node it does not
recognize. It is the `default:` arm of the type switch over pattern contexts in
`transformPatternWithType`
(`internal/transpiler/transformer/patterns.go`) — every pattern shape the
grammar can produce is supposed to have a case above it.

Reaching it means one of:

1. a new grammar rule for patterns was added but no transformer case was
   written for it, or
2. the grammar accepts a shape the transformer has never supported.

**Minimal repro.** None. This is an internal-consistency check, unreachable
from valid user source; there is no user-triggerable repro to show.

**Error output.** The shape the code produces, from the emit site (`%T` is the
Go type of the unhandled context):

```
[SemanticError GALA-E0009] main.gala:L:C unrecognized pattern syntax (internal type *grammar.FooPatternContext) (hint: this usually means a new grammar rule is missing transformer support; please report at github.com/martianoff/gala/issues)
```

**What to do.** File an issue at
[github.com/martianoff/gala/issues](https://github.com/martianoff/gala/issues)
with the source file and the exact `internal type` shown in the message — that
type name identifies the missing case directly. Do not try to work around the
error by rewriting the pattern; the rewrite would only hide a real gap in the
transpiler.

**Fix (transpiler).** Add the missing case to `transformPatternWithType`.

**Rationale.** Before this code existed the site used a raw
`fmt.Errorf("unknown pattern type: %T", patCtx)`, which produced an untracked
error with no source span. Users who hit it had no way to tell "transpiler bug"
from "my syntax is wrong". The coded error makes the bug class explicit so
reports arrive with a queryable tag.
