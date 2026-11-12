# Coverage of the Lean semantics

Written by hand, except the "Differential testing" section at the end, which a
differential run generates (see [docs/FORMAL_SEMANTICS.MD](../docs/FORMAL_SEMANTICS.MD#coverage)).

The tiers mean exactly this:

- **Modelled:** the construct is in a Lean model. For the reference interpreter
  (`galaspec`), programs using it run.
- **Tested against the spec:** generated programs and examples using it agree with the
  spec; the "Differential testing" section lists them. The fixture tests below are
  regression tests of the interpreter, not differential results.
- **Proved:** a kernel-checked theorem, with no `sorry` and only Lean's standard axioms,
  covers it.

## The reference interpreter: subset S1 and part of S2

`galaspec` (`Gala/Syntax.lean`, `Json.lean`, `Scope.lean`, `Eval.lean`, `Print.lean`,
`Cli.lean`) models subset S1 of the design, and the parts of S2 listed after it.

| Construct | Modelled | Tested against the spec | Proved |
|---|---|---|---|
| `int` literals (decimal, `0x`, `0b`, `0o`) and `+ - * / % << >> & \| ^ &^`, unary `- + ^`: Go's int64, wrapping; `/` and `%` truncate; division by zero is `panic divide`; a negative shift count is `panic other` | yes | no | no |
| `bool` literals, `! && \|\|` (short-circuit), comparisons | yes | no | no |
| `string` literals, `+`, comparisons (byte order) | yes | no | no |
| `==`/`!=` on structs, sealed values and tuples, by value | yes | no | no |
| `val`/`var` (several names, tuple destructuring, zero values), `=`, `+= -= *= /=`, `++`/`--`, assignment to `var` struct fields | yes | no | no |
| Package-level `val`/`var`, initialized in Go's dependency order | yes | no | no |
| Blocks and their scopes, shadowing | yes | no | lookup, update and scope exit only (see below) |
| `if` statement and expression, `return`, trailing-expression results | yes | no | no |
| Top-level functions and recursion | yes | no | no |
| Structs (block and shorthand), construction, field access | yes | no | no |
| Sealed types, construction, default case as zero value | yes | no | no |
| `match`: wildcard, binding, literal, tuple, nested variant and struct extractor patterns, stable identifiers, guards; arms in order, subject evaluated once, binders scoped to their arm, first failing sub-pattern stops the rest, no arm ↦ `panic match` | yes | no | no |
| `Println`/`Print` formatting of every S1 value | yes | no | no |
| Strict left-to-right evaluation of operands, arguments and tuple elements | yes | no | no |
| S2: interpolated strings `s"..."`: parts left to right, each embedded value printed as `Println` prints it; `$$` is `$`; a `%...` after an embedded expression is literal text | yes | no | no |
| Determinism of every run (T1) | — | — | yes |

Untyped constant expressions are not folded: Go rejects `9223372036854775807 + 1` at
compile time, while galaspec wraps it at run time. Only programs Go rejects differ.

`int64` is accepted as a type and means the same as `int`. Every other numeric type is
outside S1 and reported as unsupported.

### Printing follows master and will change

`Println` of a plain struct prints today what Go's `fmt` prints for the struct GALA
generates: `{1 2}` for `Pt(1, 2)`, `{1}` for a lowercase `val` field (its
`Immutable` wrapper, whose `Format` method `fmt` cannot call through an unexported field),
and for a sealed value reached through such a field, its Go layout. `Gala/Print.lean`
models exactly that. A pending change prints plain structs as `Pt(1, 2)`; when it lands,
`Gala/Print.lean` and the `printing` fixture must change with it.

### Where galaspec stops

A run ends `unsupported:<kind>` when it reaches:

- an `unsupported` node of Core JSON: the kind is the exporter's name (`lambda`,
  `import`, …);
- a name that resolves to nothing: the kind of the first top-level declaration outside
  S1 if there is one (most often `import`), else `unresolved-name` (Go builtins such as
  `println` or `len`, std names such as `Some`);
- a method call or method value: `method-call`, `method-value` (the generated `Copy` and
  `Equal` included);
- a local `func` or `struct` declaration (`local-func`, `local-struct`); a function or
  constructor used as a value (`func-value`); a type outside S1 (`type-<name>`); an
  integer constant outside int64 (`int-constant-overflow`); a cycle in package-level
  initialization (`init-cycle`); a program without `main` (`no-main`);
- printing a struct or sealed value in a program that declares methods (a `String`
  method would change the text): `method`; a layout `fmt` would print as a pointer
  address: `print-pointer`;
- anything a program GALA accepts cannot do (a type error, a wrong arity): `stuck`.

### What is tested

`lake exe galaspec-tests` runs the fixtures in [`tests/`](tests):

- 12 programs in `tests/src/` (integer arithmetic, printing, `match`, evaluation order,
  interpolated strings,
  package-level initialization and its order, scoping, three panic classes, fuel), and a
  document with
  an unknown `core` version, which is malformed.
  Each `.out` is what the program prints when built and run with the `gala` CLI; for
  `panic_match`, which GALA rejects as not exhaustive, it is the spec's own answer; so it is for the
  last line of `interp`, which the transpiler prints wrongly ([#1042](https://github.com/martianoff/gala/issues/1042),
  [#1043](https://github.com/martianoff/gala/issues/1043)).
- 14 S1 examples from `examples/`: 8 run to the end and print the example's `.out`, 2 end
  in fuel after a prefix of it, 4 are unsupported (`println`, `Some`, `Equal`).

Plus `#guard` checks of integer wrapping, literal parsing, print spacing, the decoder's
version check and the command line, run while the tests compile.

## What is proved

The proof check (`scripts/ProofCheck.lean`) fails if a theorem listed here is missing;
keep its `requiredTheorems` in step with this table.

| Theorem | Statement |
|---|---|
| `Gala.MatchLowering.compile_correct`, `program_correct` | The scoped match lowering preserves meaning, for values and errors. "No arm matched" becomes a Go panic. |
| `Gala.MatchLowering.leaky_not_correct` | A lowering that declares an arm's pattern variables before its `if` (issue [#996](https://github.com/martianoff/gala/issues/996)) does not preserve meaning. The counterexample evaluates to 3 where GALA says 10. |
| `Gala.Spec.run_deterministic`, `eval_deterministic` | **T1.** A program and a fuel budget have one output and one outcome; an evaluation from a given state has one result and final state. The semantics is a total Lean function, so this holds by construction and the proof is one line; the theorem records it, it does not add a check. |
| `Gala.Spec.lookup_rename`, `update_rename`, `exitScope_rename` | **T2, for name resolution only.** Under an injective renaming of an environment, a renamed name resolves to the same binding, an assignment updates the same binding, and leaving a scope keeps the same bindings. |
| `Gala.Spec.lookup_bind_self`, `lookup_bind_other` | A binding shadows its own name and no other. |
| `Gala.Spec.update_length`, `exitScope_push` | An assignment keeps the scope stack's shape, so leaving a block drops its own bindings and keeps its assignments to outer variables. |

These are theorems about the *spec* (and a model of one lowering), not about the
transpiler. T2 for whole programs (renaming every binder leaves the outcome unchanged) is
not proved: the lemmas above are the name-resolution part of it.

## What is not covered

Everything outside S1: the rest of S2 (lambdas, default and named arguments,
`Option`/`Either`/`Try`, format strings `f"..."`), generics, methods (and so `Copy`/`Equal`
calls), collections, loops, Go interop, and the rest. The section below, generated from a differential run, gives
every example's verdict.

<!-- difftest:begin -->
## Differential testing

Generated by `internal/formal/difftest` from one run; do not edit by hand.
Each program runs through galaspec and through the transpiler and Go; the two
runs are compared on stdout and outcome. These are test results, not proofs: they
say the programs below behave as the spec says, not that the transpiler is correct.

### Verdicts

| Verdict | Examples | Generated programs |
|---|---|---|
| AGREE | 16 | 1 |
| DISAGREE | 0 | 0 |
| UNSUPPORTED | 436 | 34 |
| INCONCLUSIVE | 0 | 0 |

Examples that run wholly inside the subset and agree with the spec: **16** (ratchet baseline 16).

### Constructs

A construct is **modelled** when galaspec ran a program using it to the end, and
**tested against the spec** when at least 3 programs using it agree and none
disagrees or is inconclusive. **Proved** lists the kernel-checked theorems about the
spec that cover it. A construct no program reached is "not seen": the run says
nothing about it.

| Construct (Core JSON kind) | Modelled | Tested against the spec | Proved |
|---|---|---|---|
| `file` | yes | yes: 17 agree | no |
| `val` | yes | yes: 4 agree | no |
| `var` | yes | yes: 3 agree | no |
| `func` | yes | yes: 17 agree | no |
| `param` | yes | yes: 15 agree | no |
| `struct` | yes | no: 1 agree | no |
| `field` | yes | no: 2 agree | no |
| `sealed` | yes | yes: 4 agree | no |
| `variant` | yes | yes: 4 agree | no |
| `type-name` | yes | yes: 15 agree | no |
| `block` | yes | yes: 17 agree | no |
| `if` | yes | yes: 4 agree | no |
| `return` | yes | yes: 3 agree | no |
| `assign` | yes | yes: 3 agree | no |
| `incdec` | not seen | no | no |
| `int` | yes | yes: 15 agree | no |
| `bool` | yes | yes: 4 agree | no |
| `string` | yes | yes: 12 agree | no |
| `name` | yes | yes: 17 agree | no |
| `unary` | yes | yes: 3 agree | no |
| `binary` | yes | yes: 15 agree | no |
| `call` | yes | yes: 17 agree | no |
| `select` | yes | no: 2 agree | no |
| `paren` | yes | no: 1 agree | no |
| `tuple` | not seen | no | no |
| `if-expr` | yes | yes: 8 agree | no |
| `match` | yes | yes: 11 agree | no |
| `case` | yes | yes: 11 agree | no |
| `pat-wild` | yes | yes: 7 agree | no |
| `pat-name` | yes | yes: 5 agree | no |
| `pat-lit` | yes | yes: 7 agree | no |
| `pat-tuple` | not seen | no | no |
| `pat-extract` | yes | no: 2 agree | no |
| `interp` | yes | yes: 5 agree | no |
| `interp-text` | yes | yes: 5 agree | no |

### Outside the subset

The constructs galaspec stopped at, by how many programs it stopped at each (41 kinds, the top 20 shown):

| Construct | Programs |
|---|---|
| `method-call` | 133 |
| `import` | 107 |
| `index` | 66 |
| `generic-type` | 40 |
| `named-arg` | 15 |
| `lambda` | 14 |
| `generic-decl` | 12 |
| `address-of` | 8 |
| `param-default` | 8 |
| `unresolved-name` | 8 |
| `float-literal` | 6 |
| `func-type` | 6 |
| `composite-literal` | 5 |
| `local-struct` | 4 |
| `stuck` | 4 |
| `slice-type` | 3 |
| `expression-pattern` | 2 |
| `for` | 2 |
| `func-value` | 2 |
| `method` | 2 |

### Known disagreements

None.
<!-- difftest:end -->
