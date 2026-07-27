# GALA-E0017 — Internal transpiler panic

> **This code indicates a transpiler defect, not a user error.** Well-formed
> GALA source should never produce it. If you see it, the correct response is to
> **file a bug report with the source that triggered it** — not to change your
> GALA code.

**When it fires.** A `panic` raised anywhere inside the transformer was caught
by the top-level `recover` in `Transform`
(`internal/transpiler/transformer/transformer.go`) and converted into a coded
error. Any panic that is not already a `*galaerr.SemanticError` lands here, with
the recovered value preserved in the message.

**Minimal repro.** None. A panic is by definition an unintended path, so there
is no user-triggerable repro to show; a specific input that reaches it is a bug
to be fixed, not a documented trigger.

**Error output.** The shape the code produces, from the emit site:

```
[SemanticError GALA-E0017] line L:C internal transpiler panic: <recovered value> (hint: please file an issue at https://github.com/martianoff/gala/issues with the source that triggered this panic)
```

The position is the last source location the transformer recorded before the
panic, so it is a hint about where the failure happened, not a precise span.

**What to do.** File an issue at
[github.com/martianoff/gala/issues](https://github.com/martianoff/gala/issues)
with the source that triggered the panic and the full message. The recovered
value in the message is the most useful part of the report.

Reducing the input to the smallest snippet that still panics helps the report —
but that is a bug report, not a fix.

**Fix (transpiler).** Replace the underlying `panic(...)` site with either
`galaerr.NewCodedSemanticError(...)` — when the cause is something user-facing
that deserves its own code — or a documented invariant comment plus a panic that
names the invariant, when the branch really is unreachable. The audit in
`panic_audit_test.go` enumerates the production panic sites still awaiting this
treatment.

**Rationale.** Before this code existed, an unguarded panic surfaced as a raw Go
stack trace from the CLI: unactionable for users and easy for maintainers to
overlook, because no error code referenced it. Wrapping at the recover seam
gives both groups a single search target.
