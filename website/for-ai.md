---
layout: default
title: "GALA for AI — a stricter compiler is a tighter agent loop"
description: "Why GALA suits AI coding agents: a strict, expressive compiler turns bugs into an instant, precise, deterministic worklist — the exact feedback an agent's generate-check-fix loop converges on."
keywords: "gala for ai, ai coding agents, llm code generation, compiler feedback loop, exhaustive pattern matching ai, sealed types refactoring, ai code correctness, agent friendly language, static types llm, go for ai agents"
permalink: /for-ai/
last_modified_at: 2026-07-10
---

<div class="breadcrumb">
  <a href="{{ '/' | relative_url }}">Home</a> &raquo; GALA for AI
</div>

*Scala on Go — and the feedback loop an AI coding agent actually wants.*

# GALA for AI — the compiler is the agent's feedback loop

A coding agent is only as fast as its feedback signal. It works in discrete
generate → check → fix turns, and the quality of "check" decides how many turns
it burns to reach correct code. GALA's compiler turns whole classes of bugs into
an **instant, precise, deterministic** error at the exact edit site — no test
harness, no lucky code-path coverage, no runtime surprise.

This is not a claim that GALA writes better code. It's a claim about **loop
economics**: *where* the error surfaces, *how precise* it is, and whether the
agent has to **run** the program to discover it.

<img src="{{ '/assets/images/gala-for-ai.png' | relative_url }}" alt="GALA for AI — the compiler is the agent's feedback loop: a loosely-checked language forces a slow runtime-discovery loop, while GALA fails fast at compile time with an exact error." style="max-width:100%;border:1px solid #e1e4e8;border-radius:10px;margin:1.5rem 0;">

---

## Why this matters for agents specifically

Human developers tolerate a loose feedback loop because they hold context in
their heads. An agent doesn't — every turn that requires *running* code costs
more than a turn that only requires *compiling* it.

| | Loosely-checked language | GALA (compile-time fast-fail) |
|---|---|---|
| Where the bug shows | runtime, only on an exercised path | build time, always |
| To even reach it | write and run a test harness | just `gala build` |
| Precision | stack trace at the symptom | `file:line` at the root edit + what's missing |
| Determinism | flaky / path-dependent | same input → same error |
| Escapes to prod | yes, if untested | no — it will not compile |

Fewer turns per fix, and no "did my test actually cover that path?" gamble. The
compiler is a free, exhaustive reviewer the agent can call on every turn.

---

## The demo: extend a closed set

The single most common refactor an agent performs is **extending a closed set** —
"add a `Crypto` payment method." Here a sealed type is matched on in three places.
The agent adds the variant and forgets the match sites — the realistic mistake.

<div class="comparison">
<div>
<p><strong>GALA — compiler hands you a worklist</strong></p>
<pre><code>sealed type Payment {
    case Card(Last4 string)
    case Cash()
    case Bank(Iban string)
    case Crypto(Chain string)  // agent added
}

// three match sites now incomplete
func label(p Payment) string = ...
func fee(p Payment) float64  = ...
func requiresName(p Payment) bool = ...</code></pre>
</div>
<div>
<p><strong>Go — compiles clean, ships the bug</strong></p>
<pre><code>type Payment interface{ isPayment() }
type Card struct{ Last4 string }
type Cash struct{}
type Bank struct{ Iban string }
type Crypto struct{ Chain string } // added

func label(p Payment) string {
    switch v := p.(type) {
    case Card: return "card ****" + v.Last4
    case Cash: return "cash"
    case Bank: return "bank " + v.Iban
    }
    return "" // Crypto silently lands here
}</code></pre>
</div>
</div>

Run each build. GALA fails, and names the exact site and the missing variant —
one precise task per build, until green:

```text
$ gala run main.gala
[GALA-E0002] line 13:4 non-exhaustive match: missing cases: Crypto
  (hint: add the missing variant cases, or add a `case _ => ...` default)

# fix label(), rebuild → line 20:4 (fee)
# fix fee(),   rebuild → line 27:4 (requiresName)
# fix all three         → compiles, runs
```

Go compiles the same mistake with **no error and no warning**, and
`label(Crypto{"eth"})` returns `""` at runtime — forever, unless a test happens
to pass a `Crypto` value through `label` and assert the result. The agent would
have to predict the exact bug in advance. Dynamically-typed languages are worse:
the mistake hides until that line executes in production.

*(The transcript above is real output from GALA — the compiler stops at the first
incomplete site, so each build is a deterministic step toward green.)*

---

## Which features feed the loop

Exhaustiveness is the flagship, but the same runtime → compile-time shift runs
through the language. Each one is another class of bug the agent is *told about*
for free, instead of having to discover by running code:

- **Sealed types + exhaustive `match`** — extend a closed set, get a complete
  worklist of every site to update.
- **`Option[T]` / `Try[T]` instead of `nil` / `(T, error)`** — "forgot the empty
  or error case" becomes a type error, not a nil panic three calls deep.
- **Immutability by default** — "accidentally mutated shared state" stops
  compiling instead of becoming a heisenbug the agent can't reproduce.
- **Type inference with no silent `any`** — an unresolved type is a hard error at
  the edit, so the agent can't paper over a mistake with `interface{}`.
- **Compile-safe regex and typed JSON `Codec[T]`** — malformed patterns and shape
  mismatches fail the build, not a request handler.

Every one converts a "write a test and hope" turn into a "read the compiler" turn.

---

## Honest limits

- The compiler proves **shape and totality, not logic.** It guarantees the agent
  *handled* the `Crypto` case; it can't know the fee should be `0.015`. Agents
  still need tests for behavior.
- This helps most on **refactors and structural change** — the work agents do
  constantly — and less on greenfield logic.
- GALA transpiles to Go, so runtime speed is unchanged. The win is entirely in the
  *authoring* loop, not execution.
- It's a real compile step. For throwaway one-liners, a dynamic REPL loop is still
  faster.

---

## Try it

- [Playground]({{ '/playground/' | relative_url }}) — run GALA in your browser, no install
- [GALA vs Go]({{ '/vs-go/' | relative_url }}) — the same comparison across sum types, `Option`, immutability, and errors
- [Getting Started]({{ '/getting-started/' | relative_url }}) — install and write your first program
- [Sealed Types]({{ '/features/sealed-types/' | relative_url }}) &middot; [Pattern Matching]({{ '/features/pattern-matching/' | relative_url }}) — the exhaustiveness that drives the loop
