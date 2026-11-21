# GALA-E0141 — Unreachable case

**When it fires.** A `match` or a partial-function literal `{ case ... }` has a
case written after an unguarded case that matches every value. Cases are tried
in the order they are written, so the later case can never run. These cases
match every value:

- `case _`;
- a plain binding, `case x`, or a capitalized name that names nothing in
  scope and so binds too (`case Rectangle` over an `int`);
- a typed pattern of the subject's own type, `case n: int` over an `int`, when
  that type is a predeclared type other than `error` or `any`, or a GALA type
  that is not an interface (a typed pattern over `any`, an interface or a type
  parameter does not match `nil`; a Go type is not counted, since whether it is
  an interface is known only from Go type information, which may be missing);
- a tuple pattern of the subject's arity whose elements are each `_`, a
  binding or such a tuple, `case (a, _)` over a `Tuple[int, string]`.

A typed pattern counts when it spells the subject's type as the subject's type
is written. In a partial-function literal, a typed or tuple pattern counts
only when the expected function type gives the parameter's type. Other
patterns are not counted, even one that happens to match every value, such as
an extractor whose fields are all bindings.

A case with a guard, `case x if x > 0`, does not match every value, so the
cases after it stay reachable. In a `match`, a second `case _` (or a second
plain binding, when there is no `case _`) is [GALA-E0006](GALA-E0006.md)
instead; in a partial-function literal it is this error too.

**Minimal repro.**

```gala
package main

func name(n int) string = n match {
    case 0 => "zero"
    case _ => "other"
    case 1 => "one"
}

func main() {
    Println(name(1))
}
```

**Error output.**

```
error[GALA-E0141]: unreachable case: `case _` on line 5 matches every value, so no case after it can run
  --> main.gala:6:5
  |
6 |     case 1 => "one"
  |     ^^^^ move this case above `case _`, or delete it
  |
  = hint: move this case above `case _`, or delete it
```

**Fix.** Move the catch-all to the end, so the specific cases are tried first:

```gala
func name(n int) string = n match {
    case 0 => "zero"
    case 1 => "one"
    case _ => "other"
}
```

If the later case really is dead, delete it.

**Rationale.** GALA follows Scala's first-match semantics: the first case that
matches wins. A `match` used to move its catch-all to the end, so the cases
written after it ran first and won, and `name(1)` above returned `"one"`
without a diagnostic. A partial-function literal kept the written order, so the
same cases gave different answers in the two forms. Rejecting the case makes
the order of the cases the order they run in, in both forms.
