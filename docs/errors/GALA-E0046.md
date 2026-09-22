# GALA-E0046 — package already imported

**When it fires.** A file imports the same package twice under the same local
name:

```gala
import (
    "strings"
)

import (
    "strings"
)
```

**Minimal repro.**

```gala
package main

import (
    "strings"
)

// A second block importing the SAME package.
import (
    "strings"
)

func main() {
    Println(strings.Repeat("-", 3))
}
```

**Error output.**

```
error[GALA-E0046]: package "strings" is already imported at line 4
  --> main.gala:9:5
  |
9 |     "strings"
  |     ^^^^^^^^^ remove this import — a file's import blocks are merged, so a…
  |
  = hint: remove this import — a file's import blocks are merged, so a package listed in one block is in scope for the whole file
```

**Fix.** Remove the second import. A file may have as many import blocks as it
likes — they are merged — so a package named in one is in scope for the whole
file.

**Rationale.** The rejection is not new; the *reporter* is. Import declarations
are concatenated on emit without deduping, so a repeated path reached `go build`
as two identical import lines and was reported against the generated file:

```
gen/main.gen.go:6:8: strings redeclared in this block
	gen/main.gen.go:5:8: other declaration of strings
gen/main.gen.go:6:8: "strings" imported and not used
```

Nothing there points at the source. The author is told about lines 5 and 6 of a
file they did not write, and the second message — "imported and not used" — is
actively misleading for a package used exactly as often as it was imported.

The case arises by editing, not by typing the same line twice: adding an import
block to the top of a file that already has one further down, past a comment
banner, which is easy to miss.

**Where it stands down.** The same path under two *different* local names stays
legal, as it is in Go, because it emits two distinct identifiers:

```gala
import "strings"
import gostr "strings"

val a = strings.Repeat("-", 3)
val b = gostr.Repeat("+", 2)
```

**Scope.** This code covers one file importing one package twice. A package that
cannot be found at all is [GALA-E0020](GALA-E0020.md); two *different*
dot-imported packages exporting the same name is
[GALA-E0032](GALA-E0032.md).
