# Coverage of the Lean semantics

Written by hand for now. Once the differential harness runs in CI, CI generates this file
(see [docs/FORMAL_SEMANTICS.MD](../docs/FORMAL_SEMANTICS.MD#coverage)).

The tiers mean exactly this:

- **Modelled:** the construct is in a Lean model. For the reference interpreter
  (`galaspec`), programs using it run.
- **Tested against the spec:** generated programs and examples using it agree with the
  spec. Nothing is in this tier yet: the differential harness that would put constructs
  here is not running. The fixture tests below are regression tests of the interpreter,
  not differential results.
- **Proved:** a kernel-checked theorem, with no `sorry` and only Lean's standard axioms,
  covers it.

## The reference interpreter: subset S1

`galaspec` (`Gala/Syntax.lean`, `Json.lean`, `Scope.lean`, `Eval.lean`, `Print.lean`,
`Cli.lean`) models subset S1 of the design.

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
| Determinism of every run (T1) | — | — | yes |

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

- 10 programs in `tests/src/` (integer arithmetic, printing, `match`, evaluation order,
  package-level initialization, scoping, three panic classes, fuel), and a document with
  an unknown `core` version, which is malformed.
  Each `.out` is what the program prints when built and run with the `gala` CLI; for
  `panic_match`, which GALA rejects as not exhaustive, it is the spec's own answer.
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
| `Gala.Spec.run_deterministic`, `eval_deterministic` | **T1.** A program and a fuel budget have one output and one outcome; an evaluation from a given state has one result and final state. |
| `Gala.Spec.lookup_rename`, `update_rename`, `exitScope_rename` | **T2, for name resolution only.** Under an injective renaming of an environment, a renamed name resolves to the same binding, an assignment updates the same binding, and leaving a scope keeps the same bindings. |
| `Gala.Spec.lookup_bind_self`, `lookup_bind_other` | A binding shadows its own name and no other. |
| `Gala.Spec.update_length`, `exitScope_push` | An assignment keeps the scope stack's shape, so leaving a block drops its own bindings and keeps its assignments to outer variables. |

These are theorems about the *spec* (and a model of one lowering), not about the
transpiler. T2 for whole programs (renaming every binder leaves the outcome unchanged) is
not proved: the lemmas above are the name-resolution part of it.

## What is not covered

Everything outside S1: lambdas, default and named arguments, `Option`/`Either`/`Try`,
string interpolation, generics, methods (and so `Copy`/`Equal` calls), collections, loops,
Go interop, and the rest. No program has been tested against the spec by the
differential harness yet, so no example has a verdict.
