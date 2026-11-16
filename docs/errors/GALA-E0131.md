# GALA-E0131 — Go grouped type parameters

**When it fires.** Type parameters share a constraint, as Go groups them:
`func pair[K, V any](...)` or `type Pair[K, V comparable] struct { ... }`.
Each GALA type parameter has its own constraint.

**Minimal repro.**

```gala
package main

func pair[K, V any](k K, v V) Tuple[K, V] = (k, v)

func main() {
    Println(pair("a", 1))
}
```

**Error output.**

```
error[GALA-E0131]: Go grouped type parameters `[K, V any]`: each GALA type parameter has its own constraint
  --> main.gala:3:10
  |
3 | func pair[K, V any](k K, v V) Tuple[K, V] = (k, v)
  |          ^^^^^^^^^^ give each its constraint
  |
  = hint: give each its constraint; here, `[K any, V any]`
```

**Fix.** Give each type parameter its constraint:

```gala
package main

func pair[K any, V any](k K, v V) Tuple[K, V] = (k, v)

func main() {
    Println(pair("a", 1))
}
```

**Rationale.** The parser used to stop at the comma with `extraneous input
',' expecting {'[', '*', 'map', 'func', 'default', IDENTIFIER}`. It is now
this error, whose hint spells the list with every constraint written. Value
parameters follow the same rule: `func f(a, b int)` is
[GALA-E0034](GALA-E0034.md).

**Scope.** Only a declaration's type parameters are checked; type arguments,
`Map[K, V]`, are a list of types and have no constraints.
