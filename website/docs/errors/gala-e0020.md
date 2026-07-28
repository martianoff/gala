---
layout: default
title: "GALA-E0020 — Package Not Found"
description: "GALA-E0020 means the analyzer could not locate an imported GALA package on any search path. See the compiler message and the three usual causes: a typo, a missing search path, or a missing gala.mod require/replace."
keywords: "gala-e0020, package not found, gala import error, gala.mod require replace, gala search path, gala module resolution"
permalink: /docs/errors/gala-e0020/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0020</p>

# GALA-E0020 — Package not found

**What it means.** The analyzer was asked to resolve a GALA package by import path or relative path, but no matching directory exists on any configured search path. It fires both for explicit `import` statements and for the directory-walk passes that follow them.

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

## Scope

This covers the analyzer's package resolution only. Go-level import errors that surface from `go build` after transpilation still appear as Go compiler messages — by then the GALA layer has finished its work.

---

## Related

- [Dependency Management](/docs/dependency-management/) — `gala.mod`, `require`, and `replace`
- [GALA-E0025](/docs/errors/gala-e0025/) — the package resolves, but this file never imported it
- [GALA-E0010](/docs/errors/gala-e0010/) — sibling files disagree on the package name
- [All GALA error codes](/docs/errors/)
