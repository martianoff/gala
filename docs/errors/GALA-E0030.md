# GALA-E0030 — Struct field redeclared

**When it fires.** A struct declares two fields with the same name. Both struct
syntaxes are covered:

- **Shorthand form** — `struct Point(X int, X int)`
- **Block form** — `type Point struct { val x int; val x int }`

**Minimal repro.**

```gala
package main

struct Point(X int, X int)

func main() {
    val p = Point(1, 2)
    Println(p.X)
}
```

**Error output.**

```
error[GALA-E0030]: field "X" already declared in struct "Point"
  --> main.gala:3:21
  |
3 | struct Point(X int, X int)
  |                     ^ rename or remove the duplicate field
  |
  = hint: rename or remove the duplicate field
```

The caret points at the second occurrence of the name.

The block form reports identically:

```
error[GALA-E0030]: field "x" already declared in struct "Point"
  --> main.gala:5:9
  |
5 |     val x int
  |         ^ rename or remove the duplicate field
  |
  = hint: rename or remove the duplicate field
```

**Fix.** Rename the duplicate, or delete it if it was a copy-paste artefact:

```gala
package main

struct Point(X int, Y int)

func main() {
    val p = Point(1, 2)
    Println(s"${p.X}, ${p.Y}")
}
```

If you wanted several values under one conceptual name, hold a collection
instead of repeating the field:

```gala
import . "martianoff/gala/collection_immutable"

struct Path(Points Array[int])
```

**Rationale.** The duplicate was doubly damaging. The field-type map kept only
the later type, so the earlier field's type was lost; and the ordered
`FieldNames` list contained the name *twice*, which the generator emitted
verbatim — producing Go with a duplicated struct field that could not compile.
Because the resulting failure surfaced in generated code, it pointed at a line
the author never wrote. Rejecting in the analyzer keeps the error on the
declaration.

**Scope.** Fields within one struct declaration. A field name that collides with
a *type* name in the same package is [GALA-E0016](GALA-E0016.md); duplicate
sealed-variant case names are [GALA-E0031](GALA-E0031.md).

**Related redeclaration codes.** [GALA-E0011](GALA-E0011.md) types · [GALA-E0012](GALA-E0012.md) methods · [GALA-E0027](GALA-E0027.md) functions · [GALA-E0028](GALA-E0028.md) type aliases · [GALA-E0029](GALA-E0029.md) interface method specs · [GALA-E0030](GALA-E0030.md) struct fields · [GALA-E0031](GALA-E0031.md) sealed cases.
