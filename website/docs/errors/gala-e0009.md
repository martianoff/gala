---
layout: default
title: "GALA-E0009 — Unrecognized Pattern Syntax (Transpiler Bug)"
description: "GALA-E0009 means the transpiler reached a pattern AST node it does not handle. It signals a compiler bug, not a mistake in your code — here is what to include when you report it."
keywords: "gala-e0009, unrecognized pattern syntax, gala transpiler bug, gala internal error, gala pattern transformer"
permalink: /docs/errors/gala-e0009/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0009</p>

# GALA-E0009 — Unrecognized pattern syntax

**What it means.** The transformer reached a pattern AST node it does not recognize. This is a defensive check: the grammar is supposed to prevent any pattern shape the transformer cannot handle from parsing in the first place. Seeing this code means either a new grammar rule for patterns was added without matching transformer support, or the grammar accepts a shape that has never been supported.

**This is a transpiler bug, not an error in your program.**

---

## Compiler message

The header reads `error[GALA-E0009]: unrecognized pattern syntax (internal type *grammar.…Context)`, followed by a framed snippet of the offending pattern and a hint asking you to report it.

No triggering program is quoted here on purpose: this code fires only when the grammar and the transformer disagree, which no well-formed source can force. The `internal type` in the message names the grammar context the transformer could not handle — copy it verbatim into your report.

---

## What to do

[File an issue](https://github.com/martianoff/gala/issues) with:

- the smallest source file that reproduces it,
- the exact `internal type *grammar.…Context` string from the message,
- your GALA version (`gala version`).

As a temporary workaround, rewrite the pattern in a simpler form — a plain binding plus a guard, or a nested `match` — and the transpiler will take a supported path.

---

## Why the code exists

This site previously produced an untracked error with no source span, so users could not tell "transpiler bug" from "my syntax is wrong". The coded error makes the bug class explicit and greppable, so issues can be filed against a stable tag.

---

## Related

- [GALA-E0017](/docs/errors/gala-e0017/) — internal transpiler panic
- [GALA-E0024](/docs/errors/gala-e0024/) — internal inference failure
- [Pattern Matching](/features/pattern-matching/) — the supported pattern shapes
- [All GALA error codes](/docs/errors/)
