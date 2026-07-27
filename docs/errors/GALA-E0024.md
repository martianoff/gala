# GALA-E0024 — Internal inference failure

> **This code indicates a transpiler defect, not a user error** — and it is
> **not currently surfaced** either. It originates in the Hindley-Milner
> type-inference engine, whose errors no call site propagates, so it cannot reach
> your terminal today. If a future change does surface it, the correct response
> is to **file a bug report with the source that triggered it**, not to change
> your GALA code.

**Where it comes from.** The inference engine reached the fall-through at the
end of its main `switch` — an expression node it has no case for. It is the
inference-layer counterpart of [GALA-E0017](GALA-E0017.md) (internal transformer
panic). The emit site is the tail of `infer`
(`internal/transpiler/infer/infer.go`).

**Why you never see it.** No caller propagates the inference engine's errors —
see [GALA-E0021](GALA-E0021.md) for the full explanation. The inferer is used as
a type *deriver*, never as a *checker*, so an unhandled node degrades the
inferred type instead of failing the build. The same applies to
[GALA-E0021](GALA-E0021.md) and [GALA-E0022](GALA-E0022.md); type errors of that
class are reported by the **Go compiler** against the generated Go.

**Minimal repro.** None. This is an internal-consistency check, unreachable from
valid user source and currently unreachable from any source at all.

**Error output.** The shape the code produces, from the emit site (`%T` is the
Go type of the unhandled node):

```
[SemanticError GALA-E0024] unknown expression type: *infer.SomeNewNode (hint: please file an issue at https://github.com/martianoff/gala/issues with the source that triggered this failure)
```

This has not been observed from a compiler run.

**What to do.** If you ever see it, file an issue at
[github.com/martianoff/gala/issues](https://github.com/martianoff/gala/issues)
with the source and the exact node type from the message — that type name
identifies the missing case directly. Do not rewrite your GALA to route around
it; the rewrite would only hide a real gap.

**Fix (transpiler).** Add the missing case to the inferer's main `switch`.

**Rationale.** Wrapping the fall-through with a code gives maintainers a stable
search target and, should it ever become user-visible, gives users one hint
about how to file the bug.
