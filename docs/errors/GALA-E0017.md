# GALA-E0017 — Internal transpiler panic

**When it fires.** A `panic` inside the transformer was caught by the
top-level recover and surfaced as a coded error. This is a transpiler
bug — well-formed GALA source should never trigger this code. If you
see it, please file an issue with the offending source snippet so the
underlying invariant can be tightened or replaced with a coded error
that names the actual problem.

**Error output.**

```
[SemanticError GALA-E0017] line 0:0 internal transpiler panic: <recovered message> (hint: please file an issue at https://github.com/martianoff/gala/issues with the source that triggered this panic)
```

**Fix (user).** None directly. As a workaround, simplify the surrounding
expression and re-run; if the simplified form transpiles cleanly, the
shape of the original expression is the trigger.

**Fix (transpiler).** Replace the underlying `panic(...)` site with
either `galaerr.NewCodedSemanticError(...)` (when the cause is
user-facing GALA) or a documented invariant comment + a panic that
identifies the invariant (when the caller really should never reach
that branch). The audit recorded in `panic_audit_test.go` enumerates
the production-code panic sites that still need this treatment.

**Rationale.** Before this code existed, an unguarded panic surfaced as
a raw Go stack trace from the CLI — surprising and unactionable for the
user, and easy for transpiler maintainers to overlook because no error
code referenced it. Wrapping at the recover seam gives both groups a
single search target.
