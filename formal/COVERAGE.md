# Coverage of the Lean semantics

Written by hand, except two generated sections at the end: "Differential testing", which a
differential run generates (see [docs/FORMAL_SEMANTICS.MD](../docs/FORMAL_SEMANTICS.MD#coverage)), and
"Ownership", which `bazel run //internal/formal/ownershipcov/update` generates.

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
| Blocks and their scopes, shadowing | yes | no | no |
| `if` statement and expression, `return`, trailing-expression results | yes | no | no |
| Top-level functions and recursion | yes | no | no |
| Structs (block and shorthand), construction, field access | yes | no | no |
| Sealed types, construction, default case as zero value | yes | no | no |
| `match`: wildcard, binding, literal, tuple, nested variant and struct extractor patterns, stable identifiers, guards; arms in order, subject evaluated once, binders scoped to their arm, first failing sub-pattern stops the rest, no arm ↦ `panic match` | yes | no | no |
| `Println`/`Print` formatting of every S1 value | yes | no | no |
| Strict left-to-right evaluation of operands, arguments and tuple elements | yes | no | no |
| S2: interpolated strings `s"..."`: parts left to right, each embedded value printed as `Println` prints it; `$$` is `$`; a `%...` after an embedded expression is literal text | yes | no | no |
| S2: lambdas `(x int) => ...` and closures: captured locals by reference (a `var` is shared with the lambda), a block body without a result type is void unless it `return`s a value, `return` ends the lambda; top-level functions as values; function types; calling a function value, a function-typed field, or a call's result | yes | no | capture of the scope only |
| S2: named arguments and default values of parameters, struct fields and case fields: arguments evaluated in the order written, then passed in parameter order; each left-out parameter's default evaluated after them, in parameter order, in the callee's scope (a function's default sees the parameters before it, a field's default no field) | yes | no | the scope of a default only (T4) |

Untyped constant expressions are not folded: Go rejects `9223372036854775807 + 1` at
compile time, while galaspec wraps it at run time. Only programs Go rejects differ.

`int64` is accepted as a type and means the same as `int`. Every other numeric type is
outside S1 and reported as unsupported.

### Printing

A shorthand struct prints as `Pt(1, 2)`, through the `String` method GALA generates for
it (docs/GALA.MD, "How Values Print"). A block-form struct (`type T struct { ... }`) prints
what Go's `fmt` prints for it: `{1 2}`, `{1}` for a lowercase `val` field (its
`Immutable` wrapper, whose `Format` method `fmt` cannot call through an unexported field),
and for a sealed value reached through such a field, its Go layout. `Gala/Print.lean`
models both.

### Where galaspec stops

A run ends `unsupported:<kind>` when it reaches:

- an `unsupported` node of Core JSON: the kind is the exporter's name (`lambda`,
  `import`, …);
- a name that resolves to nothing: the kind of the first top-level declaration outside
  S1 if there is one (most often `import`), else `unresolved-name` (Go builtins such as
  `println` or `len`, std names such as `Some`);
- a method call or method value: `method-call`, `method-value` (the generated `Copy` and
  `Equal` included);
- a local `func` or `struct` declaration (`local-func`, `local-struct`); a struct or
  variant constructor used as a value (`func-value`); a placeholder lambda `_ * 2`
  (`placeholder-lambda`); a plain expression passed as a by-name argument, which only a
  typed `func() T` parameter turns into a thunk (`by-name-arg`); the zero value of a
  function type, `nil` (`nil`); a lambda parameter with a default (`param-default`); a
  named argument to a function value (`named-arg`); a positional argument after a named
  one (`positional-after-named`); a type outside S1 (`type-<name>`); an
  integer constant outside int64 (`int-constant-overflow`); a cycle in package-level
  initialization (`init-cycle`); a program without `main` (`no-main`);
- printing a struct or sealed value in a program that declares methods (a `String`
  method would change the text): `method`; a layout `fmt` would print as a pointer
  address: `print-pointer`;
- anything a program GALA accepts cannot do (a type error, a wrong arity): `stuck`.

