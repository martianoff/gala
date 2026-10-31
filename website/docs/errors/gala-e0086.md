---
layout: default
title: "GALA-E0086 — Type Parameter on an Interface Method"
description: "GALA-E0086 fires when an interface method spec declares its own type parameters, such as Map[T any]. Go interface methods cannot have them: put the type parameter on the interface, or use a generic function."
keywords: "gala-e0086, gala generic interface method, interface method must have no type parameters, gala interface type parameter, gala method type parameter interface"
permalink: /docs/errors/gala-e0086/
last_modified_at: 2026-10-30
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0086</p>

# GALA-E0086 — Type parameter on an interface method

**When it fires.** A method spec of an `interface` declares type parameters of
its own, as in `Map[T any](f func(string) T) T`. A Go interface method cannot
have type parameters, so GALA interfaces cannot either. Type parameters on the
interface itself (`type Named[T any] interface { ... }`) and on methods of a
struct are fine.

**Minimal repro.**

```gala
package main

type Named interface {
    Map[T any](f func(string) T) T
}

func main() = Println("ok")
```

**Error output.**

```
error[GALA-E0086]: interface method "Map" of "Named" declares a type parameter [T]: a Go interface method cannot have type parameters
  --> main.gala:4:5
  |
4 |     Map[T any](f func(string) T) T
  |     ^^^ declare T on the interface instead
  |
  = hint: declare T on the interface instead (type Named[T any] interface { ... }), or write Map as a generic function that takes a Named
```

**Fix.** When every value of the interface works with one `T`, put the type
parameter on the interface. Each implementation then picks its `T`:

```gala
package main

type Named[T any] interface {
    Map(f func(string) T) T
}

struct Person(name string)

func (p Person) Map(f func(string) int) int = f(p.name)

func main() {
    val n Named[int] = Person("ada")
    Println(n.Map((s) => s.Size()))
}
```

When the caller picks `T` on each call, keep the interface free of type
parameters and write the operation as a generic function that takes the
interface value:

```gala
package main

type Named interface {
    Name() string
}

struct Person(name string)

func (p Person) Name() string = p.name

func MapName[T any](n Named, f func(string) T) T = f(n.Name())

func main() {
    val n Named = Person("ada")
    Println(MapName(n, (s) => s.Size()))
    Println(MapName(n, (s) => s + "!"))
}
```

**Rationale.** A generic method of a struct can be lowered, because the
compiler sees every call and the one receiver type it calls. A call through an
interface value is dispatched at run time to whichever type the value holds,
so there is no set of instantiations to generate: an implementation would have
to provide the method for every `T` any caller in any package uses. Lowering
`T` to `any` would give up the concrete types GALA generates. The spec used to
be copied into the Go interface as written; that Go does not parse, and the
build stopped with an internal error ([GALA-E0017](/docs/errors/gala-e0017/))
instead of an error at the method.
