# GALA-E0018 — Sealed variant type parameter cannot be inferred

**When it fires.** A zero-arg sealed-variant constructor is used in a
position where the parent sealed type's parameter cannot be pinned. The
transpiler walks three signals before giving up:

1. The enclosing `match` subject's type (e.g. `cmd match { case NoCmd() => ... }`).
2. The enclosing function's declared return type.
3. A local `val`/`var` annotation supplying an expected type.

If none of those resolve the parameter, generated Go would have to
emit a literal `Variant{}` whose type parameter Go cannot deduce —
producing an obscure `cannot infer T` error far from the GALA source.
This code surfaces the failure at the GALA call site instead.

**Minimal repro.** (`main.gala`)

```gala
package main

sealed type Box[T any] {
    case Empty()
    case Filled(value T)
}

func main() {
    val x = Empty()
    Println(x)
}
```

**Error output.** Two details often surprise readers:

* the message names only the constructor (`"Empty()"`), not the parent sealed
  type — the parent appears in the hint, not in the header; and
* the caret is a single `^` on the **closing paren** of `Empty()`, because the
  diagnostic is anchored at the call suffix rather than at the callee name.

```text
error[GALA-E0018]: cannot infer type parameter for sealed variant constructor "Empty()"
  --> main.gala:9:18
  |
9 |     val x = Empty()
  |                  ^ annotate the binding
  |
  = hint: annotate the binding (e.g. `val x Box[int] = Empty()`) or pass type args explicitly (`Empty[int]()`)
```

The `-->` line echoes the source path as the compiler resolved it; the CLI
prints it absolute. The hint names your actual parent sealed type, so the
example it prints is copy-pasteable as-is.

**Fix.** Pick the form that documents intent best. Both compile:

```gala
val x Box[int] = Empty()   // declarative; reads as a binding, not a call
```

```gala
val x = Empty[int]()       // explicit instantiation; useful when the type param is the operative information
```

Note the annotation syntax: in GALA the type follows the binding name with
**no colon** (`val x Box[int] = ...`), and primitive types are spelled
lowercase (`int`, not `Int`).

Or, when the constructor is an arm of a `match` against a value of the
parent type, the inference signal is already present and no annotation
is needed.

**Rationale.** GALA's downward inference relies on a single
expected-type signal flowing from the enclosing context. When that
signal is absent, the transpiler has historically fallen through to an
untyped composite literal and let Go deduce — which fails confusingly.
Failing fast, with a hint that names the resolving signals, points the user
at the cheapest fix.

**Scope.** Only zero-arg sealed-variant constructors of *generic*
sealed types written without explicit type args. Variants of non-generic
sealed types, explicit `Variant[T]()` shapes, and constructors that take
arguments contributing to inference are unaffected.
