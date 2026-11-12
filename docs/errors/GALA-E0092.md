# GALA-E0092 — Copy names a field the sealed value cannot have

**When it fires.** `Copy(field = ...)` on a value of a sealed type names a
field that `Copy` cannot set:

- the field belongs to some cases but not all of them. A value whose type is
  the sealed type may be any of its cases, so `Copy` sets only a field every
  case has;
- the value is a constructor call, `Sq(2.0, "s").Copy(r = 9.0)`, whose case has
  no such field;
- no case of the type has a field of that name;
- the cases store it separately, because they give it different types (as
  `case A(x string)` and `case B(x int, y int)` do) or because the type has
  a method of that name, so `Copy` cannot tell which one is meant.

**Minimal repro.**

```gala
package main

sealed type Shape {
    case Circle(r float64, label string)
    case Sq(side float64, label string)
}

func main() {
    val s Shape = Sq(2.0, "s")
    Println(s.Copy(r = 9.0))
}
```

**Error output.**

```
error[GALA-E0092]: r is a field of Circle but not of Sq, and a Shape value may be any of its cases
  --> main.gala:10:20
   |
10 |     Println(s.Copy(r = 9.0))
   |                    ^ match the case and build it with the new r
   |
   = hint: match the case and build it with the new r; for example `case Circle(_, label) => Circle(r, label)`
```

**Fix.** Match the case and build it anew with the new field, keeping the
other fields from the pattern. Every other case is left as it is. A field
that every case has, such as `label` here, can still be set with `Copy`:

```gala
package main

sealed type Shape {
    case Circle(r float64, label string)
    case Sq(side float64, label string)
}

func grow(s Shape) Shape = s match {
    case Circle(_, label) => Circle(9.0, label)
    case _ => s
}

func main() {
    val s Shape = Sq(2.0, "s")
    Println(grow(s), grow(Circle(1.0, "c")), s.Copy(label = "t"))
}
```

On a constructor call the case is known, so `Copy` sets that case's own
fields: `Sq(2.0, "s").Copy(side = 3.0)`.

**Rationale.** A sealed value holds the fields of one case. `Copy` used to
check the name against the struct that stores every case's fields, so it
could set a field of another case: the copy printed as the original, yet was
no longer equal to it.
