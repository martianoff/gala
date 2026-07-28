---
layout: default
title: "GALA-E0010 — Sibling .gala Files Declare Different Package Names"
description: "GALA-E0010 fires when two .gala files in the same directory declare different packages. See the triggering layout, the compiler message, and the two ways to resolve it."
keywords: "gala-e0010, duplicate package name, gala package declaration, sibling files different package names, gala package error"
permalink: /docs/errors/gala-e0010/
last_modified_at: 2026-07-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0010</p>

# GALA-E0010 — Sibling files declare different package names

**What it means.** Two or more `.gala` files in the same directory declare different `package` names. Test files (`_test.gala`) may diverge, following Go's `pkg_test` convention; regular sources may not.

---

## Layout that triggers it

```
lib/a.gala   // package mylib
lib/b.gala   // package other    <- triggers the error
```

---

## Compiler message

```
error[GALA-E0010]: directory lib has files with different package names: "other" and "mylib"
  --> lib/b.gala:1:1
  |
1 | package other
  | ^^^^^^^ use the same package name across all sibling .gala files, or…
  |
  = hint: use the same package name across all sibling .gala files, or move the file to a different directory
```

The header names the directory and both package names it found; the frame points at the file being compiled.

---

## How to fix it

Either rename one package so both files agree:

```gala
// lib/b.gala
package mylib
```

…or move the outlier into its own directory, where it can keep its own package name.

---

## Why the rule exists

GALA's sibling-file resolution treats every `.gala` file in a directory as part of one compilation unit, so conflicting package names break cross-file type resolution. Catching the mismatch in the analyzer is much cheaper than letting it surface as a cascade of "unresolved identifier" errors during transform.

---

## Related

- [GALA-E0020](/docs/errors/gala-e0020/) — package not found
- [GALA-E0025](/docs/errors/gala-e0025/) — cross-package symbol used without an import
- [Dependency Management](/docs/dependency-management/) — modules, `gala.mod`, and package layout
- [All GALA error codes](/docs/errors/)
