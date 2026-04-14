# GALA-E0001 — Recursive `Immutable` wrap

**When it fires.** A value declaration or expression tries to wrap a value
inside two or more layers of `Immutable[T]`. `Immutable[T]` is the
compiler-managed wrapper around `val`-bound state; it is inserted exactly
once, and nesting it is always a bug.

**Minimal repros.**

```gala
// Explicit recursive annotation
val x Immutable[Immutable[int]] = NewImmutable(NewImmutable(1))

// Wrapping a function result that is already Immutable
func getImm() Immutable[int] = NewImmutable(1)
val y = NewImmutable(getImm())   // Immutable[Immutable[int]]
```

**Error output.**

```
[SemanticError GALA-E0001] main.gala:3:4 recursive Immutable wrapping is not allowed
```

**Fix.** Assign the inner value directly — GALA will wrap it once for you.

```gala
val x = 1                // compiler wraps as Immutable[int]
val y = getImm().Get()   // unwrap once, the outer val wraps it again
```

If you genuinely need to hand an already-wrapped value to a function that
produces another wrapped value, unwrap it first with `.Get()` and let the
caller re-wrap.

**Rationale.** `Immutable[T]` is an implementation detail of `val` bindings
that the transpiler inserts automatically. Nesting breaks the read-position
auto-unwrap (`person.name` → `person.name.Get()`) and produces Go code that
the downstream compiler cannot infer types for. Rather than silently
flattening the wrap, GALA rejects it at the source so the author notices the
redundant `NewImmutable` call.

**Related work.** Detection introduced in B2 (PR #166) — previously a naked
`panic`, now routes through the documented `raiseSemanticError` helper at
`transformer.go:raiseSemanticError`.
