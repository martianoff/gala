---
layout: default
title: "GALA-E0024 — Internal Inference Failure"
description: "GALA-E0024 means the type inference engine met an expression node it does not handle. It signals a compiler bug — here is how to narrow it down and report it."
keywords: "gala-e0024, unknown expression type, gala internal inference failure, gala compiler bug, gala inference engine error"
permalink: /docs/errors/gala-e0024/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0024</p>

# GALA-E0024 — Internal inference failure

**What it means.** The inference engine was asked to type an expression node it does not know how to handle. Well-formed GALA source should always reach a recognized branch.

**This is a transpiler bug, not an error in your program.**

---

## Compiler message

The header reads `error[GALA-E0024]: unknown expression type: *infer.…`, with a hint asking you to file an issue.

No triggering program is quoted here: the code exists to catch a missing case inside the inference engine, so there is no stable repro to show. The node type named in the message *is* the missing case — include it verbatim in your report.

---

## What to do

Simplify the surrounding expression and re-run. If a simpler form transpiles cleanly, the shape of the original is the trigger — that reduced snippet is exactly what to attach to [an issue](https://github.com/martianoff/gala/issues).

---

## Why the code exists

This mirrors [GALA-E0017](/docs/errors/gala-e0017/) (internal transformer panic) for the inference layer. A coded failure gives maintainers a stable search target and users one clear instruction about how to file the bug.

---

## Related

- [GALA-E0017](/docs/errors/gala-e0017/) — internal transpiler panic
- [GALA-E0009](/docs/errors/gala-e0009/) — unrecognized pattern syntax
- [All GALA error codes](/docs/errors/)
