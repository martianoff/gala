---
layout: default
title: "GALA-E0126 — Go Embedded Field or Interface"
description: "GALA-E0126 fires on an embedded struct field (Base, *Base) or an embedded interface (io.Reader). GALA fields are named and an interface lists its methods."
keywords: "gala-e0126, gala embedded struct, gala struct embedding, gala embedded interface, gala promoted field"
permalink: /docs/errors/gala-e0126/
last_modified_at: 2026-11-15
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0126</p>

# GALA-E0126 — Go embedded field or interface

**When it fires.** A struct embeds a type, as a field with no name (`Base`,
`*Base`, `sync.Mutex`), or an interface embeds another interface
(`io.Reader`). GALA's struct fields are named, and an interface lists its
methods.

**Minimal repro.**

```gala
package main

type Base struct {
    ID int
}

type User struct {
    Base
    Name string
}

func main() {
    Println("users")
}
```

**Error output.**

```
error[GALA-E0126]: Go embedded field `Base`: GALA struct fields are named
  --> main.gala:8:5
  |
8 |     Base
  |     ^^^^ name the field
  |
  = hint: name the field; here, `Base Base`, and reach its members through it (`v.Base.Field`)
```

**Fix.** Name the field, usually after its type, and reach the embedded
type's members through it: `u.Base.ID` for Go's promoted `u.ID`:

```gala
package main

type Base struct {
    ID int
}

type User struct {
    Base Base
    Name string
}

func main() {
    val u = User(Base = Base(ID = 7), Name = "Ada")
    Println(u.Base.ID, u.Name)
}
```

For an embedded interface, write its methods in the interface that embeds
it. A type that has to embed for a Go API (a struct that must satisfy an
interface through a promoted method) goes in a `.go` file of the same
package.

**Rationale.** An embedded field used to be read as a field whose type was
the next line's name, and the parser lost its place for the rest of the file:
`extraneous input 'main' expecting '('` at the next function, and more after
it. It is now this one error at the embedded type.

**Scope.** A field whose type is on the next line, a name and then a type
alone on the line after it, is a named field and is not reported. A type-set
element in an interface, `int | float64` or `~int`, is
[GALA-E0127](/docs/errors/gala-e0127/).

---

## Related

- [Porting from Go](/docs/porting-from-go/)
- [All GALA error codes](/docs/errors/)
