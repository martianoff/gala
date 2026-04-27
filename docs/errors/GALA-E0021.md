# GALA-E0021 — Type mismatch (unification failure)

**When it fires.** Two types that the inference engine required to
agree could not be unified. Covers every failure of the
Hindley-Milner unification step, including (but not limited to):

- A function argument's type does not match the parameter type.
- The two arms of an `if` expression produce different types.
- The condition of an `if` expression is not `bool`.
- A constructor's positional argument does not match the declared
  field type.

The single code lets users grep for one identifier rather than a
zoo of per-site identifiers; the message describes the specific
mismatch.

**Error output (representative).**

```
[SemanticError GALA-E0021] line 0:0 cannot unify Int and String (hint: the expression's type does not match what the surrounding context expects — annotate the binding or convert the value explicitly)

[SemanticError GALA-E0021] line 0:0 if condition must be bool, got String (hint: the expression's type does not match what the surrounding context expects — annotate the binding or convert the value explicitly)

[SemanticError GALA-E0021] line 0:0 if branches must have same type: Int and String (hint: the expression's type does not match what the surrounding context expects — annotate the binding or convert the value explicitly)
```

**Fix.** Usually one of three:

1. **Convert one side** — e.g. `Int.toString(n)` to coerce an `Int`
   to `String`.
2. **Annotate the variable** — when the inference engine has too
   little context, an explicit type forces it to commit and the
   real mismatch surfaces against a fixed reference type.
3. **Restructure the expression** — if both branches of an `if`
   honestly produce different types, you usually want a sealed type
   (`Either[A, B]`, `Option[A]`) instead of mixing the values.

**Rationale.** Promoting these errors out of `fmt.Errorf` lets tools
and CI surface them with a stable identifier. The shared code matches
how the Go compiler reports its own mismatches under one bucket
(`cannot use X as Y`) — users learn the code once and recognize it
across every unification site.

**Scope.** Inference failures from `internal/transpiler/infer/`. Type
errors emitted by the transformer outside the inferer (e.g. specific
sealed-variant arity checks) still use their own dedicated codes.
