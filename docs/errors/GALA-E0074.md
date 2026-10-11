# GALA-E0074 — More than one default case

**When it fires.** A sealed type marks more than one case `default`. The
default case is the type's zero value — what a value nothing constructed holds
— so a sealed type has at most one.

**Minimal repro.**

```gala
package main

sealed type Light {
    default case Off()
    case Dim(Level int)
    default case Unknown()
}

func main() {
    Println(Off())
}
```

**Error output.**

```
error[GALA-E0074]: sealed type "Light" marks both "Off" and "Unknown" as its default case
  --> main.gala:6:5
  |
6 |     default case Unknown()
  |     ^^^^^^^ keep `default` on one case: the default case is the type's z…
  |
  = hint: keep `default` on one case: the default case is the type's zero value
```

**Fix.** Keep `default` on the case a value should be when nothing set it:

```gala
package main

sealed type Light {
    default case Off()
    case Dim(Level int)
    case Unknown()
}

func main() {
    Println(Off())
}
```

**Rationale.** A Go zero value of a sealed type — an uninitialized `var`, a
field a struct literal leaves out, a slot of a fresh slice — is its case with
tag 0, and the default case takes that tag. Two default cases would need the
same tag. A sealed type with no default case has no zero value: its cases are
numbered from 1, and a match on a zero value panics instead of reading it as
one of the cases. See [Sealed Types: Default Case](../GALA.MD#default-case).
