# GALA-E0077 — Type holds itself by value

**When it fires.** A struct or sealed type has a field whose type contains the
type itself by value: an `Option`, `Either`, `Try`, tuple or `List` of it,
another struct that holds it, or the type itself in a struct. A Go value cannot
contain itself, so the type would have infinite size.

**Minimal repro.**

```gala
package main

struct Node(value int, next Option[Node])

func main() {
    val n = Node(1, None[Node]())
    Println(n.value)
}
```

**Error output.**

```
error[GALA-E0077]: struct Node holds itself by value: field next has type Option[Node], which contains Node itself
  --> main.gala:3:24
  |
3 | struct Node(value int, next Option[Node])
  |                        ^^^^ a value cannot contain itself
  |
  = hint: a value cannot contain itself; declare recursive data as a sealed type whose case field has the type itself (`case Cons(head int, tail Node)`), or hold it through a pointer (`next *Node`)
```

**Fix.** Declare recursive data as a sealed type. A case field whose type is
the sealed type itself is held by reference, so the type can refer to itself:

```gala
package main

sealed type Node {
    case Last(value int)
    case Link(value int, next Node)
}

func sum(n Node) int = n match {
    case Last(v) => v
    case Link(v, next) => v + sum(next)
}

func main() {
    Println(sum(Link(1, Link(2, Last(3))))) // 6
}
```

A pointer field (`next *Node`), an `Array`, a `HashMap`, a Go slice or map and
a function also hold the type by reference and are accepted.

**Rationale.** Without the check the type reached `go build`, which rejected it
as an invalid recursive type and named the generated `std.Immutable` and
`std.Option` wrappers instead of the field.
