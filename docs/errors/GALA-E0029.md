# GALA-E0029 — Interface method redeclared

**When it fires.** An `interface` declaration lists two method specs with the
same name. GALA has no overloading, so the two specs are not distinct methods —
the second simply replaces the first in the interface's method table.

The check is scoped to one parse of one interface declaration: re-analysing the
same source (incremental builds, LSP passes) preserves the method map across
calls and does not re-trigger it.

**Minimal repro.**

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

**Error output.**

```
error[GALA-E0029]: method "Find" already declared in interface "Repo"
  --> main.gala:5:5
  |
5 |     Find(name string) string
  |     ^^^^ rename or remove the duplicate method
  |
  = hint: rename or remove the duplicate method
```

**Fix.** Name each method for what it does. Two lookups that differ by key are
two methods:

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

If the two specs really are one operation over different input shapes, model the
input as a sealed type and take a single parameter:

```gala
sealed type Key {
    case ByID(id int)
    case ByName(name string)
}

type Repo interface {
    Find(key Key) string
}
```

A type parameter on the method *spec* is not an option: Go requires interface
methods to have no type parameters, so `Find[K any](key K) string` inside an
interface produces Go that does not compile
(`interface method must have no type parameters`). Put the type parameter on
the interface itself instead:

```gala
type Repo[K any] interface {
    Find(key K) string
}
```

**Rationale.** The earlier spec's metadata was silently overwritten by the later
one. That hid a real mistake — the author believed both signatures existed —
and pushed the failure to the Go compiler, which reports it against generated
code (`duplicate method Find`) with no pointer back to the GALA line. Rejecting
in the analyzer names the interface, the method, and the offending line.

**Scope.** Method specs inside a single `interface` declaration. Duplicate
*implementations* on a concrete type are [GALA-E0012](GALA-E0012.md).
