---
layout: default
title: "GALA-E0087 — Name Is Both a std Type and a Dot-Imported Value"
description: "GALA-E0087 fires when a bare name used as a value is a std type and also a function or val of a dot-imported package, such as Void(). Qualify it: util.Void() or std.Void."
keywords: "gala-e0087, gala ambiguous name, gala dot import std type, gala dot import function std type, gala Void ambiguous, gala qualify std type"
permalink: /docs/errors/gala-e0087/
last_modified_at: 2026-10-31
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0087</p>

# GALA-E0087 — Name is both a std type and a dot-imported value

**When it fires.** A bare name is written as a value (called, passed, or read)
and two imports supply it without a qualifier:

- the std package, imported into every file implicitly, declares a **type** of
  that name (`Void`, `NoSuchElementError`, `Hashable`, `Ordered`, ...), and
- a package the file **dot-imports** (`import . "path"`) declares a
  **function** or a package-level **`val`/`var`** of that name.

Neither import outranks the other, so the transpiler does not guess.

Names your own package declares, and local bindings (parameters, `val`s,
lambda parameters), still win over both imports and never reach this check.

**Minimal repro.**

`util/util.gala`:

```gala
package util

func Void() = Println("cleanup done")
```

`main.gala`:

<!-- doc-check: fragment -->
```gala
package main

import . "example.com/demo/util"

func main() {
    Void()
}
```

**Error output.**

```
error[GALA-E0087]: "Void" is ambiguous: it is a type of the implicitly imported package std and a function of the dot-imported package util
  --> main.gala:6:5
  |
6 |     Void()
  |     ^^^^ qualify the name to pick one
  |
  = hint: qualify the name to pick one; for the function, drop the dot from the import of util and write `util.Void`; for the type, add `import "martianoff/gala/std"` and write `std.Void`
```

**Fix.** Qualify the name, so it says which one you mean.

For the dot-imported function, import its package without the dot and call it
through the package name:

```gala
package main

import "example.com/demo/util"

func main() {
    util.Void()
}
```

For std's type, import std by name and qualify it. The implicit import stays;
the named one only gives the qualifier:

```gala
package main

import "martianoff/gala/std"

func main() {
    Println(std.NoSuchElementError("empty").Error())
}
```

**Type positions are not affected.** In `func close() Try[Void]` or a
parameter `e NoSuchElementError`, only std's name is a type, so it is std's
type there, dot import or not. The same holds for a type argument
(`None[Void]()`) and for the extractor of a constructor pattern
(`case NoSuchElementError(m) =>`), where no function can stand.

**Rationale.** A package's own declarations outrank every import, the implicit
std import included, so `func Void()` declared in your package is what a bare
`Void()` calls. Between two imports there is no such order. A dot import and
the implicit std import both bring names in unqualified, and picking either
one silently changes what working code means when a dependency adds or renames
a function. GALA reports two dot imports that export one name for the same
reason ([GALA-E0032](/docs/errors/gala-e0032/)).

**Related.** [GALA-E0032](/docs/errors/gala-e0032/) covers one name exported by two
dot-imported packages. [GALA-E0066](/docs/errors/gala-e0066/) covers a declaration of
your package named like a dot-imported name.