### What is tested

`lake exe galaspec-tests` runs the fixtures in [`tests/`](tests):

- 14 programs in `tests/src/` (integer arithmetic, printing, `match`, evaluation order,
  interpolated strings, lambdas and closures, named arguments and defaults,
  package-level initialization and its order, scoping, three panic classes, fuel), and a
  document with
  an unknown `core` version, which is malformed.
  Each `.out` is what the program prints when built and run with the `gala` CLI; for
  `panic_match`, which GALA rejects as not exhaustive, it is the spec's own answer.
- 14 S1 examples from `examples/`: 8 run to the end and print the example's `.out`, 2 end
  in fuel after a prefix of it, 4 are unsupported (`println`, `Some`, `Equal`).

Plus `#guard` checks of integer wrapping, literal parsing, print spacing, the decoder's
version check and the command line, run while the tests compile.

## What is proved

The proof checks (`scripts/ProofCheck.lean` under Lake, `//formal:proofs` under Bazel)
fail if a theorem listed here is missing; keep `requiredTheorems` there and
`REQUIRED_THEOREMS` in `BUILD.bazel` in step with this table. Each one's statement is
restated in `Gala/Statements.lean` and checked against its proof, so a changed statement
fails the build until that file changes too. The definitions a statement mentions are not
pinned that way.

