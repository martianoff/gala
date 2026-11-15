---
layout: default
title: "GALA-E0120 — Unused Import"
description: "GALA-E0120 fires when a file imports a package and nothing in the file refers to it. Remove the import, or write import _ \"path\" to run only its initialization."
keywords: "gala-e0120, gala unused import, gala imported and not used, gala blank import, gala import underscore, gala package initialization"
permalink: /docs/errors/gala-e0120/
last_modified_at: 2026-11-15
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0120</p>

# GALA-E0120 — Unused import

**When it fires.** A file imports a package and nothing in the file refers to
it: no name qualified by the import (`strings.ToUpper`, `strings.Builder` in a
type, `lib.Shape(...)` in a pattern), and, for a dot import, none of the
package's names. An aliased import is unused when its alias is never written.
Use is read from the source alone: a method called on a value whose type
comes from the package does not use the import, and neither does code GALA
generates, which gets the imports it needs by itself. The imports GALA adds
itself, such as `std`, are never reported. Dependencies are not checked yet:
only the packages of the module being built are, and a dependency's unused
import is dropped from the generated Go as before.

**Minimal repro.**

```gala
package main

import "strings"

func main() {
    Println("done")
}
```

**Error output.**

```
error[GALA-E0120]: unused import "strings": the file never refers to strings
  --> main.gala:3:8
  |
3 | import "strings"
  |        ^^^^^^^^^ remove the import
  |
  = hint: remove the import; to run only its initialization, write `import _ "strings"`
```

**Fix.** Remove the import:

```gala
package main

func main() {
    Println("done")
}
```

To import a package only for its initialization — the package-level `val`s it
declares, whose initializers may register a handler or a codec, say — write a
blank import. It is never reported:

```gala
import _ "example.com/app/plugins"
```

**Rationale.** Go rejects an import that nothing uses ("imported and not
used"). GALA used to drop such an import from the generated Go without a word,
so the package's initialization did not run either, and nothing said so. An
import now either is used, or says with `_` that it is there for its
initialization.

---

## Related

- [Unused and blank imports](/docs/language-reference/#unused-and-blank-imports)
- [All GALA error codes](/docs/errors/)
