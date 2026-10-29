# GALA-E0084 — Values that cannot be compared with ==

**When it fires.** `a == b` compares with the operands' `Equal` method when
their type has one — every struct, sealed type and tuple, `Option`, the
collections, and any type that declares its own — and with Go's `==`
otherwise. This error is the case where neither works: Go cannot compare the
type, and it has no `Equal`. That is an operand of type

- a function value;
- a Go map;
- a type parameter constrained by `any` (`func same[T any](a T, b T)`), whose
  type is not known statically;
- a Go type that holds a slice, map or function, such as `bytes.Buffer`.

A [stable-identifier pattern](../GALA.MD#stable-identifiers-constants-in-patterns)
(`case Answer`) compares the matched value as `==` does, and fires the same
way.

It also fires inside a type's own `Equal` method, on `==` between values of
that same type: there `==` is Go's, since calling `Equal` would call the method
being defined, and a type that holds an `Array`, a function or a Go slice is
not one Go can compare.

A comparison with `nil` is not this error. A type GALA does not know is left
to the Go compiler, and so are two Go slices compared directly: Go reports
`slice can only be compared to nil`.

**Minimal repro.**

```gala
package main

func same[T any](a T, b T) bool = a == b

func main() {
    Println(same(1, 1))
}
```

**Error output.**

```
error[GALA-E0084]: == cannot compare T values: T is a type parameter constrained by any
  --> main.gala:3:35
  |
3 | func same[T any](a T, b T) bool = a == b
  |                                   ^^^^^^ constrain T with comparable where it is declared
  |
  = hint: constrain T with comparable where it is declared (`T comparable`); or compare with `Equal(a, b)`
```

A function value reads `func(int) int is a function`, a Go map or Go type
hints `compare with Equal: Equal(m, n)`, and a stable identifier reports
`case Answer compares the matched value with Answer by ==, which cannot
compare ...`. Inside a type's own `Equal`, the message reads `== inside Bag's
own Equal is Go's ==, which cannot compare Bag values: field items has type
Array[int], ...`.

**Fix.** In a generic function, constrain the type parameter with
`comparable` when Go's `==` is what you mean, or compare with `Equal(a, b)`,
which takes any type and calls the value's `Equal` method:

```gala
package main

import . "martianoff/gala/collection_immutable"

func same[T any](a T, b T) bool = Equal(a, b)

func sameKey[T comparable](a T, b T) bool = a == b

func main() {
    Println(same(ListOf(1), ListOf(1))) // true
    Println(sameKey("a", "a"))          // true
}
```

`T comparable` keeps Go's `==`: a `List` passed as `T` compares by identity.
Use `Equal(a, b)` for value equality in generic code.

A function value cannot be compared at all: compare the values it returns.
Inside a type's own `Equal`, compare its fields one by one instead of the
whole value.

**Rationale.** Without this check, the comparison was emitted as written and
`go build` rejected it against the generated code — `incomparable types in
type set`, or a message naming the `std.Immutable` wrapper a `val` field is
stored in rather than the field.
