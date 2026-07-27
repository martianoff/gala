# GALA-E0020 — Package not found

**When it fires.** The analyzer was asked to resolve a GALA package by its
import path, but no matching directory exists on any configured search path.
Triggered both by explicit `import` statements and by the directory-walk passes
that follow them.

**E0020 has no framed `error[GALA-E0020]:` form of its own.** It is raised
without a source position (line 0, column 0) deep in the analyzer, formatted
into a warning string, and collected. Transformation then fails with a separate,
**uncoded** unresolved-imports error, framed at the file's `package` clause,
with the E0020 text nested inside it in the terse single-line form. So the code
you grep for appears in the body of a larger diagnostic rather than in its
header.

**Minimal repro.**

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

**Scope.** Only the analyzer's package-resolution failures, not Go-level import
errors that surface from `go build` after transpilation. Those still appear as
Go compiler messages, since the GALA layer has already finished its work.
