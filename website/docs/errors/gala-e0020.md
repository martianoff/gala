---
layout: default
title: "GALA-E0020 — Package Not Found"
description: "GALA-E0020 means the analyzer could not locate an imported GALA package on any search path, or no module in gala.mod provides an imported Go package. See the compiler messages and how to fix each cause."
keywords: "gala-e0020, package not found, gala import error, gala.mod require replace, gala search path, gala module resolution"
permalink: /docs/errors/gala-e0020/
last_modified_at: 2026-11-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0020</p>

# GALA-E0020 — Package not found

**What it means.** The analyzer was asked to resolve a GALA package by import path or relative path, but no matching directory exists on any configured search path. It fires both for explicit `import` statements and for the directory-walk passes that follow them. `gala build`, `gala run` and `gala test` also report it for an imported Go package that no module in `gala.mod` provides.

---

## Compiler message

Given `import . "martianoff/gala/doesnotexist"` on line 3:

```
Warning: failed to analyze package doesnotexist (imported at line 3): [SemanticError GALA-E0020] package not found: doesnotexist (hint: check that the directory exists on a search path; for cross-module imports verify gala.mod has a `require` (and `replace` if local) for the module)
error: cannot transpile: 1 imported package(s) could not be resolved:
  - failed to analyze package doesnotexist (imported at line 3): [SemanticError GALA-E0020] package not found: doesnotexist (hint: check that the directory exists on a search path; for cross-module imports verify gala.mod has a `require` (and `replace` if local) for the module)
Hint: ensure all GALA dependencies are available via --search paths or gala.mod
  --> e0020.gala:1:1
  |
1 | package main
  | ^^^^^^^
  |
```

This one reads differently from every other code. Package resolution fails before the file is compiled, so GALA-E0020 is reported *inside* an unresolved-imports wrapper rather than under its own `error[GALA-E0020]:` header, and the frame points at the package clause rather than the import. The code and message are still there to grep for.

---

## How to fix it

**1. Typo or case mismatch in the import path.** GALA preserves case from the filesystem, so `github.com/example/foo` will not resolve a directory named `Foo`.

**2. Missing search path.** Running outside Bazel, the standalone CLI needs `--search` pointed at the workspace's module root. The Bazel rules pass this automatically.

**3. Cross-module dependency without a `replace`.** `gala.mod` has `require github.com/example/foo v0.1.0` but no `replace` pointing at a local checkout. Add the replace directive, or work in a workspace where the module has been fetched.

---

## A Go module missing from gala.mod

A file imports a Go package from outside the standard library, and no module the build requires provides it: not the project's own module, not a GALA module it requires, and not a Go module (`// go`) required in `gala.mod` or by one of those modules. GALA reads a Go package's types from its module's source, so without the module it knows none of them, and the code calling into the package could not be lowered.

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

Require the module, with the version you want:

```
gala mod add github.com/spf13/cobra@v1.10.2 --go
```

or let the Go toolchain pick the latest version, as `go get` does:

```
gala mod add github.com/spf13/cobra@latest --go
```

`@latest` also works for a module with no tagged version: it resolves to the pseudo-version of the module's latest commit (`v0.0.0-20220603013715-3360bf22bea4`), and that is the version `gala.mod` records.

For a package in a subdirectory of its module (`github.com/spf13/cobra/doc`), require the module (`github.com/spf13/cobra`). The modules a required Go module requires itself (cobra requires `github.com/spf13/pflag`) need no line of their own. Under Bazel, Go dependencies are the `deps` of the GALA target, and this check does not run.

---

## Scope

This covers the analyzer's package resolution only. Go-level import errors that surface from `go build` after transpilation still appear as Go compiler messages — by then the GALA layer has finished its work.

---

## Related

- [Dependency Management](/docs/dependency-management/) — `gala.mod`, `require`, and `replace`
- [GALA-E0025](/docs/errors/gala-e0025/) — the package resolves, but this file never imported it
- [GALA-E0010](/docs/errors/gala-e0010/) — sibling files disagree on the package name
- [All GALA error codes](/docs/errors/)
