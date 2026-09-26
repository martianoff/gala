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
[SemanticError GALA-E0017] line L:C internal transpiler panic: <recovered value> (hint: not this line's fault: an internal transpiler defect; the position is the transformer's last known location, not a diagnosis, and a panic raised on a parse worker can name a different line on every run, so re-running may succeed. Please file an issue at https://github.com/martianoff/gala/issues with the source that triggered this panic)
```

**The position is not a diagnosis.** It is the last source location the
transformer recorded before the panic. When the panic is raised on one of the
analyzer's concurrent parse workers it is not even approximately the cause: it
is whichever file and line the crashing worker happened to hold, and it can
differ on the next run of byte-identical input. One report of about a dozen
such aborts in a single day named five unrelated lines — a generic call, a
range loop, a plain call, a named-argument list — sharing no construct at all,
and every one of them transpiled cleanly on retry with no edit.

So: if you hit E0017, re-run the same command before investigating anything. A
success on retry does not mean the problem is gone, but it does tell you the
line in the message is innocent and saves you from reading it. Either way the
transpiler is at fault and the report is worth filing.

**What to do.** File an issue at
[github.com/martianoff/gala/issues](https://github.com/martianoff/gala/issues)
with the source that triggered the panic and the full message. The recovered
value in the message is the most useful part of the report.

Reducing the input to the smallest snippet that still panics helps the report —
but that is a bug report, not a fix.

**Diagnosing one.** Set `GALA_PANIC_STACK=1` to print the recovered value and
the Go stack behind the panic to stderr before it is wrapped. The framed
GALA-E0017 message names the source position but not the transformer site; the
stack names the function that panicked.

```
GALA_PANIC_STACK=1 gala transpile repro.gala
```

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
