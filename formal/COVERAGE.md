# Coverage of the Lean semantics

Written by hand for now. Once the reference interpreter and the differential harness
exist, CI generates this file (see [docs/FORMAL_SEMANTICS.MD](../docs/FORMAL_SEMANTICS.MD#coverage)).

The tiers mean exactly this:

- **Modelled:** the construct is in a Lean model.
- **Tested against the spec:** generated programs and examples using it agree with the
  spec. Nothing is in this tier yet.
- **Proved:** a kernel-checked theorem, with no `sorry` and only Lean's standard axioms,
  covers it.

## What is modelled

| Construct | Modelled | Tested against the spec | Proved | Where |
|---|---|---|---|---|
| Sealed-type constructors (tag plus fields) | yes, simplified | no | yes, see below | `Gala/MatchLowering.lean` |
| `match` on a sealed value: variant patterns with variable binders, arms tried in order, no match ↦ panic | yes, simplified | no | yes, see below | `Gala/MatchLowering.lean` |
| Integer literals, `+`, unary `-`, variables | yes, as the expressions around constructors and `match` | no | yes, see below | `Gala/MatchLowering.lean` |

The model is simplified: there are no types, and patterns are a variant tag with variable
binders only. Nested, literal, wildcard, tuple and sequence patterns, guards, extractors
and views are not modelled.

## What is proved

The proof check (`scripts/ProofCheck.lean`) fails if a theorem listed here is missing;
keep its `requiredTheorems` in step with this table.

| Theorem | Statement |
|---|---|
| `Gala.MatchLowering.compile_correct`, `program_correct` | The scoped match lowering preserves meaning, for values and errors. "No arm matched" becomes a Go panic. |
| `Gala.MatchLowering.leaky_not_correct` | A lowering that declares an arm's pattern variables before its `if` (issue [#996](https://github.com/martianoff/gala/issues/996)) does not preserve meaning. The counterexample evaluates to 3 where GALA says 10. |

These are theorems about a *model* of the lowering, not about the transpiler. They say
what a correct lowering must do; they do not say the transpiler does it.

## What is not covered

Everything else in GALA. There is no reference interpreter yet, no Core JSON exporter and
no differential testing, so no program has been tested against the spec, and no example
has a verdict.

<!-- difftest:begin -->
## Differential testing

No differential run yet. The harness (`internal/formal/difftest`) generates this section
from a run against galaspec; the first such run fills it in.
<!-- difftest:end -->
