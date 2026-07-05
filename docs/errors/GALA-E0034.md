# GALA-E0034 — Untyped function/struct parameter

**When it fires.** A function parameter, method parameter, or struct-shorthand
field is written without a type. Unlike lambda parameters (see
[GALA-E0033](GALA-E0033.md)), these declaration sites have no surrounding
context to infer a type from, so every parameter and field must state its own
type.

The usual trigger is Go-style *grouped* parameter syntax, where several names
share a single trailing type:

```gala
func add(a, b int) int = a + b       // ← GALA-E0034: `a` has no declared type

struct Point(X, Y int)               // ← GALA-E0034: `X` has no declared type
```

GALA parses `(a, b int)` as a typeless `a` followed by a typed `b int`; it does
**not** propagate the trailing type back over the earlier names. Grouped
parameters are therefore not a supported feature.

**Error output.**

```
[SemanticError GALA-E0034] file.gala:3:9 parameter "a" has no declared type (hint: type every parameter individually (e.g. `a int`); GALA does not support Go-style grouped parameters like `(a, b int)`)
```

**Fix.** Give every parameter and field its own type:

```gala
func add(a int, b int) int = a + b

struct Point(X int, Y int)
```

**Rationale.** Emitting `any` for the untyped parameter would violate GALA's
concrete-types invariant and only surface later as a confusing Go build failure
(`mismatched types any and int`). Rejecting it at the declaration site names the
exact parameter and points at the fix. GALA deliberately keeps one type per
parameter rather than adopting Go's grouped-parameter shorthand, so the rule is
uniform across function signatures, method signatures, and struct-shorthand
fields.
