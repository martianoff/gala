# GALA-E0022 — Occurs check failure

**When it fires.** During type unification, the inference engine
attempted to substitute a type variable `T` with a type that
*contains* `T`. Allowing such a substitution would produce an
infinite type (e.g. `T = List[T]` with no way to bottom out), so
Hindley-Milner rejects the unification.

In practice this surfaces from:

- A recursive value that has no fixed point (the function calls
  itself with a value of the wrong shape, and the inferer cannot
  pin a finite type).
- A pattern that destructures the *whole* value into one of its
  fields (e.g. matching a struct field against the entire struct).

**Error output.**

```
[SemanticError GALA-E0022] line 0:0 occurs check failed: T in List[T] (hint: the inferred type would have to refer to itself; add an explicit type annotation or restructure the recursion)
```

**Fix.** Add an explicit type annotation at the recursion point so
inference does not have to discover the fixpoint, or restructure the
value so the recursion has a base case the inferer can see:

```gala
// Force the type at the recursion site:
func length[T](xs List[T]) Int = ...
```

**Rationale.** Occurs check failures are extremely rare in
straight-line GALA code; when they do fire, the inferer's diagnostic
is opaque without knowing what "occurs check" means. The dedicated
code lets the docs page link out to a primer instead of cramming
type-theory into the error message.

**Scope.** Hindley-Milner unification only. The transformer's own
recursive-type guards (e.g. `Immutable[Immutable[T]]`) emit `GALA-E0001`.