| Theorem | Statement |
|---|---|
| `Gala.MatchLowering.compile_correct`, `program_correct` | In the lowering model only (a small GALA core with flat constructor patterns, no guards or literals, integers only; not galaspec, and not the transpiler's output): the scoped match lowering preserves meaning, for values and errors. "No arm matched" becomes a Go panic. |
| `Gala.MatchLowering.leaky_not_correct` | In the same model, a lowering that declares an arm's pattern variables before its `if` (issue [#996](https://github.com/martianoff/gala/issues/996)) does not preserve meaning. The counterexample evaluates to 3 where GALA says 10. |
| `Gala.Spec.capturedEnv_captured` | **Closures capture their scope.** A lambda's call sees exactly the bindings of the scope it was written in, a `var` by the same cell, so each side sees the other's assignments. Stated for scopes whose `var`s are exactly their cells; that evaluation keeps this invariant is not proved. |
| `Gala.Spec.defaultFrame_other`, `defaultFrame_last` | **T4, the scope only.** The frame a default value is evaluated in holds only the parameters before it, the last bound to the value the call gave it: no local of the caller's is visible there. That `bindArgs` uses this frame, and evaluates arguments as written and then defaults in parameter order, is its definition, not a theorem. |
| `Gala.Gala2Lean.Examples.Match.eval_total` | About one program: gala2lean's translation of `tests/src/match.gala`, exactly as the translator writes it, under gala2lean's prelude. Its `eval` (a recursive `match` with a nested pattern, defined by `partial_fixpoint`) never panics or diverges, leaves the state unchanged, and computes the obvious fold over the expression tree. |

These are theorems about the *spec* (and a model of one lowering, and one program's
translation under gala2lean's prelude), not about the transpiler.

Not proved: T1, T2, T3 and T5–T7, and T4 beyond the scope of a default value.
[docs/FORMAL_SEMANTICS.MD](../docs/FORMAL_SEMANTICS.MD#5-theorems-the-road-to-an-end-to-end-proof) gives the statements
they need: fuel adequacy and monotonicity for T1, alpha-invariance with its side
conditions for T2, and an observational statement about the order of effects for T3.

## gala2lean

[gala2lean](README.md#gala2lean-a-third-oracle) translates the programs it covers to
Lean definitions. It translates subset S1 and the S2 constructs galaspec models (as
galaspec models them, except as listed below):

- interpolated strings `s"..."`, each part printed as `Println` prints it;
- named arguments and default values: arguments in the order written, then each
  left-out default in parameter order, in the callee's scope; package-level
  initialization follows the defaults a call leaves out, and only those;
- lambdas, function types and functions as values. A `var` a lambda refers to lives in a
  reference cell in the program state (one heap per type), so the lambda and the code
  around it share it, as GALA's capture by reference says; a `func() T` parameter turns
  a plain argument into a thunk;
- `Some`, `None`, `Left` and `Right`, as the prelude's `GOption` and `GEither`, with
  their patterns (their methods are method calls, not translated yet).

It translates a whole program or nothing: one construct outside its subset anywhere in
the program, even in a function `main` never calls, stops it, and is reported. Beyond
what Core JSON leaves out, it does not translate yet:

- a `return` or an assignment to an outer `var` inside an expression
  (`return-inside-expression`, `var-mutated-inside-expression`);
- a lambda whose parameter types neither it nor its context gives
  (`untyped-lambda-param`); the zero value of a function type (`nil`); printing a
  function, or a value whose text would hold a function's address (`print-pointer`); a
  function kept in the program state, as a package-level variable or a captured `var`
  (`func-in-state`); a type Lean cannot infer where Core JSON gives none, such as a
  `None()` printed on its own (`lean-type-inference:<function>`, reported by the run);
- tuples of more than four elements; a sealed type used as a zero value without a
  default case (`sealed-zero-value`); a package-level variable whose type is not plain to
  see from its initializer (`untyped-global`); local functions and structs;
- printing or comparing a value of a type other than `int`, `string` or `bool` in a
  program that declares methods (`method`).

The section below gives each example's gala2lean verdict next to galaspec's.

## What is not covered

Everything outside S1: the rest of S2 (`Option`/`Either`/`Try`, format strings `f"..."`), generics, methods (and so `Copy`/`Equal`
calls), collections, loops, Go interop, and the rest. The section below, generated from a differential run, gives
every example's verdict.

<!-- difftest:begin -->
## Differential testing

Generated by `internal/formal/difftest` from one run; do not edit by hand.
Each program runs through galaspec and through the transpiler and Go; the two
runs are compared on stdout and outcome. These are test results, not proofs: they
say the programs below behave as the spec says, not that the transpiler is correct.

Each program gala2lean translates is also run as Lean, a third account of what
it means, and compared with the Go run (the gala2lean columns). A program it
translates and that agrees says that the translation and Go print the same
thing for it, nothing more.

### Verdicts

| Verdict | Examples | Generated programs | gala2lean: examples | gala2lean: generated programs |
|---|---|---|---|---|
| AGREE | 28 | 1 | 29 | 1 |
| DISAGREE | 0 | 0 | 0 | 0 |
| UNSUPPORTED | 448 | 34 | 447 | 34 |
| INCONCLUSIVE | 0 | 0 | 0 | 0 |

Examples that run wholly inside the subset and agree with the spec: **28** (ratchet baseline 28).
Examples gala2lean translates whose Lean run agrees with Go: **29** (ratchet baseline 29).

### Constructs

A construct is **modelled** when galaspec ran a program using it to the end, and
**tested against the spec** when at least 3 programs using it agree and none
disagrees or is inconclusive. **Proved** lists the kernel-checked theorems about the
spec that cover it. A construct no program reached is "not seen": the run says
nothing about it.
**gala2lean** says how the programs using it that gala2lean translated fared
when run as Lean and compared with Go.

| Construct (Core JSON kind) | Modelled | Tested against the spec | Proved | gala2lean |
|---|---|---|---|---|
| `file` | yes | yes: 29 agree | no | translated: 30 agree |
| `val` | yes | yes: 11 agree | no | translated: 12 agree |
| `var` | yes | yes: 4 agree | no | translated: 5 agree |
| `func` | yes | yes: 29 agree | no | translated: 30 agree |
| `param` | yes | yes: 26 agree | no | translated: 25 agree |
| `struct` | yes | yes: 3 agree | no | translated: 3 agree |
| `field` | yes | yes: 7 agree | no | translated: 7 agree |
| `sealed` | yes | yes: 7 agree | no | translated: 7 agree |
| `variant` | yes | yes: 7 agree | no | translated: 7 agree |
| `type-name` | yes | yes: 27 agree | no | translated: 26 agree |
| `block` | yes | yes: 29 agree | no | translated: 30 agree |
| `if` | yes | yes: 6 agree | no | translated: 5 agree |
| `return` | yes | yes: 5 agree | no | translated: 5 agree |
| `assign` | yes | yes: 4 agree | no | translated: 5 agree |
| `incdec` | not seen | no | no | not translated |
| `int` | yes | yes: 26 agree | no | translated: 26 agree |
| `bool` | yes | yes: 8 agree | no | translated: 7 agree |
| `string` | yes | yes: 21 agree | no | translated: 22 agree |
| `name` | yes | yes: 29 agree | no | translated: 30 agree |
| `unary` | yes | yes: 5 agree | no | translated: 6 agree |
| `binary` | yes | yes: 22 agree | no | translated: 22 agree |
| `call` | yes | yes: 29 agree | no | translated: 30 agree |
| `select` | yes | yes: 4 agree | no | translated: 3 agree |
| `paren` | yes | no: 1 agree | no | not translated |
| `tuple` | not seen | no | no | not translated |
| `if-expr` | yes | yes: 10 agree | no | translated: 9 agree |
| `match` | yes | yes: 13 agree | no | translated: 15 agree |
| `case` | yes | yes: 13 agree | no | translated: 15 agree |
| `pat-wild` | yes | yes: 7 agree | no | translated: 8 agree |
| `pat-name` | yes | yes: 7 agree | no | translated: 10 agree |
| `pat-lit` | yes | yes: 7 agree | no | translated: 6 agree |
| `pat-tuple` | not seen | no | no | not translated |
| `pat-extract` | yes | yes: 4 agree | no | translated: 7 agree |
| `named-arg` | yes | yes: 5 agree | no | translated: 5 agree |
| `lambda` | yes | yes: 5 agree | no | translated: 4 agree |
| `type-func` | yes | yes: 3 agree | no | translated: 2 agree |
| `interp` | yes | yes: 13 agree | no | translated: 15 agree |
| `interp-text` | yes | yes: 13 agree | no | translated: 15 agree |

### Outside the subset

The constructs galaspec stopped at, by how many programs it stopped at each (41 kinds, the top 20 shown):

| Construct | Programs |
|---|---|
| `import` | 132 |
| `method-call` | 123 |
| `index` | 72 |
| `generic-type` | 53 |
| `generic-decl` | 15 |
| `address-of` | 12 |
| `unresolved-name` | 9 |
| `float-literal` | 8 |
| `composite-literal` | 7 |
| `local-struct` | 4 |
| `stuck` | 4 |
| `by-name-arg` | 3 |
| `method` | 3 |
| `slice-type` | 3 |
| `char-literal` | 2 |
| `expression-pattern` | 2 |
| `for` | 2 |
| `format-string` | 2 |
| `type-Value` | 2 |
| `type-any` | 2 |

### Outside what gala2lean translates

The first construct gala2lean did not translate in each program, by how many programs (61 kinds, the top 20 shown):

| Construct | Programs |
|---|---|
| `generic-type` | 147 |
| `import` | 111 |
| `index` | 46 |
| `method-call` | 30 |
| `type-float64` | 13 |
| `qualified-type` | 11 |
| `method` | 10 |
| `pointer-type` | 6 |
| `slice-type` | 6 |
| `variadic-param` | 6 |
| `address-of` | 5 |
| `type-error` | 5 |
| `generic-decl` | 4 |
| `local-struct` | 4 |
| `type-any` | 4 |
| `expression-pattern` | 3 |
| `extractor-arity` | 3 |
| `func-in-state` | 3 |
| `return-inside-expression` | 3 |
| `type-UserID` | 3 |

### Known disagreements

None.
<!-- difftest:end -->

<!-- ownership:begin -->
## Ownership: why pointers are not unique

Generated by `bazel run //internal/formal/ownershipcov/update`; do not edit by hand.
It is the output of phase 1 of [the ownership design](../docs/OWNERSHIP_DESIGN.MD) (inferred
uniqueness, `internal/transpiler/ownership`) over the repository's GALA code. A pointer
binding (a `Ptr`/`*T` receiver or parameter, or a local `val` bound to a pointer) is
**unique** when it is of fresh origin, only ever lent downward, never reaches Go, and points
at an owned-shaped value. These are analysis results, not proofs.

| | Libraries | Examples | 
|---|---|---|
| Functions and methods | 1758 | 1686 | 
| …with a pointer binding | 584 | 133 | 
| …whose pointer bindings are all unique | 0 (0.0%) | 2 (1.5%) | 
| Pointer bindings | 834 | 197 | 
| …receivers unique | 0 of 467 | 0 of 48 | 
| …params unique | 0 of 90 | 2 of 44 | 
| …locals unique | 0 of 277 | 4 of 105 | 
| Packages that did not analyze | 0 | 2 | 

### Reasons

Each binding that is not unique is counted once under every reason it has. The first
location is where the analysis first met the reason.

| Reason | Meaning | Libraries | Examples | First location |
|---|---|---|---|---|
| `stored` | stored in a field, constructor, literal, tuple or Option | 18 | 3 | `collection_mutable/array.gala:431` |
| `collection` | aliased through a collection | 24 | 5 | `collection_mutable/array.gala:387` |
| `aliased` | bound to another name | 13 | 1 | `collection_immutable/array.gala:422` |
| `returned` | returned while not fresh | 75 | 5 | `collection_immutable/array.gala:499` |
| `captured` | captured by a closure, method value, by-name argument or bind | 119 | 18 | `collection_immutable/array.gala:638` |
| `passed-to-go` | it, or a reference inside its pointee, flows into Go | 315 | 36 | `collection_immutable/array.gala:184` |
| `compared` | compared or matched | 1 | 12 | `std/ptr.gala:14` |
| `escaping-callee` | lent to a parameter or receiver that escapes | 293 | 75 | `collection_immutable/array.gala:98` |
| `unknown-callee` | used in a call without a summary | 22 | 10 | `collection_mutable/array.gala:106` |
| `rebound` | its name is bound more than once | 20 | 0 | `collection_immutable/treemap.gala:329` |
| `other` | another use (reassigned, indexed, address taken, an operand) | 26 | 1 | `collection_immutable/array.gala:332` |
| `origin` | not of fresh origin | 282 | 67 | `collection_immutable/array.gala:96` |
| `from-go` | received from Go | 497 | 63 | `collection_immutable/array.gala:183` |
| `shape` | the pointee is not owned-shaped | 834 | 140 | `collection_immutable/array.gala:96` |
| `nullable` | a `*T` that may be nil | 11 | 77 | `collection_immutable/array.gala:422` |

### Where pointers reach Go

The Go functions, methods and conversions that `passed-to-go` bindings reach, by how many
bindings reach each (the 15 most frequent).

| Go side | Bindings |
|---|---|
| `passed-to-go:a reference inside it` | 158 |
| `passed-to-go:a reference inside it, passed to the function value f` | 47 |
| `passed-to-go:a reference inside it, passed to go_interop.SliceAppend` | 19 |
| `passed-to-go:a reference inside it, passed to the function value p` | 19 |
| `passed-to-go:a reference inside it, passed to std.Some` | 13 |
| `passed-to-go:a reference inside it, passed to collection_immutable.treeMapNode` | 12 |
| `passed-to-go:a reference inside it, passed to collection_immutable.treeNode` | 11 |
| `passed-to-go:a reference inside it, passed to List.Append` | 8 |
| `passed-to-go:bytes.Buffer.WriteString receiver, on a field` | 8 |
| `passed-to-go:a reference inside it, passed to mutableTreeMapCompare` | 7 |
| `passed-to-go:a reference inside it, passed to treeMapCompare` | 7 |
| `passed-to-go:bytes.Buffer.WriteByte receiver, on a field` | 7 |
| `passed-to-go:a reference inside it, passed to compare` | 6 |
| `passed-to-go:a reference inside it, passed to std.Equal` | 6 |
| `passed-to-go:a reference inside it, passed to std.KeyIndex` | 6 |
<!-- ownership:end -->
