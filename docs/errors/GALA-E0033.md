# GALA-E0033 — Untyped lambda parameter

**When it fires.** A lambda parameter has no type annotation *and* the
surrounding context supplies no expected type the transpiler can use to infer
one. The canonical case is a lambda bound to a `val` with no declared type:

```gala
func main() {
    val f = (x) => x + 1          // ← GALA-E0033: x has no inferable type
    Println(f(2))
}
```

There is nothing for the transpiler to infer `x` from — the only way to give
`x` a concrete type would be to emit `any`, which violates GALA's
concrete-types invariant and produces Go that either fails to compile (when the
slot has a concrete target type) or silently erases the type.

**Error output.**

```
[SemanticError GALA-E0033] file.gala:2:14 lambda parameter "x" has no type and none can be inferred from context (hint: annotate it (e.g. `(x int) => …`) or use the lambda in a typed context (typed val, function argument, or return))
```

**Fix.** Either annotate the parameter:

```gala
val f = (x int) => x + 1
```

…or place the lambda in a typed context, which threads the declared signature
into the lambda so the parameter types are inferred:

```gala
val f func(int) int = (x) => x + 1               // declared val type
arr.Map((x) => x * 2)                            // method argument
func mk() func(int) int = (x) => x + 1           // function return
```

**Rationale.** A lambda in a typed slot draws its parameter and return types
from that slot — a declared `val` function type, a function/method argument's
`FuncType` metadata, an if-expression feeding a function-typed slot, or a
curried lambda's decomposed return type all supply the expectation. A bare
lambda initializer has no such slot, so requiring an annotation is the only way
to keep generated Go concrete. Previously such a parameter defaulted to `any`
with a warning; the error surfaces the problem at its source instead of
deferring it to a confusing downstream Go compile error (or a silent `any`).

**Scope.** Only lambda parameters in contexts that supply no expected type.
Call-argument lambdas whose parameter types the transpiler cannot yet infer
still fall back to `any` (a separate inference-completeness concern), so this
error does not fire there.
