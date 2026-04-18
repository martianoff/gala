# GALA-E0009 — Unknown pattern type

**When it fires.** The transformer reached a pattern AST node it does not
recognize. This is defensive — the ANTLR grammar is supposed to prevent any
pattern shape that the transformer cannot handle from being parsed in the
first place.

**Minimal repro.** None — reaching this error means either

1. a new grammar rule for patterns was added but no transformer case was
   written for it, or
2. the grammar accepts a shape that the transformer has never supported.

**Error output.**

```
[SemanticError GALA-E0009] main.gala:L:C unrecognized pattern syntax (internal type *grammar.FooPatternContext) (hint: this usually means a new grammar rule is missing transformer support; please report at github.com/martianoff/gala/issues)
```

**Fix.** Report the issue with the offending source file and the exact
context type shown in the error. The transpiler needs a new case in
`transformPatternWithType` (`internal/transpiler/transformer/patterns.go`).

**Rationale.** Before this code existed the site used a raw
`fmt.Errorf("unknown pattern type: %T", patCtx)` which produced an
untracked error without a source span. Users who hit it had no way to
distinguish "transpiler bug" from "my syntax is wrong". The coded error
makes the bug class explicit so issues can be filed with a queryable tag.
