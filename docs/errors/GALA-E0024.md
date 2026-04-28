# GALA-E0024 — Internal inference failure

**When it fires.** The inference engine was asked to infer the type
of an expression node it does not know how to handle. This is a
transpiler bug — well-formed GALA source should always reach a
recognized branch. If you see this code, please file an issue with
the offending snippet so the missing case can be added.

**Error output.**

```
[SemanticError GALA-E0024] line 0:0 unknown expression type: *infer.SomeNewNode (hint: please file an issue at https://github.com/martianoff/gala/issues with the source that triggered this failure)
```

**Fix (user).** None directly. As a workaround, simplify the
surrounding expression and re-run; if a simpler form transpiles
cleanly, the shape of the original is the trigger.

**Fix (transpiler).** Add the missing case to the inferer's main
`switch` and emit a coded diagnostic with a real source span if the
new shape can fail.

**Rationale.** Mirrors `GALA-E0017` (internal transformer panic) but
for the inference layer. Wrapping these failures with a code gives
maintainers a stable search target and end users a single hint
about how to file the bug.
