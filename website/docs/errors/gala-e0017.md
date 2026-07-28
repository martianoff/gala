---
layout: default
title: "GALA-E0017 — Internal Transpiler Panic"
description: "GALA-E0017 means a panic inside the GALA transformer was caught and surfaced as a coded error. It signals a compiler bug — here is how to narrow it down and what to include in a report."
keywords: "gala-e0017, internal transpiler panic, gala compiler bug, gala transformer panic, gala internal error"
permalink: /docs/errors/gala-e0017/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0017</p>

# GALA-E0017 — Internal transpiler panic

**What it means.** A `panic` inside the transformer was caught by the top-level recover and surfaced as a coded error. Well-formed GALA source should never trigger this.

**This is a transpiler bug, not an error in your program.**

---

## Compiler message

The header reads `error[GALA-E0017]: internal transpiler panic: …`, with the recovered panic message appended verbatim and a hint asking you to file an issue.

No triggering program is quoted here: by definition this code only appears when the transpiler hits a case it does not handle, so there is no stable repro to show. The error is positioned at the last source location the transformer tracked, which may or may not be near the real cause — copy the whole message into your report.

---

## What to do

1. **Narrow it down.** Simplify the surrounding expression and re-run. If the simplified form transpiles cleanly, the shape of the original expression is the trigger.
2. **Report it.** [File an issue](https://github.com/martianoff/gala/issues) with the smallest source that reproduces it, the full error line, and your GALA version.

There is no user-side fix beyond restructuring the offending expression until the transpiler bug is fixed.

---

## Why the code exists

Before this code, an unguarded panic surfaced as a raw Go stack trace from the CLI — unactionable for users and easy for maintainers to overlook, because nothing referenced it. Wrapping at the recover seam gives both groups one search target.

---

## Related

- [GALA-E0009](/docs/errors/gala-e0009/) — unrecognized pattern syntax
- [GALA-E0024](/docs/errors/gala-e0024/) — internal inference failure
- [All GALA error codes](/docs/errors/)
