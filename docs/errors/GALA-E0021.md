# GALA-E0021 — Type mismatch (unification failure)

> **This code is not currently surfaced as a user-facing type check.** It
> originates in the Hindley-Milner type-inference engine, which the transpiler
> uses as a type *deriver*, not as a type *checker*. Every caller discards the
> error. Searching for `GALA-E0021` because of something in your terminal will
> not find a match — type errors of this class are reported by the **Go
> compiler**, against the generated Go. See *Where type errors actually come
> from* below.

**Where it comes from.** Two types that the inference engine required to agree
could not be unified. The engine
(`internal/transpiler/infer/infer.go`) raises it from three sites:

- general unification failure — `cannot unify <A> and <B>`
- a non-boolean `if` condition — `if condition must be bool, got <T>`
- mismatched `if` arms — `if branches must have same type: <A> and <B>`

One code covers all three so tools can pin one identifier rather than a zoo of
per-site ones.

**Why you never see it.** The engine is reachable only through two bridge
methods, `inferExprType` and `inferIfType`
(`internal/transpiler/transformer/bridge.go`). Across the transformer there are
ten call sites of those two methods, and **not one propagates the error**:
eight discard it explicitly with `_`, and the remaining two bind it only to
decide whether to fall back to another inference strategy. A unification failure
therefore degrades the inferred type — it never becomes a diagnostic.

**Where type errors actually come from.** The Go compiler, after transpilation.
Both classic mismatches transpile with exit 0 and fail at build time:

```gala
package main

func add(a int, b int) int = a + b

func main() {
    Println(add(1, "two"))
}
```

```
# gala-build-workspace/gen
main.gala:6: cannot use "two" (untyped string constant) as int value in argument to add
go build: exit status 1
```

```gala
package main

func main() {
    val x = if ("nope") 1 else 2
    Println(x)
}
```

```
# gala-build-workspace/gen
main.gala:5: non-boolean condition in if statement
go build: exit status 1
```

Generated Go carries `//line` directives, so the message is attributed to your
`.gala` file and line. The **wording**, however, is Go's — it talks about
untyped constants and Go types, not GALA ones — and any construct that lowers to
something structurally different in Go (a `match` IIFE, an `Immutable[T]`
wrapper) will be described in those terms.

**Error output.** The shape the code would produce, from the emit sites:

```
[SemanticError GALA-E0021] cannot unify Int and String (hint: the expression's type does not match what the surrounding context expects — annotate the binding or convert the value explicitly)
[SemanticError GALA-E0021] if condition must be bool, got String (hint: ...)
[SemanticError GALA-E0021] if branches must have same type: Int and String (hint: ...)
```

These have not been observed from a compiler run — no caller surfaces them.

**Fix.** When the Go compiler reports the mismatch, the remedy is usually one of:

1. **Convert one side** explicitly — GALA has no implicit numeric widening or
   narrowing.
2. **Annotate the binding** — an explicit type forces inference to commit, so
   the mismatch surfaces against a fixed reference type.
3. **Restructure** — if two `if` or `match` arms honestly produce different
   types, model that with a sealed type (`Either[A, B]`, `Option[A]`) rather
   than mixing values.

**Status.** Using Go as the downstream type checker is the current, deliberate
design: the inference engine derives types for code generation, and Go verifies
them. This page documents that arrangement rather than a temporary gap. If the
inferer is ever promoted to a checking role, this code is the identifier those
diagnostics will carry.

**Scope.** The inference engine only. Type errors the transformer raises
directly — sealed-variant arity ([GALA-E0004](GALA-E0004.md)), default-parameter
mismatches ([GALA-E0014](GALA-E0014.md)), untyped parameters
([GALA-E0033](GALA-E0033.md) / [GALA-E0034](GALA-E0034.md)) — have their own
codes and *are* surfaced.
