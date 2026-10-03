# GALA-E0062 — Opaque type over a type it cannot be declared over

**When it fires.** An `opaque type` is declared over something other than a
scalar. The underlying type must be `bool`, `string`, an integer or
floating-point kind (`rune` and `byte` included), an alias that names one, or a
Go named scalar such as `time.Duration`. Each other shape is rejected with its
own reason: another opaque type, a struct or sealed type (a GALA collection is
a struct), a Go slice, map, pointer or channel, an interface, a function type,
a complex number and a bare type parameter.

**Minimal repro.**

```gala
package main

opaque type UserID int64
opaque type AdminID UserID

func main() {
    Println(AdminID(1))
}
```

**Error output.**

```
error[GALA-E0062]: cannot declare opaque type "AdminID" over UserID: UserID is itself an opaque type, and an opaque type inherits nothing from the type it is declared over
  --> main.gala:4:21
  |
4 | opaque type AdminID UserID
  |                     ^^^^^^ declare AdminID over int64 instead
  |
  = hint: declare AdminID over int64 instead
```

**Fix.** Declare the opaque type over the underlying type:

```gala
package main

opaque type UserID int64
opaque type AdminID int64

func main() {
    Println(AdminID(1))
}
```

An `AdminID` built from a `UserID` is then a deliberate conversion through the
underlying type, `AdminID(int64(id))`.

**A collection or struct.** A GALA collection is a struct, and so is anything
declared with `struct`:

```gala
package main

import . "martianoff/gala/collection_immutable"

opaque type Tags Array[string]

func main() {}
```

```
error[GALA-E0062]: cannot declare opaque type "Tags" over Array[string]: Array is a struct, and a distinct type over a struct (a GALA collection included) loses all of its methods
  --> main.gala:5:18
  |
5 | opaque type Tags Array[string]
  |                  ^^^^^^^^^^^^^ use a struct to wrap it, or a type alias to name it
  |
  = hint: use a struct to wrap it, or a type alias to name it
```

Wrap it in a struct to give it an identity of its own, or name it with an
alias:

```gala
package main

import . "martianoff/gala/collection_immutable"

struct Tags(Values Array[string])

type TagList Array[string]

func main() {
    Println(Tags(ArrayOf("a")), ArrayOf("b"))
}
```

**Rationale.** An opaque type lowers to a Go defined type, `type X Y`, and a
Go defined type starts with **no** methods: nothing of `Y`'s method set is
inherited. Over a scalar that costs nothing, since a scalar's operators are
not methods. Over a struct it would drop everything that makes the type
useful — `Map` and `Filter` on a collection, the generated `Copy`, `Equal` and
`Unapply` on a struct, the variants of a sealed type. Over another opaque type
it would inherit none of that type's methods while forbidding the natural
conversion between the two ([GALA-E0063](GALA-E0063.md)). Go slices, maps,
pointers and channels are Go-interop types, an interface has no value of its
own to make distinct, function types are out of scope, and Go forbids a type
parameter as the right-hand side of a type declaration.

**Related.** [Opaque Types](../GALA.MD#opaque-types).
