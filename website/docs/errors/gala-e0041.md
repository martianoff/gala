---
layout: default
title: "GALA-E0041 — Import of an Internal Package From Outside Its Tree"
description: "\"package X is internal to Y and cannot be imported from Z\" — GALA-E0041 enforces Go's internal/ package rule on GALA import paths. See the real compiler output, the visibility table, and how to publish an internal package."
keywords: "gala-e0041, gala internal package, gala private package, gala package visibility, gala internal directory, go internal rule, gala module private api"
permalink: /docs/errors/gala-e0041/
last_modified_at: 2026-07-30
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0041</p>

# GALA-E0041 — Import of an internal package from outside its tree

**What it means.** An `import` names a package that lives under a directory called `internal`, and the importing package is not inside the tree allowed to see it.

GALA uses Go's rule, applied to GALA import paths:

> `a/b/c/internal/d` is importable only from the tree rooted at `a/b/c`.

Only whole path **elements** count — `a/b/internalize/c` contains no `internal` element and is an ordinary public package. When a path contains more than one `internal` element, the innermost is the binding one, matching `cmd/go`: `a/internal/b/internal/c` is importable from the tree rooted at `a/internal/b`.

There is no exemption for the standard library. `martianoff/gala/*` resolves through the same mechanism as any other GALA library and obeys the same rule.

---

## Who can import what

For a module laid out like this:

```
mylib/
  gala.mod                     module example.com/mylib
  mylib.gala                   package mylib          — public
  internal/
    detail/detail.gala         package detail         — private to example.com/mylib
  sub/
    sub.gala                   package sub            — public
    internal/
      deep/deep.gala           package deep           — private to example.com/mylib/sub
```

| Importer | `.../mylib/internal/detail` | `.../mylib/sub/internal/deep` |
|---|---|---|
| `example.com/mylib` | ✅ | ❌ — not under `sub/` |
| `example.com/mylib/sub` | ✅ | ✅ |
| `example.com/mylib/sub/other` | ✅ | ✅ |
| another module | ❌ | ❌ |

---

## Minimal repro

```gala
package main

import "example.com/myapp/sub/internal/deep"

func main() {
    Println(deep.Deep())
}
```

`main.gala` sits at `example.com/myapp`, which is *above* `example.com/myapp/sub` rather than inside it.

## Error output

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

---

## How to fix it

1. **Use the public API.** If the internal package belongs to a dependency, the library author deliberately did not publish it — call something the parent package exports instead.

2. **Move the importer into the tree.** A file that genuinely belongs to the `sub/` subsystem should live under `sub/`, at which point the import is legal.

3. **Publish the package.** Move it out of the `internal` directory. This changes its import path, so it is a breaking change for anyone already importing it under the old path — which is exactly the guarantee the mechanism buys you.

---

## Why the rule exists

An `internal` tree lets a library share code across its own packages without adding it to the API it has to keep supporting. Consumers can call whatever the parent package exports; they cannot reach `internal/detail`, so it can be renamed, resplit or deleted without breaking them.

This is worth reaching for when you ship a library to consumers you do not control and want a small supported surface. For an application or a single-binary project, the whole module is already private to you, and an `internal` tree mostly adds a directory level — use it where there is a real API boundary to defend, not as a default layout.

## Why the check takes two signals

The transpiler reports a violation only when the import paths *and* the directory layout on disk both indicate one. Import paths are not always a faithful picture of the layout: under Bazel a `gala_library` declares its `importpath` explicitly, and it need not mirror where the sources live. Judging by the derived import path alone would reject a package importing its own `internal/` subtree in those builds.

Whenever a violation cannot be established — the importing package's import path is unknown (no module root, or a file outside it), or the imported package cannot be resolved to a directory — the check deliberately fails **open**. The Go compiler still rejects the generated code, so failing open costs a worse error message rather than a missed violation.

---

## See also

- [Package Visibility](/docs/language-reference/#package-visibility) — the full visibility model, including identifier-level casing rules
- [Dependency Management](/docs/dependency-management/) — what you can and cannot import from a dependency
- [GALA-E0020](/docs/errors/gala-e0020/) — package not found (the import path does not resolve at all)
- [GALA-E0025](/docs/errors/gala-e0025/) — unresolved cross-package symbol
