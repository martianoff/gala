---
layout: default
title: "GALA-E0029 — Interface Method Redeclared"
description: "\"method \"Find\" already declared in interface \"Repo\"\" — GALA-E0029 fires when an interface lists two method specs with the same name. See the compiler output, the rename, and the sealed-key alternative."
keywords: "gala-e0029, method already declared in interface, gala duplicate interface method, gala interface method specs, gala no overloading, gala generic interface"
permalink: /docs/errors/gala-e0029/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0029</p>

# GALA-E0029 — Interface method redeclared

**What it means.** An `interface` declaration lists two method specs with the same name. GALA has no overloading, so the two specs are not distinct methods — the second simply replaces the first in the interface's method table.

---

## Code that triggers it

```gala
package main

type Repo interface {
    Find(id int) string
    Find(name string) string
}

func main() {
    Println("unused")
}
```

---

## Compiler message

```
error[GALA-E0029]: method "Find" already declared in interface "Repo"
  --> main.gala:5:5
  |
5 |     Find(name string) string
  |     ^^^^ rename or remove the duplicate method
  |
  = hint: rename or remove the duplicate method
```

---

## How to fix it

Name each method for what it does. Two lookups that differ by key are two methods:

```gala
package main

type Repo interface {
    FindByID(id int) string
    FindByName(name string) string
}

struct MemoryRepo(label string)

func (r MemoryRepo) FindByID(id int) string = s"${r.label}#$id"
func (r MemoryRepo) FindByName(name string) string = s"${r.label}:$name"

func main() {
    val repo = MemoryRepo("users")
    Println(repo.FindByID(1))
    Println(repo.FindByName("ada"))
}
```

If the two specs really are one operation over different input shapes, model the input as a sealed type and take a single parameter:

```gala
sealed type Key {
    case ByID(id int)
    case ByName(name string)
}

type Repo interface {
    Find(key Key) string
}
```

A type parameter on the method *spec* is not an option: Go requires interface methods to have no type parameters, so `Find[K any](key K) string` inside an interface produces Go that does not compile (`interface method must have no type parameters`). Put the type parameter on the interface itself instead:

```gala
type Repo[K any] interface {
    Find(key K) string
}
```

---

## Why the rule exists

The earlier spec's metadata was silently overwritten by the later one. That hid a real mistake — the author believed both signatures existed — and pushed the failure to the Go compiler, which reports it against generated code (`duplicate method Find`) with no pointer back to the GALA line. Rejecting in the analyzer names the interface, the method, and the offending line.

**Scope.** Method specs inside a single `interface` declaration. Duplicate *implementations* on a concrete type are [GALA-E0012](/docs/errors/gala-e0012/).

---

## Related redeclaration codes

[GALA-E0011](/docs/errors/gala-e0011/) types · [GALA-E0012](/docs/errors/gala-e0012/) methods · [GALA-E0027](/docs/errors/gala-e0027/) functions · [GALA-E0028](/docs/errors/gala-e0028/) type aliases · **GALA-E0029** interface method specs · [GALA-E0030](/docs/errors/gala-e0030/) struct fields · [GALA-E0031](/docs/errors/gala-e0031/) sealed cases

---

## Related

- [Sealed Types](/features/sealed-types/) — one method over several input shapes
- [GALA-E0012](/docs/errors/gala-e0012/) — duplicate method *implementations*
- [All GALA error codes](/docs/errors/)
