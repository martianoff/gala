# GALA-E0063 — Direct conversion between two opaque types

**When it fires.** A value of one opaque type is converted straight into
another opaque type, `OrderID(userID)`. Go allows the conversion when the two
share an underlying type; GALA does not.

**Minimal repro.**

```gala
package main

opaque type UserID int64
opaque type OrderID int64

func orderFor(id UserID) OrderID = OrderID(id)

func main() {
    Println(orderFor(UserID(7)))
}
```

**Error output.**

```
error[GALA-E0063]: cannot convert UserID to OrderID directly: they are different opaque types
  --> main.gala:6:44
  |
6 | func orderFor(id UserID) OrderID = OrderID(id)
  |                                            ^^ convert through the underlying type if this is intended: Ord…
  |
  = hint: convert through the underlying type if this is intended: OrderID(int64(id))
```

**Fix.** When the change of kind is what you mean, convert through the
underlying type, which says so:

```gala
package main

opaque type UserID int64
opaque type OrderID int64

func orderFor(id UserID) OrderID = OrderID(int64(id))

func main() {
    Println(orderFor(UserID(7)))
}
```

More often the conversion is the bug: a user's ID passed where an order's ID
belongs. Then the fix is to pass the right value.

**Rationale.** Opaque types exist to keep values of the same representation
apart — a user ID and an order ID are both `int64`, and mixing them up is the
mistake the types are there to catch. A direct conversion between two of them
reads like an ordinary construction and hides exactly that mistake. Going
through the underlying type keeps the conversion possible and makes it
visible. Conversions between an opaque type and its own underlying type,
`UserID(n)` and `int64(id)`, are unaffected.

**Related.** [Opaque Types](../GALA.MD#opaque-types),
[GALA-E0064](GALA-E0064.md) for the implicit form of the same mix-up.
