# GALA-E0016 — Struct field name collides with type name

**When it fires.** A *generic* struct's field name is the same as another
*generic* type (struct or sealed type) defined in the same package. Both
the containing struct and the shadowed type must declare type parameters —
the codegen bug this rule guards against does not manifest without
generics, and non-generic patterns like `struct Route(Handler Handler)`
remain accepted.

**Minimal repro.**

```gala
package main

sealed type Mode[T any] {
    case A(Fn func(int) T)
    case B(Fn func(string) T)
}

// Field `Mode` collides with sealed type `Mode`.
struct Box[T any](Mode Mode[T])
```

**Error output.**

```
[SemanticError GALA-E0016] file.gala:9:18 field "Mode" in generic "Box" shares its name with generic type "Mode" in package "main" (hint: rename the field (e.g. "Mode" → "M") so it does not shadow the type name)
```

**Fix.** Rename the field (or rename the type) so the field's identifier
is distinct from any type-name in the same package. Idiomatic GALA picks
short field names that hint at the role — `Mode HarnessMode[T]` over
`Mode Mode[T]`, or `M Mode[T]` for a leaner option.

**Rationale.** When a `match` scrutinee is a field-access whose field
shares a name with a type in scope, the IIFE param-type generator
currently emits duplicated generic args (e.g. `func(obj Mode[T][T]) T {…}`)
producing invalid Go. The bug is upstream of generated code, but the
collision itself is also a semantic smell — `b.Mode` on a `Box[T any]`
where `Mode` is also a type forces every reader to disambiguate field
vs. type. Rejecting at the analyzer makes the failure obvious and points
at the cheap fix (rename the field).

**Scope.** Limited to struct/shorthand-struct fields whose name matches
another *type* declared in the same package. Field-name vs. function-name,
field-name vs. variable-name, and field-name == own-struct-name are not
flagged — Go itself does not reject the latter and they don't trip the
codegen bug this rule is designed to surface.
