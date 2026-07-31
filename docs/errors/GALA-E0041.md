# GALA-E0041 — Import of an internal package from outside its tree

**When it fires.** An `import` names a package that lives under a directory
called `internal`, and the importing package is not inside the tree that is
allowed to see it.

GALA uses Go's rule, applied to GALA import paths:

> `a/b/c/internal/d` is importable only from the tree rooted at `a/b/c`.

Only whole path **elements** count. `a/b/internalize/c` contains no `internal`
element and is an ordinary public package. When a path contains more than one
`internal` element, the innermost is the binding one, matching `cmd/go`:
`a/internal/b/internal/c` is importable from the tree rooted at
`a/internal/b`.

There is no exemption for the standard library. `martianoff/gala/*` resolves
through the same mechanism as any other GALA library and obeys the same rule.

**Minimal repro.** A module whose `sub/` subtree keeps a private helper:

```
myapp/
  gala.mod                      module example.com/myapp
  main.gala                     package main
  sub/
    internal/
      deep/deep.gala            package deep — private to example.com/myapp/sub
```

```gala
package main

import "example.com/myapp/sub/internal/deep"

func main() {
    Println(deep.Deep())
}
```

`main.gala` sits at `example.com/myapp`, which is *above* `example.com/myapp/sub`
rather than inside it, so the import is rejected.

**Error output.**

```
error[GALA-E0041]: package "example.com/myapp/sub/internal/deep" is internal to "example.com/myapp/sub" and cannot be imported from "example.com/myapp"
  --> main.gala:3:8
  |
3 | import "example.com/myapp/sub/internal/deep"
  |        ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ internal packages are private to their parent tree
  |
  = hint: internal packages are private to their parent tree; import something "example.com/myapp/sub" exports instead, or move this package out of internal/ to make it public
```

The same code covers a dependency's private packages:

```
error[GALA-E0041]: package "example.com/lib/internal/secret" is internal to "example.com/lib" and cannot be imported from "example.com/myapp"
```

**Fix.** Pick whichever matches your intent:

1. **Use the public API.** If the internal package is a dependency's, the
   library author deliberately did not publish it — call something the parent
   package exports instead.

2. **Move the importer into the tree.** A file that genuinely belongs to the
   `sub/` subsystem should live under `sub/`, at which point the import is
   legal.

3. **Publish the package.** Move it out of the `internal` directory. Note that
   this changes its import path, so it is a breaking change for anyone already
   importing it under the old path — which is the point of the mechanism.

**Rationale.** The rule itself was always enforced, but by the **Go compiler**,
against the **generated** code — long after the transpiler had already resolved
and transpiled the forbidden package. What the user saw was:

```
package gala-build-workspace/gen
        gen\main.gen.go:6:8: use of internal package gala-build-workspace/gen/sub/internal/deep not allowed
```

That message names a file nobody wrote (`gen\main.gen.go`) and an import path
that exists only inside the build workspace (`gala-build-workspace/gen/...`) —
neither of which appears anywhere in the user's source. Nothing in it points
back to the `import` line that caused it.

Checking at import-resolution time reports the violation at that `import` line,
in the `.gala` file, using the path the user actually typed.

**Why it takes two signals.** The check reports a violation only when the
import paths *and* the directory layout on disk both say so. Import paths alone
are not a faithful picture of the layout: under Bazel a `gala_library` declares
its `importpath` explicitly, and it need not mirror where the sources live.
This repository's own examples declare `martianoff/gala/greeting` for sources
under `examples/internal_package/greeting/` — judging by the derived import
path alone, that package importing its own `internal/format` subtree would look
like a violation. The layout shows it plainly is not.

The Go compiler remains the backstop. Whenever a violation cannot be
established — the importing package's import path is unknown (no module root,
or a file outside it, as happens for a cached dependency analyzed on the fly or
an editor buffer with no `gala.mod`), or the imported package cannot be
resolved to a directory — the check deliberately fails **open** rather than
guessing, and the compiler still rejects the generated code as before.

**See also.** [Package Visibility](../GALA.MD#package-visibility) in the
language reference, for the full visibility model and guidance on when an
`internal` tree is worth introducing.
