# GALA-E0032 — Dot-import symbol collision

**When it fires.** Two or more **dot-imported** packages export the same
identifier. A dot import (`import . "path"`) drops every exported name of the
package into the current file's namespace; when two of them supply the same
name, the generated Go contains two declarations of that identifier and Go
rejects it with "redeclared in this block".

The check needs at least two dot imports to run, and considers every exported
name the analyzer knows about: GALA types (including sealed-variant companions),
functions, companion objects, and the exports of dot-imported Go packages.
It runs early in transformation, before any expression is transformed.

**Minimal repro.**

`pkg_a/a.gala`:

```gala
package pkg_a

func Greet() string = "hello from a"
```

`pkg_b/b.gala`:

```gala
package pkg_b

func Greet() string = "hello from b"
```

`main.gala`:

```gala
package main

import (
    . "example.com/demo/pkg_a"
    . "example.com/demo/pkg_b"
)

func main() {
    Println("hi")
}
```

**Error output.**

```
error[GALA-E0032]: dot-import symbol collision(s) detected:
  - symbol "Greet" is exported by multiple dot-imported packages: pkg_a, pkg_b
Use a qualified or aliased import for one of the packages to resolve the conflict.
(Some stdlib packages intentionally re-export names from another package as a convenience facade — e.g. `concurrent` re-exports `go_interop`'s execution-context helpers — so dot-importing both is never meaningful. Pick the facade you want.)
  --> main.gala:3:1
  |
3 | import (
  | ^^^^^^ qualify or alias one of the dot-imports to disambiguate
  |
  = hint: qualify or alias one of the dot-imports to disambiguate
```

Every colliding symbol is listed, one bullet per name, with the packages that
supply it. The caret points at the import block rather than a single import,
because the collision is a property of the set.

**Fix.** Demote one of the dot imports to a qualified import and prefix its uses:

```gala
package main

import (
    . "example.com/demo/pkg_a"
    "example.com/demo/pkg_b"
)

func main() {
    Println(Greet())            // pkg_a, via the dot import
    Println(pkg_b.Greet())      // pkg_b, explicitly qualified
}
```

An alias works too when you want both short:

```gala
import (
    . "example.com/demo/pkg_a"
    b "example.com/demo/pkg_b"
)
```

**Facade packages.** Some stdlib packages deliberately re-export another
package's names as a convenience facade — `concurrent` re-exports
`go_interop`'s execution-context helpers, for instance. Dot-importing both
sides of a facade is never useful: pick whichever one you actually want and
drop the other.

**Rationale.** Without this check the collision surfaced from `go build`,
against generated code, naming a file the author never wrote. Detecting it at
the GALA level names the symbol, names every package that supplies it, and
points at the import block that has to change. It was previously reported as an
uncoded semantic error; the stable code lets tooling distinguish an import
conflict from other semantic failures.

**Scope.** Dot imports only. A qualified import can never collide, because its
names live behind the package selector. A collision between a dot-imported
name and a *local* declaration is not flagged here — local declarations shadow
dot imports by design.

**Related.** [GALA-E0026](GALA-E0026.md) covers ambiguity between sealed-variant
constructors from two dot-imported packages. Because a sealed case registers a
companion type under its own name, that situation is caught by this check first.
