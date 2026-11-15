# GALA-E0020 — Package not found

**When it fires.** The analyzer was asked to resolve a GALA package by its
import path, but no matching directory exists on any configured search path.
Triggered both by explicit `import` statements and by the directory-walk passes
that follow them. `gala build`, `gala run` and `gala test` also report it for a
Go package that no module in `gala.mod` provides (see **A Go module missing
from gala.mod** below).

**A missing GALA package has no framed `error[GALA-E0020]:` form.** It is raised
without a source position (line 0, column 0) deep in the analyzer, formatted
into a warning string, and collected. Transformation then fails with a separate,
**uncoded** unresolved-imports error, framed at the file's `package` clause,
with the E0020 text nested inside it in the terse single-line form. So the code
you grep for appears in the body of a larger diagnostic rather than in its
header.

**Minimal repro.**

<!-- doc-check: fragment -->
```gala
package main

import "example.com/demo/doesnotexist"

func main() {
    Println("hi")
}
```

**Error output.** The full, real shape — two stderr warnings followed by the
framed wrapper:

```
Warning: failed to transpile dependency example.com/demo/doesnotexist: package not found: example.com/demo/doesnotexist
Warning: failed to analyze package example.com/demo/doesnotexist (imported at line 3): [SemanticError GALA-E0020] package not found: example.com/demo/doesnotexist (hint: check that the directory exists on a search path; for cross-module imports verify gala.mod has a `require` (and `replace` if local) for the module)
error: cannot transpile: 1 imported package(s) could not be resolved:
  - failed to analyze package example.com/demo/doesnotexist (imported at line 3): [SemanticError GALA-E0020] package not found: example.com/demo/doesnotexist (hint: check that the directory exists on a search path; for cross-module imports verify gala.mod has a `require` (and `replace` if local) for the module)
Hint: ensure all GALA dependencies are available via --search paths or gala.mod
  --> main.gala:1:1
  |
1 | package main
  | ^^^^^^^
  |
```

**Fix.** Find the offending import by the **line number in the `Warning:` line**
— not by the caret, which points at the `package` clause. Three common causes:

1. **Typo in the import path** — `import . "github.com/example/foo"` when the
   directory is `github.com/example/Foo` (case mismatch). GALA preserves case
   from the filesystem.
2. **Missing search path** — outside Bazel, the standalone CLI needs `--search`
   pointing at the workspace's module root. The Bazel rules pass this
   automatically.
3. **Cross-module dependency without `replace`** — `gala.mod` has
   `require github.com/example/foo v0.1.0` but no matching `replace` directive
   points at the local checkout. Add a `replace`, or run inside a workspace
   where the module has been fetched.

**Reading the output.**

- The `Warning:` lines go to **stderr as the analyzer walks imports**. The first
  reports that the dependency could not be transpiled; the second carries the
  GALA-E0020 code and names the line of the offending `import`.
- The `error: cannot transpile:` block is the failure that stops the build. It
  lists one bullet per unresolved import, each repeating the nested E0020 text.
- The caret frames the `package` clause at line 1. The wrapper has no better
  position: it is raised after the whole file has been analyzed, so it points at
  the compilation unit as a whole.

Analysis continues past the first missing package, so a file with several bad
imports reports all of them in one run — the count in the header tells you how
many.

**Rationale.** Before this code existed the failure surfaced as a generic
`package not found: <path>` with no stable identifier for tools or CI to grep.
The code lets build tooling distinguish "the user's import is wrong" from other
infrastructure failures.

The terse nested form is deliberate rather than a rendering gap: E0020 is raised
with no usable source position — the analyzer knows the import *path* that
failed, not a span in the file being compiled — so there is nothing to frame.
Wrapping many per-import failures into one diagnostic also keeps a file with
five bad imports to a single error instead of five disconnected ones.

**A Go module missing from gala.mod.** A file imports a Go package from outside
the standard library, and no module
the build requires provides it: not the project's own module, not a GALA
module it requires, and not a Go module (`// go`) required in `gala.mod` or by
one of those modules. GALA reads a Go package's types from its module's
source, so without the module it knows none of them, and the code calling into
the package could not be lowered.

```gala
package main

import "github.com/spf13/cobra"

func main() {
    val cmd = &cobra.Command(Use = "app")
    Println(cmd.Use)
}
```

```
error[GALA-E0020]: package not found: no module required in gala.mod provides Go package "github.com/spf13/cobra"
  --> main.gala:3:8
  |
3 | import "github.com/spf13/cobra"
  |        ^^^^^^^^^^^^^^^^^^^^^^^^ require its module in gala.mod
  |
  = hint: require its module in gala.mod; run `gala mod add github.com/spf13/cobra@<version> --go` (for a package in a subdirectory of its module, name the module's path)
```

**Fix.** Require the module, with the version you want:

```
gala mod add github.com/spf13/cobra@v1.10.2 --go
```

or let the Go toolchain pick the latest version, as `go get` does:

```
gala mod add github.com/spf13/cobra@latest --go
```

`@latest` also works for a module with no tagged version: it resolves to the
pseudo-version of the module's latest commit
(`v0.0.0-20220603013715-3360bf22bea4`), and that is the version `gala.mod`
records. `gala.mod` needs a concrete version, a tag or a pseudo-version, so
write `latest` on the command line, not in `gala.mod`.

For a package in a subdirectory of its module (`github.com/spf13/cobra/doc`),
require the module (`github.com/spf13/cobra`). The modules a required Go
module requires itself (cobra requires `github.com/spf13/pflag`) need no line
of their own. Under Bazel, Go dependencies are the `deps` of the GALA target,
and this check does not run.

**Scope.** Only the analyzer's package-resolution failures, not Go-level import
errors that surface from `go build` after transpilation. Those still appear as
Go compiler messages, since the GALA layer has already finished its work.
