---
layout: default
title: "GALA-E0017 — Internal Transpiler Panic"
description: "GALA-E0017 means a panic inside the GALA transformer was caught and surfaced as a coded error. It signals a compiler bug — here is how to narrow it down and what to include in a report."
keywords: "gala-e0017, internal transpiler panic, gala compiler bug, gala transformer panic, gala internal error"
permalink: /docs/errors/gala-e0017/
last_modified_at: 2026-09-25
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0017</p>

# GALA-E0017 — Internal transpiler panic

**What it means.** A `panic` inside the transformer was caught by the top-level recover and surfaced as a coded error. Well-formed GALA source should never trigger this.

**This is a transpiler bug, not an error in your program.**

---

## Compiler message

The header reads `error[GALA-E0017]: internal transpiler panic: …`, with the recovered panic message appended verbatim and a hint asking you to file an issue.

No triggering program is quoted here: by definition this code only appears when the transpiler hits a case it does not handle, so there is no stable repro to show.

**The line it points at is usually innocent.** The error carries the last source location the transformer tracked, and the CLI draws a caret under it exactly as it does for a real error in your code. That position is not a diagnosis. When the panic is raised on one of the analyzer's concurrent parse workers it is simply whichever file and line that worker happened to hold, and it can differ on the next run of byte-identical input. One report of about a dozen such aborts in a single day named five unrelated lines — a generic call, a range loop, a plain call, a named-argument list — sharing no construct at all, and every one transpiled cleanly on retry with no edit. Copy the whole message into your report and do not assume the quoted line is involved.

---

## What to do

1. **Re-run the same command first, before changing anything.** These panics can be nondeterministic. If it succeeds on retry with no edit, the line in the message was innocent and there is nothing in your source to find — it is still a transpiler bug worth reporting, but you have been spared the search.
2. **If it reproduces on every run, narrow it down.** Simplify the surrounding expression and re-run. When the failure is deterministic, a simplified form that transpiles cleanly does identify the shape that triggers it. (This step is worthless for the nondeterministic case, which is why it comes second.)
3. **Report it.** [File an issue](https://github.com/martianoff/gala/issues) with the smallest source that reproduces it, the full error message, your GALA version, and whether it reproduces every time or only sometimes — that last detail tells a maintainer which of the two failures they are looking at.

There is no user-side fix until the transpiler bug is fixed.

---

## Why the code exists

Before this code, an unguarded panic surfaced as a raw Go stack trace from the CLI — unactionable for users and easy for maintainers to overlook, because nothing referenced it. Wrapping at the recover seam gives both groups one search target.

---

## Related

- [GALA-E0009](/docs/errors/gala-e0009/) — unrecognized pattern syntax
- [GALA-E0024](/docs/errors/gala-e0024/) — internal inference failure
- [All GALA error codes](/docs/errors/)
