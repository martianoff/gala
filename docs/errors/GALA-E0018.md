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

**Minimal repro.**

```gala
package main

sealed type Box[T] = case Empty()

func main(): Unit = {
    val x = Empty()  // No annotation, no enclosing type — parameter T is unbound
    Println(x)
}
```

**Error output.**

```
[SemanticError GALA-E0018] line 5:13 cannot infer type parameter for sealed variant constructor "Empty()" of "Box" (hint: annotate the variable (e.g. `val x: Box[Int] = Empty()`) or pass type args explicitly (`Empty[Int]()`))
```

**Fix.** Pick the form that documents intent best:

```gala
val x: Box[Int] = Empty()   // declarative; reads as a binding, not a call
val x = Empty[Int]()         // explicit instantiation; useful when the type param is the operative information
```

Or, when the constructor is an arm of a `match` against a value of the
parent type, the inference signal is already present and no annotation
is needed.

**Rationale.** GALA's downward inference relies on a single
expected-type signal flowing from the enclosing context. When that
signal is absent, the transpiler has historically fallen through to an
untyped composite literal and let Go deduce — which fails confusingly.
Failing fast, with a hint that names the three resolving signals,
points the user at the cheapest fix.

**Scope.** Only zero-arg sealed-variant constructors of *generic*
sealed types. Variants of non-generic sealed types and constructors
that take arguments contributing to inference are unaffected.
