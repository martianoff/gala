# GALA-E0022 — Occurs check failure

> **This code is not currently surfaced as a user-facing type check.** Like
> [GALA-E0021](GALA-E0021.md), it originates in the Hindley-Milner
> type-inference engine, which the transpiler uses as a type *deriver*, not a
> type *checker*. Every caller discards the error, so searching for
> `GALA-E0022` because of something in your terminal will not find a match.

**Where it comes from.** During unification the engine attempted to substitute a
type variable `T` with a type that *contains* `T`. Allowing it would produce an
infinite type (`T = List[T]` with no way to bottom out), so Hindley-Milner
rejects the substitution. The check lives in `bind`
(`internal/transpiler/infer/infer.go`).

**Why you never see it.** The engine is reachable only through `inferExprType`
and `inferIfType` (`internal/transpiler/transformer/bridge.go`). All ten call
sites of those two methods across the transformer discard the error — eight
with an explicit `_`, two by inspecting it only to choose a fallback inference
strategy. An occurs-check failure degrades the inferred type; it never becomes a
diagnostic.

Type errors of this class — like every other type error in GALA today — are
reported by the **Go compiler** against the generated Go. In practice an
infinite type has no Go equivalent to emit, so what surfaces is whatever the
degraded inference produced, which is usually a plain Go type error at the
recursion site.

**Minimal repro.** None. No caller surfaces the error, so there is nothing to
show from a compiler run.

**Error output.** The shape the code would produce, from the emit site:

```
[SemanticError GALA-E0022] occurs check failed: T in List[T] (hint: the inferred type would have to refer to itself; add an explicit type annotation or restructure the recursion)
```

This has not been observed from a compiler run.

**Fix.** When a recursive definition fails to build, add an explicit type
annotation at the recursion point so inference does not have to discover the
fixpoint, or restructure the value so the recursion has a base case. Annotating
the function signature is usually enough — a declared parameter and return type
give every recursive call a fixed reference type.

**Status.** Using Go as the downstream type checker is the current, deliberate
design. This page documents that arrangement rather than a temporary gap. If the
inferer is ever promoted to a checking role, this code is the identifier such
diagnostics will carry.

**Scope.** Hindley-Milner unification only. The transformer's own
recursive-type guard for `Immutable[Immutable[T]]` is
[GALA-E0001](GALA-E0001.md), and that one *is* surfaced.
