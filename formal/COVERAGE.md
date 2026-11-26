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
| `bool` literals, `! && \|\|` (short-circuit), comparisons | yes | no | short-circuit only (T3b) |
| `string` literals, `+`, comparisons (byte order) | yes | no | no |
| `==`/`!=` on structs, sealed values and tuples, by value | yes | no | no |
| `val`/`var` (several names, tuple destructuring), `=`, `+= -= *= /=`, `++`/`--`, assignment to `var` struct fields | yes | no | that `op=` reads its target before the right side runs (T3d) |
| `var x T` with no value: the type's default value (docs/GALA.MD, "Default Values"): Go's zero value for `int`, `bool`, `string`; a struct's fields, and a sealed type's default case's, each at its declared default, else at its type's default value. The declared defaults are evaluated for each name anew, in field order and depth first, with no local in scope, as the transpiler writes the value out at each use | yes | no | no |
| Package-level `val`/`var`, initialized in Go's dependency order (a declaration with no value depends on what its type's declared field defaults refer to, and a construction on those in the default values of the fields it leaves out) | yes | no | no |
| Blocks and their scopes, shadowing | yes | no | no |
| `if` statement and expression, `return`, trailing-expression results | yes | no | no |
| Top-level functions and recursion | yes | no | no |
| Structs (block and shorthand), construction, field access | yes | no | no |
| Sealed types, construction, the default case as the default value | yes | no | no |
| `match`: wildcard, binding, literal, tuple, nested variant and struct extractor patterns, stable identifiers, guards; arms in order, subject evaluated once, binders scoped to their arm, first failing sub-pattern stops the rest, no arm ↦ `panic match` | yes | no | sub-patterns stop at the first failure; a failed arm leaves only its guard's effects; the first matching arm runs (T3e, T3f); the scoped lowering to GoCore is correct, given correctly lowered parts and a subject of its static type (M3) |
| `Println`/`Print` formatting of every S1 value | yes | no | no |
| Strict left-to-right evaluation of operands, arguments and tuple elements | yes | no | the order, given what each part does (T3a, T3c) |
| S2: interpolated strings `s"..."`: parts left to right, each embedded value printed as `Println` prints it; `$$` is `$`; a `%...` after an embedded expression is literal text | yes | no | the order of the parts (T3a) |
| S2: lambdas `(x int) => ...` and closures: captured locals by reference (a `var` is shared with the lambda), a block body's trailing expression is its result where the context or a declaration gives a result type, and with none a trailing value is a compile error and the block is void unless it `return`s a value, `return` ends the lambda; top-level functions as values; function types; calling a function value, a function-typed field, or a call's result | yes | no | capture of the scope only |
| S2: named arguments and default values of parameters, struct fields and case fields: arguments evaluated in the order written, then passed in parameter order; each left-out parameter's default evaluated after them, in parameter order, in the callee's scope (a function's default sees the parameters before it, a field's default no field); a left-out field without a declared default (which a constructor call may leave out, among the types declared in GALA, only for a block-form `type T struct`: for a shorthand struct or a case GALA rejects it, GALA-E0045, a static check galaspec leaves to the type checker) takes its type's default value, its declared defaults evaluated in its turn, with no local in scope | yes | no | the defaults phase (T4): it does not read the caller's locals, and each default's frame binds exactly the earlier parameters; the order: written arguments, then defaults in parameter order (T3c, for calls whose every left-out parameter or field has a declared default) |

An operator expression of literals only is a Go constant expression, evaluated exactly at
compile time (#1237): `(1 << 70) >> 68` is `4`. galaspec folds each one before it runs a
program (`Gala/Const.lean`). Within such an expression, a division by zero, a negative
shift count, a value of 512 bits or more and a shift count above 1024 (bounds of the
model, within gc's), all of which Go rejects at compile time, stop a run that reaches
them `unsupported`; so does a result that does not fit `int`. None is ever wrapped. A
division of a variable by the literal `0` is not a constant expression, and stays a
run-time panic.

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
  integer constant outside int64 (`int-constant-overflow`), a constant division by zero
  (`constant-division-by-zero`), a negative constant shift count (`constant-negative-shift`)
  or a constant beyond the model's bounds (`constant-too-large`); a cycle in package-level
  initialization (`init-cycle`); a program without `main` (`no-main`);
- an import, before anything runs (`import`; `blank-import` for `import _ "pkg"`): it
  runs the package's initializers whether or not the run reaches a name of it, and
  galaspec does not model them. A named import of one of a fixed list of packages
  (`Json.noOutputInitImports`, exact paths: Go standard-library packages such as `fmt`
  and `strings`, and GALA's `std` and `collection_immutable`, which the generated Go
  imports for any program using `Option` or `Array`) does not stop the run: they print
  nothing during initialization, which galaspec relies on for the listed paths (for
  the two GALA packages, a gala2lean test checks their package-level initializers). A
  name of such a package still stops the run when it is reached (`import`);
- printing a struct or sealed value in a program that declares methods (a `String`
  method would change the text): `method`; a layout `fmt` would print as a pointer
  address: `print-pointer`;
- anything a program GALA accepts cannot do (a type error, a wrong arity): `stuck`.

### What is tested

`lake exe galaspec-tests` runs the fixtures in [`tests/`](tests):

- 17 programs in `tests/src/` (integer arithmetic, printing, `match`, evaluation order,
  interpolated strings, lambdas and closures, named arguments and defaults, a by-name
  default whose name a caller's local shadows,
  package-level initialization and its order (with defaults that call their own function,
  in a branch that does not run and in one that does),
  scoping, three panic classes, fuel), and a
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
| `Gala.Spec.capturedEnv_captured` | **Closures capture their scope.** A lambda's call sees exactly the bindings of the scope it was written in, a `var` by the same cell, so each side sees the other's assignments. Stated for scopes whose `var`s are exactly their cells; that evaluation keeps this invariant is not proved. |
| `Gala.Spec.bindDefaults_callerLocals` | **T4, default arguments.** The defaults phase of a call (`bindDefaults`, the second half of `bindArgs`: each parameter the call leaves out takes its default, evaluated in the callee's scope) does not read the caller's locals. Run with any other locals, it gives the same values or the same `Stop` and leaves the same output, heap, package-level variables and steps. When it succeeds, the caller's locals are back as they were. This covers a function's defaults, a field's, and the declared defaults in the default value of a left-out field without one (`Gala/Defaults.lean`). It failed before galaspec resolved a by-name default's bare name in the default's own frame (fixture `default_byname`). Not covered: that a call's written arguments are evaluated first, in the caller's scope, which is `bindArgs`'s definition (an ordering statement belongs to T3). Also not covered: that the caller's locals come back after a panic inside a default, which no run can observe, since nothing catches a panic. |
| `Gala.Spec.defaultFrame_lookup`, `defaultFrame_other`, `defaultFrame_last` | **T4, the frame of a default.** The frame a default value is evaluated in binds exactly the parameters before it: each name to the value of the latest earlier parameter of that name, immutable; any other name, a local of the caller's included, is unbound there. `defaultFrame_other` and `defaultFrame_last` are special cases. That `bindDefaults` evaluates the default for parameter `j` in the frame of parameters `0..j-1`, with the values the call gave them (explicitly or by their own defaults), is its definition. |
| `Gala.Spec.evalList_acts`, `eval_binary_acts`, `eval_tuple_acts`, `eval_interp_acts`, `eval_and_acts`, `eval_or_acts`, `argSlots_eq`, `bindArgs_acts`, `bindDefaults_acts`, `eval_compound_acts`, `compound_eff`, `matchPats_append`, `matchPats_stops`, `evalCases_nil`, `evalCases_nomatch`, `evalCases_guard_false`, `evalCases_guard_true`, `evalCases_match` | **T3, left to right** (`Gala/Order.lean`). Each theorem takes, as hypotheses, what every part does: an `Eff`, a function of the starting state giving the part's result (a value or a `Stop`), the state it leaves and the exact number of steps it spends (`Acts`). Its conclusion is what the compound does, built from the parts in source order: each part runs from the state the one before it left, and none runs after one that stopped. Steps are exact, so no statement holds by running out of fuel. Operand lists (`evalList_acts`; the same for binary operands, tuples and `s"..."` parts). `&&` and `\|\|` run the right operand only when the left one does not decide. A call's written arguments run in written order, named ones included, then each left-out parameter's default runs in parameter order; `argSlots_eq` says which parameter each argument goes to (its position if positional, else the one it names). This is stated for calls whose by-name checks pass in every scope: every argument and default for a parameter that is not `func() T`, and every lambda; a bare function name passed to a `func() T` parameter is not covered. `t op= x` reads `t` before `x` runs, and stores into the state `x` left. Sub-patterns: once one fails, later ones are never tried; in S1 a pattern has no effect, so this matters only for a later sub-pattern that would be `stuck` or `unsupported`. `match` arms: a failed pattern leaves the state unchanged; a false guard leaves its own effects but drops the arm's bindings (stated for a guard that keeps the locals, which every S1 guard expression does, since blocks pop their own scope); a matching arm with no guard or a true guard runs its body and then drops its bindings (#996); with no arm left the match panics. Induction on the arms combines these: the first arm whose pattern matches and whose guard holds is the one that runs. Not covered: `if`, blocks, declarations, `++`/`--`, multi-target `=`, and calls of function values (`callValue`) beyond their arguments. `eval_bool_acts` and an example derive the `Acts` of `true && false` from its literals, so the hypotheses can be met. |
| `Gala.Gala2Lean.Examples.Match.eval_total` | About one program: gala2lean's translation of `tests/src/match.gala`, exactly as the translator writes it, under gala2lean's prelude. Its `eval` (a recursive `match` with a nested pattern, defined by `partial_fixpoint`) never panics or diverges, leaves the state unchanged, and computes the obvious fold over the expression tree. |
| `Gala.Spec.run_fuel_adequate` | **T1, fuel adequacy.** For every program and fuels `n ≤ m`: if galaspec's run with fuel `n` does not end in `fuel`, the run with fuel `m` ends the same way (`ok`, the same panic, `unsupported` or `stuck`) and prints the same output. So an `AGREE` or `DISAGREE` verdict stays the same with any larger fuel budget (a smaller one can turn it into `INCONCLUSIVE`). It says nothing about a run that ends in `fuel`: `INCONCLUSIVE` stays inconclusive. Covers all of `run` (`Gala/Fuel.lean`). |
| `Gala.Spec.lookup_rel`, `resolve_rel`, `matchPat_rel`, `argSlots_rel`, `argSlots_same_rel`, `byNameOk_rel` | **Lemmas for T2** (`run_alpha` below). `Gala/Alpha.lean` defines when one program is another with its binders renamed (`AlphaE`, with `NameR`, `PatR`, `ValR`, `EnvR`, `DeclsR`): a renamed binder is lowercase on both sides, never `_` and never a function, struct or sealed case name (nor a package-level variable's, by `AlphaProgram`); a free name (a builtin included) is bound on neither side; a function's parameters stay distinct and a named argument follows its parameter's new name, a constructor's keeps its field name. Only the shapes the parser produces are related: a declaration as a block statement, a named argument as a call's argument; and binders inside a struct or case field's default are not renamed, and such a default must be related to itself with no local in scope (`FieldR`): it may not pass a named argument whose name the callee's parameter renaming changes. These lemmas show that name lookup, name resolution, pattern matching, binding a function's or a constructor's arguments to its slots and the by-name check give related results on related programs and states, with `stuck` and `unsupported` text the only difference allowed. |
| `Gala.Spec.binop_rel`, `unop_rel`, `fmtVs_rel`, `emit_rel`, `getField_rel`, `setField_rel`, `readPlace_rel`, `assignTo_rel`, `alloc_rel`, `pushAll_rel`, `construct_rel` | **Lemmas for T2** (`Gala/AlphaStep.lean`). The evaluator's steps that evaluate no subexpression give related results on related values and states: operators (the same value, the same panic, or `stuck` on both sides), printing (the same text: closures and functions print as an error, or as `<fn>` inside a generated `String`, on both sides), reading and setting fields, reading a place and assigning to it (locals, cells and package-level variables stay related), binding names (a `var`'s new cell has the same index on both sides) and building a struct or case. Struct and sealed declarations are the same on both sides, so field layout, zero values and type checks are the same functions. |
| `Gala.Spec.eval_alpha` | **A lemma for T2** (`Gala/AlphaEval.lean`). An expression and its renaming (`AlphaE`, under the conditions above), evaluated with related declarations from related states at any depth, give related results: equal scalars, related closures and functions, or `Stop`s of the same kind (the same panic and message, `fuel`, `unsupported` of the same kind, `stuck`, or a `return` of related values), with only the text of `stuck` and `unsupported` allowed to differ. They leave related states: the same output, so what is printed before a panic is the same; related locals, package-level variables and heap; and exactly the same steps left. After a value, the locals are related along the scope the expression started in. This covers every form the relation admits (a declaration only as a block statement, a named argument only as a call's argument), through every function of `eval`'s mutual block (blocks, declarations, `match`, calls of functions, closures and constructors, named arguments and defaults). A whole program's run, which also initializes the package-level variables and calls `main`, is `run_alpha`'s. An `example` applies it to `{ val x = 1; { val x = 2; Println(x) }; Println(x) }` with the inner `x` renamed to `y`. |
| `Gala.Spec.run_alpha` | **T2, alpha-invariance** (`Gala/AlphaProg.lean`). A program and its renaming (`AlphaProgram`) print exactly the same output and end the same way, for every fuel, up to the text of `unsupported` and `stuck` (`OutcomeR`: the same panic class and message, `fuel` on both sides, `unsupported` of the same kind). `AlphaProgram` relates the declarations `run` sees (`collect`), in the same order: functions, structs, sealed types and package-level variables correspond one to one, as do files and declarations within them. The renaming covers local binders only: block declarations, function and lambda parameters, pattern variables. It is capture-avoiding (no reference ends up bound by another binder, and no free name, builtins such as `Println` included, ends up bound by one; shadowing is allowed); a renamed binder is lowercase on both sides, never `_` and never a package-level name (function, package-level variable, struct or case); a function's parameters stay distinct and a named argument at a call of it follows its parameter's new name. Not covered: renaming top-level declarations (their names are equal on both sides), binders inside a struct or case field's default (and such a default may not pass a named argument whose name the renaming changes), and shapes the parser does not produce (a declaration other than as a block statement, a named argument other than as a call's argument). Built on `eval_alpha` for the initializers and `main`, and on the initialization order: `mentions` and `reach` list the same package-level names on both sides (`mentions_alpha`, `reach_alpha`), so `initGlobals` initializes the same declarations in the same order (`initGlobals_alpha`). |
| `Gala.Spec.alphaCheck_sound`, `alphaCheck_run` | **The renaming checker is sound** (`Gala/AlphaCheck.lean`, `Gala/AlphaProg.lean`). `alphaCheck p p'`, a `Bool` function galaspec runs as `galaspec alpha a.json b.json`, accepts only related programs: `alphaCheck p p' = true → AlphaProgram (alphaPkg …) (alphaPm …) p p'`, with the package-level names and each function's parameter pairing computed from the programs; so, by `run_alpha`, the two run the same way (`alphaCheck_run`). Completeness is not claimed: where the relation leaves a choice (a pattern name kept the same that could also count as a renamed one) the checker makes one. `internal/transpiler/fuzz/alpha_check_test.go` runs it on the metamorphic generator's renamings of local binders (`FuzzAlphaRenaming`'s generator, keeping top-level names, on a fixed batch of programs) and on a capturing renaming, which it must reject; two `example`s in `Gala/AlphaProg.lean` decide one accepted and one rejected pair in the kernel. |
| `Gala.M3.lowerMatch_correct` | **M3, the `match` lowering against galaspec** (`Gala/MatchLower.lean`). The scoped lowering of `match` into GoCore (`Gala/GoCore.lean`) is correct with respect to galaspec, *given that the subject, guards and bodies are lowered correctly*. "Correct" is `Part`: forward simulation from galaspec's results. From related states (the same output; the same local and package-level names in order, with encoded values; GoCore's temporaries left out) in which the static scope `inScope` is exactly the set of names bound (`ScopeOk`, locals and package-level variables), wherever galaspec's run of the `match` ends with a value or a panic, GoCore's run of the lowering ends with the encoded value or a panic of the same class, in related states, keeping both sides' frames. It says nothing about runs galaspec ends out of fuel, `stuck`, `unsupported`, or with a `return` inside an arm. Hypotheses: the lowered subject, guards and bodies are each `Part` in their scope (they are holes in GoCore, so they may print, panic and assign); a guard leaves galaspec's locals as it found them; and the subject has the static type the lowering was given ([the stated gap](#m3s-stated-gap-the-subject-has-the-static-type-the-transformer-inferred)). Covered pattern forms: `_`, a binder (`_` included), int, bool and string literals, stable identifiers (local or package-level), a bare case without fields, a sealed case's extractor with any number of fields, a struct's extractor, tuples, nested to any depth; guards. The lowering has the transformer's shape up to the normalizations listed in [docs/FORMAL_SEMANTICS.MD, M6](../docs/FORMAL_SEMANTICS.MD#m6-per-program-translation-validation); that it describes what the transformer emits is trusted. |
| `Gala.M3.lowerPat_correct`, `valEq_enc` | The pattern lowering agrees with galaspec's `matchPat` on a value of the static type: the condition is true exactly when galaspec matches, and then each pattern variable's expression gives the encoding of the value galaspec binds, in galaspec's order. Every read of an `Option`'s value sits after its `IsDefined()` in an `&&`, so GoCore's zero placeholder decides no test. galaspec's `==` (`valEq`) equals GoCore's (`geq`, defined from Go's `==` and the generated `Equal`) on encoded values. |
| `Gala.M3.part_lit_int`, `part_var`, `part_add`, `part_gt`, `lowerMatch_instance` | **The hypotheses can be met.** `Part` holds of an int literal, of a variable read (local or package-level), and of int `+` and `>` whose operands are `Part` and give ints. `lowerMatch_instance` applies `lowerMatch_correct` to `3 match { case 1 => 10; case x => 20; case _ => 30 }` (a literal, a binder and a wildcard), discharging every hypothesis, so the theorem is not vacuous. It has no guard and no body that reads a pattern variable: those need `Part` of an expression over typed locals, which needs T7 (the counterexample below computes the scoped result instead). |
| `Gala.M3.leaky_not_correct` | **#996 against galaspec.** The lowering issue [#996](https://github.com/martianoff/gala/issues/996) reported binds an arm's pattern variables before the arm's `if`, so they stay in scope in the later arms. It is not `Part` for galaspec: on `s match { case Circle(r) if r > 100 => r; case Circle(side) => side + r }` with `s = Circle(3)` and `r = 7`, galaspec gives 10 (`gala_says_10`, computed through galaspec's own equations), the leaky lowering 6 and the scoped lowering 10. The guard fails, so no zero value is involved. |
| `Gala.M5.eval_pure` | **M5, galaspec's side of a call-free expression** (`Gala/M5/SpecPure.lean`). For an expression of literals, locals, parentheses and unary and binary operators whose names are all locals, galaspec's `eval` runs out of fuel or gives what a step-free program, `pureEval`, gives, and leaves the state the program sees (`Core`) as it was. |
| `Gala.M5.expr_correct` | **M5, step 2a: call-free expressions against GoCore** (`Gala/M5/Expr.lean`). The reference lowering of a call-free expression into GoCore for whole programs (`Gala/GoCore/*.lean`) is correct with respect to galaspec, in GoCore's order of evaluation (left to right, which is gc's for operands that are not calls). The lowering is type-directed: it is given each local's kind (`int`, `bool`, `string`) and accepts an operator only on the kinds Go type-checks it on. For an expression it accepts and with no constant operator node (what galaspec's constant folding leaves), from related states (`LocRel`: each local the lowering knows is bound on both sides to related values of its kind, GoCore's typed; `Predeclared`: nothing shadows `true` and `false`), galaspec's state is unchanged apart from steps and, at every depth from `gdepth e` on, GoCore's evaluation gives a related value (`VRel`: the same `int`, in int64's range, `bool` or `string`, typed or an untyped constant; so `LocRel` assumes each local `int` is in range, as galaspec's always are) of the expression's kind, or the same run-time panic, leaving GoCore's state as it was. Nothing is claimed when galaspec runs out of fuel or stops `stuck` or `unsupported`. Covered: `int`, `bool` and `string` literals (a negative one as Go's `-` of its magnitude), locals, parentheses, unary `-` `+` `^` `!`, `+ - * / %`, the bitwise `& | ^ &^` and `< <= > >=` on `int`s (galaspec computes the bitwise operators through `Int64`, GoCore exactly and then reduced to `int`; they agree through core's `BitVec` lemmas, `Gala/M5/Bits.lean`), `==` and `!=` on one kind, string `+`, the orderings `< <= > >=` on strings (galaspec and GoCore both by code point; that Go's byte order agrees on UTF-8 is GoCore's model, not proved, tested against `go run` on the strings of the GoCore fixture `string_order`), `&&`, `||`. Shifts `<< >>` of a typed `int` by an `int` count, counts of 64 or more and negative ones included (a negative count panics with Go's text on both sides). Not covered yet: a shift of a literal (Go types the untyped constant from its context), a negative constant count (Go does not compile it), and everything with a call (later M5 steps). An instance in the same file meets every hypothesis, on `x + 1`; `inst_bitwise` and `inst_shift` compute each bitwise operator and shift (a typed count, `>>` of a negative, counts of 64 or more, a negative count) on both sides, with the answers Go gives; `inst_strcmp` computes the four string orderings on both sides, with typed and untyped operands. |
| `Gala.M5.stmts_correct`, `block_correct` | **M5, step 2b: `val`/`var`, `if` statements and blocks against GoCore** (`Gala/M5/Stmt.lean`). The reference lowering of a statement list (`lowerSs`) is correct with respect to galaspec, for GoCore's evaluator as it is. The shapes are the transformer's: `val x = e` and `var x = e` (one name, no type) become Go's `var x = e`; an `if` whose branches are blocks, its `else` a block or another `if`, becomes Go's `if`/`else if`; a block becomes Go's block; the expressions are step 2a's. From related states (`Inv`: `LocRel`, equal outputs, `Predeclared`), wherever galaspec's run of the list ends normally, GoCore's run of the lowering, at every depth from `sdepthL ss` on, ends normally in related states with the kinds the list declared (`ResL`: galaspec's locals only extended, both sides' cells only added to); wherever galaspec's run panics, GoCore's panics the same way, with equal outputs. `block_correct` is the same for a block, after which galaspec's locals are those before it. Nothing is claimed when galaspec runs out of fuel, stops `stuck` or `unsupported`, or ends with a `return` (step 2 has none). Not covered: assignments, expression statements, calls (step 3). A local GALA rejects as unused (GALA-E0102) is not excluded: the theorem is about GoCore's semantics, which runs such code. |
| `Gala.M5.stmts_correct_instance` | **The hypotheses can be met:** `val y = x + 1; if (y > 5) { var z = y; if (z > 0) {} } else { val w = 0; if (w > 1) {} }` (a body GALA and Go accept: every local is read) with `x = 5` on both sides, every hypothesis discharged. `inst_spec_ok` shows galaspec's run of it ends normally (computed through `eval`'s unfolding lemmas, as `eval` is well-founded), and `inst_gocore_ok` draws the conclusion: GoCore's run ends normally too, in a state related to galaspec's. `inst_runs` computes GoCore's side (cells `5`, `6`, `6`). |
| `Gala.M5.gc_runsStep2`, `leftToRight_runsStep2`, `hSs_lowered` | **The orders step 2b holds for** (`Gala/M5/StmtOrder.lean`). `RunsStep2AsWritten ord`: in every program, `ord` leaves every function body that is a lowering of step 2 without a shift (`Step2Body`) as written, in its place. A shift can panic with another text than a division, which can trigger gc's rewrite, so bodies with a shift are left to step 3 here. Left to right (the identity) meets it at once. GoCore's model of gc 1.25.5's order (`Order.gc`) meets it: `hSs_lowered` proves that the model's rewrite (`Hoist.hSs`) returns a lowered statement list unchanged in every scope, because none of its statements triggers the rewrite (no call, nothing that changes state, and the only panic is an integer division by zero, whose text is the same wherever it happens). The predicate constrains only step 2's function bodies, and no theorem takes it as a hypothesis yet: step 3's will, widened to the whole program. It is narrower than every order the Go spec allows: an order that copies an `&&` or `||` into a temporary first, as real gc's order.go does, rewrites these statements and is outside it, though here the copy cannot be observed; so the theorem is about the model, not about real gc. That full hoisting agrees with left to right is open and carried to M6, with `untriggered_orders_agree`: it needs a model of real gc's copying. |
| `Gala.M5.calls_correct`, `body_correct`, `stmts3_correct` | **M5, step 3a: direct calls and `return` against GoCore** (`Gala/M5/Call.lean`). The reference lowering of expressions with calls (`lowerC`), statements with `return` (`lowerSs3`) and function bodies (`lowerBody`) is correct with respect to galaspec, for GoCore's evaluator as it is. A call is of a top-level function whose parameters and result are `int`, `bool` or `string`, with positional arguments, one for each parameter (no named argument, no default, the subjects of later steps); `= e` is Go's `return e`, a block body its statements and `return` of its last element; `return e` inside a body is Go's `return`. `FunRel` says GoCore's package declares each function the lowering calls as the lowering of galaspec's. From related states (`Inv3`: step 2b's `Inv`, every local of both sides one the lowering knows, no package-level variable named as a called function) with no more galaspec steps than GoCore steps: wherever galaspec's run ends with a value, GoCore's run of the lowering, at every depth from `K * d` on (`d` galaspec's depth, `K` at least 11 and at least the GoCore depth of every call-free part, which the lowering at `K` checks; `lowerC_mono` and `lowerBody_mono`: a larger `K` lowers the same, so `K` excludes no program), ends with a related value, both states only gaining cells and GoCore's otherwise unchanged (`GFrame`, a record equality), and GoCore spending no more steps than galaspec (`Spent`, kept through every call: GoCore's one step per call is paid by galaspec's step at the call node); a panic is the same panic, with equal outputs; a `return` returns a related value of the result's kind; an expression never ends with a `return` (proved). Nothing is claimed when galaspec runs out of fuel or depth or stops `stuck` or `unsupported`; GoCore never runs out at the depths claimed. The depth bound is per program because a recursive call's GoCore depth is not bounded by the program's text. Proved by induction on galaspec's depth for expressions, argument lists, statements, lists, blocks, `if`s, block bodies and bodies together. |
| `Gala.M5.calls_correct_instance`, `inc_gocore_ok` | **The hypotheses can be met:** `inc(x) = x + 1` called as `inc(5)`, every hypothesis of `calls_correct` discharged (`FunRel`, `Inv3`, the lowering, by computation). `inc_spec_ok` computes galaspec's run (through `eval`'s unfolding lemmas: `eval` is well-founded) to end with `6`, and `inc_gocore_ok` draws the conclusion: GoCore's run of `inc(5)` ends with a related `int`. GoCore's run itself cannot be computed by the kernel here (its tables are hash maps of strings, and `String.hash` is opaque to it). |
| `Gala.M5.gc_runs3`, `leftToRight_runs3`, `canon_eq` | **The orders step 3a holds for** (`Gala/M5/CallOrder.lean`). `Body3` (decidable): no statement in which an operand that may panic (`/` or `%` by a non-literal, a shift by a non-literal count) precedes a call in the same operand list, a call's arguments being a list of their own, computed on `canon`, the events GoCore's model of gc reads in a scope the body fits. `canon_eq` proves that bridge: in a scope where no name the body reads may be changed by a call and every name it calls is a call of a single-result function (`FitsE`), `Hoist.scanE` is `canon`. `RunsAsWritten3 ord`: `ord` leaves in place every `Body3` function body that fits the scope gc's rewrite gives it (`gcScope`, `FitsSs`); `gc_runs3` proves `Order.gc` does, `leftToRight_runs3` left to right. `gc_runs3` holds for bodies that fit their scope; that lowered programs' bodies fit is owed by step 3d (from `FunRel` and the program's tables). The result is about GoCore's model of gc 1.25.5, not real gc, which copies every call into a temporary; that full hoisting agrees with left to right is carried to M6, which needs a model of that copying. The reads-a-changeable-variable half of the trigger is ruled out by `FitsSs` (owed by 3d), not proved here. |
| `Gala.M5.ordProg_gc`, `body3_div_call`, `body3_div_arg_call`, `body3_div_inside_call` | **The order result can be met:** on a program of a recursive `fact` (`if n <= 1 { return 1 }; return n * fact(n - 1)`) and `g(x) = fact(x) + 1`, both bodies are `Body3` and fit the scope gc's rewrite really builds for them (computed from the program), so `gc_runs3` leaves them in place; a `#guard` runs `Order.gc` on the program and compares (an evaluation, not a proof: the kernel cannot run it). `x / z + f(x)` and `g(x / z, f())` are not `Body3` (gc calls first, so a division by zero would come after the call's effects); `f(x / z) + g()` is (the division runs before `f` in both orders). The GoCore fixture `eval_order_call` pins the first against `go run`: `f` runs, printing, before the division panics. |
| `Gala.M5.calls_correct`, `body_correct`, `stmts3_correct` (step 3b) | **M5, step 3b: `if` used as a value, Go's IIFE** (`Gala/M5/Call.lean`). The same three theorems, for a lowering widened to `if` used as a value: as an expression it is Go's function literal called at once, `func() T { if c { T(a) } else { T(b) } }()`, each arm its tail (`tailOf`: its statements, then `return` of its last value; an arm that is an `if` is `{ if .. }`); a function's last value that is an `if` is that tail itself, with no IIFE. The arms have one kind, and the IIFE's value has its type, never an untyped constant (`ERes3 _ false`). The statement lowering takes the kind a `return` may have (`none` in an IIFE's arms: GALA's `return` there leaves the function, Go's would leave only the literal; `RetOK none` cannot hold, as a related value has a kind); 3a's theorems are the case `some ρ`, their meaning unchanged. GoCore's IIFE adds a closure to the state (`GFrame` lets the closures grow, as the cells) and takes one step for the call, paid by galaspec's step at the `if`. `K` is at least 11, the depth an IIFE takes; `lowerC_mono` and `lowerBody_mono` hold for the widened lowering. Proved in the same induction on galaspec's depth, with the tails (`TailSim`, `TsSim`) and the `if` of a tail or an IIFE (`IfTSim`). `match` used as a value is step 4's. |
| `Gala.M5.tailIf_lowerBody`, `armNest_lowerBody`, `retInArm_not_lowered`, `iife_correct_instance`, `if_spec_ok`, `iife_gocore_ok` | **Step 3b on the transpiler's shapes, and a typed run** (`Gala/M5/IifeInst.lean`). On five functions (`pick`, `blk`, `tailIf`, `argIf`, `armNest`) the lowering, computed by `rfl`, is the Go `gala transpile` emits, quoted next to each; outside the proofs, the galaspec syntax was compared with `galacore`'s export of the GALA source and the lowering with `gocorejson`'s export of the transpiler's Go, all five equal, and the fixture `iife_shapes` runs that Go against `go run`. A `return` in an arm is not lowered, nor a block whose last element is a block (its statements would join the enclosing Go block, where a name declared in both would be redeclared: `nestedBlock_not_lowered`), nor a body with a `val` named as a parameter at its top (`paramShadow_not_lowered`): the lowering checks that no Go scope declares a name twice (`scopeOK`, which Go rejects and GoCore does not check) and refuses bodies that do; the check rejects a second `t` in one `if` arm and accepts one `t` in each of two arms (`scopeOK_redecl_in_arm`, `scopeOK_sibling_arms`). `iife_correct_instance` meets every hypothesis of `calls_correct` on `(if (true) 1 else 2) + 3`, `if_spec_ok` computes galaspec's run to end with `4`, and `iife_gocore_ok` draws the conclusion: GoCore's run ends with a related, typed `int`. |
| `Gala.M5.iifeProg_gc`, `body3_div_iife`, `body3_iife_div_inside` | **The order result on IIFEs** (`Gala/M5/CallOrder.lean`, `IifeInst.lean`). An IIFE is a call to gc's model (an O-node): `Body3` treats it as one, and checks its body as a level of its own in the scope the rewrite gives it (`LitsQuiet`: the rewrite leaves the body as it is). `pick`'s lowering is `Body3` and fits its scope, so `gc_runs3` leaves it in place (`#guard` cross-check by evaluation); `x / z + func() int { .. }()` is not `Body3`, a division inside an arm is. The fixture `eval_order_iife` pins against `go run` that gc runs the literal's body first. Same limits as 3a: GoCore's model of gc 1.25.5, `FitsSs` owed by step 3d. |
| `Gala.M5.calls_correct`, `body_correct`, `stmts3_correct` (step 3c), `lowerFun_mono` | **M5, step 3c: the self-tail-call loop** (`Gala/M5/Loop.lean`). The same three theorems, their statements unchanged, now covering callees that loop: `FunRel` declares each function as `lowerFun` lowers it. Where the transformer loops a function (`loopGate`: an `if` used as a value as the body, through nested `if`s, with a leaf calling the function itself with an argument per parameter), `lowerFun` is its Go, `for { L(tree) }`: an `if` is `if c { .. } else { .. }`, a self-call leaf `_tmp_1 := a_1; ..; p_1, .., p_k = _tmp_1, ..; continue`, any other leaf `return` of its value; where the gate fires and that lowering does not apply (a named argument, an `if` in a step's argument, a compound or parenthesized leaf, a parameter named as a temporary), nothing, never the recursive lowering; elsewhere `lowerBody`. Inside a loop the parameters' cells are stored to in place (`GFrameP`, `ParamCells`); a caller sees `GFrame` again. **A tail loop is proved to agree for as many iterations as galaspec's depth allows**: each iteration is a galaspec call one level deeper, so the claim covers the runs galaspec ends within its depth; that a run diverging at every depth is one GoCore does not end normally is step 3d's. Six definitions changed: `FunRel` declares functions by `lowerFun` and gains `noTmp` (no called name is a temporary's); `Inv3.closed`'s GoCore half is required only of names that are not temporaries'; `Inv3` gains `sync` (as many cell types as cells, a new hypothesis of the three theorems: `allocAs` pushes both, stores set only, a run starts empty; that it holds of `runProgram`'s states is proved in 3d, `syncAll`); `GFrame` gains `Pre` on the cell types, equal growth of cells and types, and steps never more. `lowerFun_mono`: a larger `K` lowers the same. |
| `Gala.M5.sw_lowerFun`, `tmpParam_not_lowered`, `tmpCall_no_funRel`, `loop_calls_instance`, `sw_spec_ok`, `sw_gocore_ok` | **Step 3c on the transpiler's shape, and a run** (`Gala/M5/LoopInst.lean`). On `sw(a, b, n) = if (n == 0) a - b else sw(b, a, n - 1)` the lowering, by `rfl`, is the Go `gala transpile` emits (quoted; the transformer numbers temporaries across the program, the lowering per step from `_tmp_1`, M6's normalization). A parameter named `_tmp_1` is not lowered (`tmpParam_not_lowered`; `tmpParam_loop_lowers` shows only the temporaries' check refuses it). A body calling a function named `_tmp_1` in a step's argument is lowered (`tmpCall_lowered`), but no program declaring that function meets `FunRel` (`tmpCall_no_funRel`: Go would read the temporary there). `loop_calls_instance` meets every hypothesis of `calls_correct` on `sw(1, 2, 2)`, `sw_spec_ok` computes galaspec's run to end with `-1` after two iterations that swap the parameters (in place without the temporaries it would be `0`), and `sw_gocore_ok` draws the conclusion: GoCore's loop ends with a related `int`. The fixture `tail_loop` runs the transpiler's loops against `go run`, a swap and an argument that panics partway through included. `loop_correct`, the loop's own statement, is internal (not pinned). |
| `Gala.M5.simAll`, `runProgramAt_adequate`, `runProgram_adequate`, `syncAll`, `runProgramAt_sync`, `sw_gocore_more`, `sw_gocore_sync` | **M5, step 3d: GoCore's fuel adequacy** (`Gala/M5/GoFuel.lean`, `Gala/M5/GoFuelInst.lean`), GoCore's M1. `simAll`: every one of the 71 functions of the evaluator's mutual block that take a depth (its other two, `constTo` and `inferTArgs`, by lemmas of their own), started with `k` more steps and at any larger depth, ends a run that does not run out of either the same way, in the same state, with `k` more steps left (`GSim`); one induction on the depth. It rests on `tick` and `forLoop`'s bound being the only readers of the steps, every `Stop` handler passing `fuel` on, and `recover` reading the open deferred-call frames rather than the depth. `runProgramAt_adequate`: a program's run that does not end in `fuel` ends the same way and prints the same with any larger depth and fuel (`runProgram_adequate` with the depth capped). `syncAll`: every function keeps as many cell types as cells, and so do package initialization and the whole run (`initPkg_sync`, `runProgramAt_sync`), so `Inv3.sync` holds of `runProgram`'s states. The kernel does not evaluate a whole program's run (`buildCtx`'s hash maps keyed by strings), so the theorems are pinned on `sw(1, 2, 2)`: `sw_gocore_more` (with `j` more steps and any depth `N' ≥ N`, the same value and `j` steps left over) and `sw_gocore_sync`; the whole-program theorems have no instance on a concrete program yet: that comes with step 5's instances. |
| `Gala.M5.fitsSs_sound`, `lowerProg_funRel`, `lowerProg_gc`, `sw_lowerProg`, `inc_lowerProg`, `evenOdd_funRel`, `evenOdd_gc`, `dup_not_lowered`, `int_not_lowered`, `changeable_not_fits` | **M5, step 3d: the lowering of a whole program** (`Gala/M5/Prog.lean`, `ProgInst.lean`). `lowerProg K ds`: one package `main`, a `func` per function of the lowered kinds lowered by `lowerFun` with the program's own signatures (`progSigs`); functions of other signatures (`main`, unit results) are skipped (step 5 widens this). It refuses repeated names, names the lowering uses (`true`, `false`, `int`, `bool`, `string`, `init`), temporaries' names, bodies that are not `Body3`, and bodies that do not fit gc's scope: `lowerProg` checks that each body fits gc's scope (`fitsSs`, proved sound by `fitsSs_sound`) and refuses bodies that don't. `lowerProg_funRel`: the context GoCore builds from the lowered program (`buildCtx`) meets every side condition of `FunRel`, through `buildPkg`'s tables and `HashMap` lemmas (no hash is computed). `lowerProg_gc`: gc's model leaves the lowered program as written, `Order.gc p = p`. Instances: `sw` and `inc` lower to the programs whose `buildCtx` is the context 3a and 3c built by hand (`sw_buildCtx`, `inc_buildCtx`), and their `FunRel` now follows from `lowerProg_funRel`; `isEven`/`isOdd`, calling each other, lower and meet `FunRel` (`evenOdd_funRel`) and gc's order (`evenOdd_gc`); a repeated name and a function named `int` are refused (`dup_not_lowered`, `int_not_lowered`); and the fit check is not vacuous: a body reading a variable a call may change fails it (`changeable_not_fits`). `Inv3`'s hypothesis that no galaspec global is named after a lowered function stays a hypothesis (step 5). |
| `Gala.M5.swProg_gc` | **The order result on the loop** (`Gala/M5/CallOrder.lean`, `LoopInst.lean`). `Body3` and `FitsS` widen to `for {}` with no init, condition or post, `:=` and `=` with names on the left (the right side then the top-level operand list) and `continue`; the scope after `:=` binds its names, as the rewrite's does. `sw`'s loop is `Body3` and fits the scope gc's rewrite gives it, so `gc_runs3` leaves it in place (`#guard` cross-check by evaluation). A parameter the loop assigns is assigned outside every function literal, so no call can change it. |


### How much of `examples/` steps 3a to 3c lower

`formal/scripts/CallCount.lean` sorts every function of the examples' Core JSON
(`galacore`, constant-folded as galaspec runs it) by the first reason the lowering leaves
it out. Of 542 examples, 541 export, with 1,526 functions:

| Reason | Functions |
|---|---|
| no result (`main`, functions returning nothing) | 652 |
| a parameter or result not `int`, `bool` or `string` | 680 |
| parameter names Go rejects or that do not bind (`_`, `true`, `false`, repeated) | 0 |
| the body reads a package-level name (the lowering knows only locals) | 15 |
| a call names an argument or leaves a defaulted one out | 0 |
| the body calls something not lowered (another function left out, `Println`, a constructor, a lambda) | 81 |
| a `match` (step 4) | 22 |
| a `return` inside an arm of an `if` used as a value (Go's would leave the IIFE, GALA's the function) | 4 |
| looped by the transformer, refused by the loop lowering where the recursive one would not | 0 |
| another body shape (an assignment, a loop, ...: later steps) | 23 |
| excluded by the order predicate (`Body3`) | 0 |
| refused: does not fit gc's scope (`fitsSs`, step 3d) | 0 |
| a parameter a call may change (a function literal assigns it) | 0 |
| lowered, `Body3`, a self-tail-call loop (step 3c) | 6 |
| lowered, `Body3`, with an `if` used as a value or as the last value (step 3b) | 10 |
| lowered, `Body3`, otherwise | 33 |

Of the 194 functions with signatures of the three kinds, 49 lower, as after step 3b: 33
with no `if` used as a value or as the last value, 10 with one (step 3b), and 6 as
self-tail-call loops (step 3c). Step 3b counted those 6 as its own, lowered as recursion,
which is not the Go the transpiler emits for them; step 3c lowers them as its loop.
None is excluded by the order predicate. No lowered body has an IIFE that changes a variable of the
function around it (which would make the body not fit gc's scope): the only assignment is a
loop's, of its parameters, outside every function literal, and the measured count of
parameters a call may change is 0. None is refused for not fitting gc's scope: the script checks `fitsSs` in
the program of each file's lowered functions, as `lowerProg` does. Whole programs: of the 93 files with a
function of the lowered kinds, `lowerProg` (which lowers every such function or none) lowers 20.

### M3's stated gap: the subject has the static type the transformer inferred

`lowerMatch_correct` assumes the subject's value has the static type `t` the lowering was
given (`HasTy`, deep: fields and tuple elements too). It needs this because galaspec and the
transformer decide what a bare pattern name means differently: galaspec at run time, from
the value's sealed type (`classifyPatName`), the transformer statically, from the type it
inferred. They agree exactly when the value has that type. A program GALA's type checker
accepts satisfies it; galaspec has no typing judgment to prove that (T7), so it stays a
hypothesis.

These are theorems about the *spec* (and a model of one lowering into GoCore, and one
program's translation under gala2lean's prelude), not about the transpiler.

Not proved: T5–T7.

## GoCore: the Go the transpiler emits (M4)

`gocore` (`Gala/GoCore/*.lean`, `GoCoreMain.lean`) runs the transformer's actual Go
output, read by the Go AST exporter (`internal/formal/gocoreexport`), together with the
Go of the GALA packages it imports: the program's sealed types and their `Unapply`,
`Equal` and `String` methods, and the standard library's generated Go, run as the Go they
are. It is a model of the Go subset the transpiler emits, not of Go, and its fidelity to
Go is tested, not proved.

| Construct (Go spec section) | Modelled |
|---|---|
| Integer types of every width, wrapping; untyped constants exact, checked representable where typed ("Constants", "Integer overflow", "Representability") | yes |
| `+ - * / % & \| ^ &^ << >>`, unary `- + ^ !`, `&& \|\|`, comparisons; division by zero and a negative shift count panic with Go's runtime text ("Arithmetic operators", "Comparison operators") | yes |
| Variables as cells: `&x`, `*p`, pointer equality by identity, field addresses ("Address operators") | yes |
| `:=`, `=` (two phases), `op=`, `++`/`--`, `var`, blocks and shadowing ("Assignment statements", "Blocks") | yes |
| `if`, expression `switch`, type `switch`, `for` with `break`/`continue` and per-iteration loop variables ("If statements", "Switch statements", "For statements") | yes |
| Functions, methods with value and pointer receivers, method sets, method values, closures, named results ("Function declarations", "Method sets", "Function literals") | yes |
| Structs, composite literals, interface values (their interface type and dynamic value; a nil pointer inside one is not nil), type assertions ("Struct types", "Composite literals", "Interface types", "Type assertions") | yes |
| Generic functions and types, type arguments explicit (in part too) or inferred from the arguments, through pointer, slice, array, generic and function types, typed arguments before untyped constants, an untyped constant's default type for a parameter no constraint mentions and whose own has no core type, a generic defined type against a type literal (inexact unification), and a generic function passed by name to a generic call (not in a `defer` statement), inferred left to right from what the other arguments fix ("Instantiations", "Type inference"; fixture `inference_generic`). This is a subset of Go's inference: every equation GoCore uses is one Go has, so when it binds every type parameter it reaches Go's unique solution, and otherwise it stops `type-inference`, at Go's joint step (a generic function argument fixed only together with an untyped constant, `unsupported_inference_generic_ref`), at constraint type inference through a core type (`unsupported_inference`) and through methods (`unsupported_inference_method`), and at a generic function used as a value elsewhere | yes |
| Package-level variables in dependency order, then `init` functions; packages in import order ("Package initialization") | yes |
| `panic` with Go's printed text: a string, an error's `Error()`, a Stringer's `String()` ("Handling panics") | yes |
| `fmt.Println`, `Print`, `Sprint`, `Printf`, `Sprintf`, and `Errorf` without `%w` (`errors.New` of its text, run from Go's `errors.go`, so tested only by the differential run: `examples/match_type_switch.gala`, `struct_pattern_interface_subject.gala`, and through std's `recoveredError` `try_panic_non_string_value.gala`), with `%v %d %s %t %x %X %e %E %f %F %g %G %c %T %%`, and on integers, booleans and strings the flags `- + space 0 #`, a width and a precision (`fmtInteger`, `fmtS`); on floats the flags `- + space 0`, a width and a precision, the digits the exact value rounded half to even (`fmtFloat`, `strconv.FormatFloat`); `String`, `Error` and `Format` by method set; `&{...}` for a top-level pointer to a struct; no methods through unexported fields | yes (a model); a flag on a composite value, `%+v`, `%#v`, `#` and `%b %x` on floats, `*` widths and argument indexes are not; nor is `%T` of a type with an interface type with methods or a struct type with fields in it, with a local type in a type argument, or, outside type arguments, of a package whose name GoCore cannot tell from its path |
| `std.FormatStruct`, `Copy`, `Equal`, `EvalOperand`, `recoveredError` (a recovered panic's error, its stack left nil; a panic value neither an error nor a string as `fmt.Errorf("panic: %v", r)`), `Immutable.Format`, `(*panicError).Format` (hand-written, reflective) | yes (models, `libFuncs` in `Gala/GoCore/Eval.lean`) |
| Slices and arrays: slices sharing an underlying array, `len`, `cap`, `make`, `append`, `copy` (from a string too), index and slice expressions (the full `a[l:h:m]` too) with Go's runtime panic text, literals with keys, `[...]T`, elided element types; arrays compared element by element, slices only with `nil` ("Slice types", "Array types", "Index expressions", "Slice expressions", "Appending to and copying slices") | yes |
| The capacity `append` gives a growing slice, which the spec leaves open | yes: gc's (Go 1.25.5, amd64), `Gala/GoCore/GcRuntime.lean`; where gc's choice depends on escape analysis, a run that would tell the two apart ends `unsupported` (`cap-escape-dependent`, `append-escape-dependent`, `slice-escape-dependent`) |
| The order of evaluation of operands, which the spec leaves open beyond the order of calls among themselves ("Order of evaluation") | yes: gc's (Go 1.25.5), `Gala/GoCore/Hoist.lean`. Calls, builtins, slice expressions, type assertions, `[]byte(s)` and `&&`/`\|\|` are evaluated at their place, the other operands after them, as gc's order pass does; a statement where gc's choice depends on a static type (a value converted to an interface, which gc copies early or reads late by its type, where the declared types the syntax shows do not settle it), or whose place cannot hold the rewrite (a `for` condition, a `switch` case, a package-level initializer), ends `unsupported` (`eval-order-*`) when the two orders could be told apart there |
| `range` over slices, arrays (through a pointer too), strings and integers; variadic functions, `f(xs...)` ("For statements with range clause", "Passing arguments to ... parameters") | yes |
| `slices.Clip` | yes (a model) |
| `fmt.Stringer`, the defined interface type `interface { String() string }`, kept distinct from an identical literal interface ("Type identity") | yes (`libType?`) |
| `errors` (`errors.go`: `New`), `strconv` (`atoi.go`, `quote.go`, `isprint.go`: `Atoi`, `ParseInt`, `ParseUint`, `Quote` and their errors, but for a bad base or bit size, which uses `Itoa`), `unicode/utf8` (`utf8.go`: `RuneCountInString` and the rest of the file), `slices` (`sort.go`, `zsortanyfunc.go`: `SortFunc`, gc's pdqsort, so a comparator is called in Go's order, tested by `examples/sort_comparator_calls.gala` on random, reversed and already sorted input and on input with repeated keys, which go_interop's index tie-break makes distinct, so pdqsort's equal-element path (`partitionEqualCmpFunc`) is never reached from GALA; `SortStableFunc`, in the same file, runs if reached but is untested, as no GALA code reaches it), `cmp` (`cmp.go`) and `math/bits` (`bits.go`, `bits_tables.go`) | yes: Go's own source, read from the pinned toolchain's GOROOT and run as it is (`gocoreexport.RunnableStdlib`), when the program uses only names those files declare (a sort that reaches pdqsort's pattern breaking, `1 << bits.Len(...)`, stops: GoCore does not type an untyped constant shifted by a non-constant from its context); `internal/stringslite.Clone`, which their errors use, is a model (strings are immutable, so the copy is the string) |
| Local `const` declarations; a shift of an untyped constant in a constant declaration, or converted (`T(1 << s)`, which gives it the type `T`); `[]byte(s)` of a constant string (capacity its length, gc's choice) and, in a range clause, of any string; `string(b)` of bytes that are valid UTF-8 ("Constant declarations", "Operators", "Conversions to and from a string type") | yes; `[]byte(s)` of a non-constant string elsewhere (its capacity depends on escape analysis), `[]rune(s)`, `string(b)` of bytes that are not valid UTF-8 (GoCore's strings are) and a shift of an untyped constant in another context are not |
| `float32` and `float64`: untyped float constants exact (rationals), with the kind order integer, rune, floating-point; IEEE 754 arithmetic, one rounding per operation to the type's width (no fused multiply-add, a gc choice); comparisons with NaN unordered; division by zero an infinity or NaN; conversions rounded to nearest, ties to even, and to integers by truncation; `%v` as `strconv`'s shortest `'g'` ("Constants", "Numeric types", "Arithmetic operators", "Conversions") | yes; a float to an integer out of range (or an infinity or NaN), `math`, and complex numbers are not |
| `defer` and `recover`: the deferred function and arguments evaluated at the `defer`, the calls run newest first when the function returns or panics, after a `return` set the results; `recover` stops a panic only called by a deferred call itself (an exact call depth); a panic in a deferred call replaces the current one; the panic's text made when it ends the run ("Defer statements", "Handling panics") | yes; recovering a runtime error, `panic(nil)` and deferring a builtin, conversion or another package's function (`defer fmt.Println`) are not modelled |
| Maps, channels, `go`, `select`, labels, `goto`, `fallthrough`, conversions between strings and slices but `[]byte(s)` and `string(b)` (above), Go's standard library beyond the `fmt` functions, `fmt.Stringer`, `slices.Clip` and the files above | no: a run that reaches one ends `unsupported:<construct>` |

A run ends `ok`, `panic` (with Go's text), `fuel` (out of steps or depth: inconclusive),
`unsupported:<construct>`, or `stuck`, which no program Go compiles reaches: a GoCore or
exporter bug. GoCore's semantics is defined for programs `go build` accepts; on others its
answer is unspecified.

Besides the constructs in the last row, a run ends `unsupported` at:
- a failed type assertion (Go's panic text names the static interface type);
- `==` on values Go cannot compare (`uncomparable`);
- an untyped constant shifted by a non-constant count (`untyped-shift`), whose type comes
  from its context;
- an interface type that embeds another (`embedded-interface`);
- a member promoted from an embedded field (`promoted-selector`);
- a `String` or `Error` method that panics while `fmt` prints, except on a nil pointer,
  which prints `<nil>` as `fmt` does.

In a package of `quietInitPackages` (`Gala/GoCore/Run.lean`, the same list as galaspec's
`noOutputInitImports`, kept equal by `TestNoOutputInitImportsInStep`), a variable whose
initializer reaches a construct outside GoCore, such as reflection in `std/types.go`, is
left poisoned: reading it ends the run `unsupported`. This rests on the assumption galaspec
and gala2lean share, that these packages print nothing and do not panic while they
initialize. In any other package such an initializer ends the run `unsupported`.

**Tested:** `formal/tests/gocore` holds Go programs whose output, outcome and panic text
come from a real `go run` (`tests/gocore/regen.sh`). They cover:
- integer wrapping and shifts;
- method sets and `fmt`: a value-receiver `String` for `T` and `*T`, a pointer-receiver
  one only for `*T`, an unexported field;
- control flow and loop variables;
- initialization order;
- interface values: a nil pointer in an `error` that is not nil, comparison by dynamic
  type, reassignment to another dynamic type, method values, `init` functions, `<nil>` for
  a nil receiver;
- generics, two panics, fuel and an unsupported construct;
- gc's order of evaluation (`eval_order`, `eval_order2`, `eval_order3`, `eval_order4`,
  `eval_order_call`): a
  variable read after a call in the same statement changes it, nested calls, `&&` and `||`,
  `len`, slice expressions, method calls, `:=`, `var`, `op=`, `if` conditions, `return` and
  `defer` arguments, output from a call before an earlier operand's panic, and the
  operands gc reads late whatever their type (a field through a pointer parameter or a
  pointer assertion, a dereferenced receiver, a struct passed as a struct); and the type-dependent
  case that stops (`unsupported_eval_order`); and a division by zero written before a call
  of a function that prints (`eval_order_call`: gc calls first, so the output comes before
  the panic; M5 step 3a's `Body3` excludes such statements); the same before a function
  literal called at once, the transpiler's IIFE (`eval_order_iife`: the literal's body
  prints before the panic);
- the transpiler's self-tail-call loops (`tail_loop`): a sum, a gcd, a swap of
  parameters (`sw(1, 2, 2)` is `-1`; assigning in place without the temporaries would
  give `0`) and an argument whose division by a parameter panics on the last
  iteration, after each iteration's output;
- the transpiler's IIFEs (`iife_shapes`): its Go for `if` used as a value, as a `var`'s
  value, an argument, with an arm of statements, and a function's tail `if`, the Go M5
  step 3b's lowering gives for the same functions;
- string ordering (`string_order`): Go orders strings byte-wise, GoCore by code point,
  and on these strings (multi-byte ones differing in a lead or a continuation byte,
  prefixes, untyped constants, a defined string type, a sort) the two agree, as they
  must on valid UTF-8. Tested on these strings, not proved;
- local types (`local_types`): one type per declaration, whatever the call or iteration,
  and another declaration of the same name another type; in a generic function or a
  method of a generic type, one type per instantiation, whether the type mentions the
  type parameters or not, a function literal's included; a recursive type, a type that
  outlives its function in a closure, an alias, a local interface, a type switch; a
  definition naming local constants (`[n]T`); a local type shadowing a package-level type
  (with a `String` method) or a variable, a variable shadowing it in an inner block, and a
  later spec of the same declaration not yet in scope for an earlier one; and a conversion
  to a local type, which gc reads late like any conversion. A generic local type stops the
  run (`unsupported_local_generic_type`).

Where GoCore stops on the corpus, and why. Tested programs only: each is the first
construct a run reached, so a program may stop at another once it is modelled.
- **Out of reach by design:** a `range` over a map of two or more entries (Go randomizes
  the order), channels and goroutines (`time.After`, `go`), the file system and the
  environment (`os`, `io/fs`), output that depends on the operating system
  (`path/filepath`), `context`, `net/http`, and reflection (`encoding/json`, and the
  reflective fallback of std's `Copy` and `Equal`).
- **Deferred, reachable but costly:** `strings` and `bytes` (their Go source calls
  `internal/bytealg`, written in assembly, and `unsafe` for `strings.Builder`; models of
  both would be trusted), `time` (a large package, with time zones), complex numbers
  (arithmetic and printing), and Go library packages used by a single program each
  (`net/url`, `net`, `regexp`, `sync`, `sort`, `math`, `crypto/sha256`, `testing`,
  `fmt.Fprintf` to an `io.Writer`); a selector promoted through an embedded field.
- **Deferred, reachable but little gained:** items cheap on their own, whose programs
  stop again right after them:
  - generic local types (`type Box[T any] ...` in a function body), which gc 1.25.5
    accepts; the run stops at the declaration (one program);
  - `strconv.FormatInt` (`itoa.go` would run as it is): its only program also uses
    `math/big`;
  - `slices.IndexFunc`: `slices.go` imports `unsafe`, which the runnable-file guard
    refuses;
  - a recovered runtime error (`recover-runtime-error`): its programs then read the
    panic's stack (`PanicStack`, through `runtime.CallersFrames`, which GoCore does not
    run);
  - a `String` method on a pointer receiver and `fmt.Formatter` (`pointer-string`,
    `fmt.Formatter`): their programs also print with `%#v`, which is not modelled;
  - `runtime.Compiler`: one program; a model of the constant would add to what is
    trusted for it alone.

The differential run (`internal/formal/difftest`) runs every compiled program's Go through
GoCore and compares it with `go run`; its results are in the "GoCore" part of the
generated section below.

## gala2lean

[gala2lean](README.md#gala2lean-a-third-oracle) translates the programs it covers to
Lean definitions. It translates subset S1 and the S2 constructs galaspec models (as
galaspec models them, except as listed below):

- an operator expression of literals only, a Go untyped constant: each maximal such tree
  is evaluated before translation with Go's own constant arithmetic (`go/constant`, as
  Go's type checker does, so `(1 << 70) >> 68` is 4) and written as its value. What
  Go rejects at compile time is reported (`untyped-constant`, #1237), never wrapped:
  an integer result outside int64, an integer of more than 512 bits at any step (the
  type checker's limit), a division by zero, a negative shift count or one above
  Go's bound. A name is never a constant: a `val` lowers to a Go variable;
- interpolated strings `s"..."`, each part printed as `Println` prints it;
- named arguments and default values: arguments in the order written, then each
  left-out default in parameter order, in the callee's scope; package-level
  initialization follows the defaults a call leaves out, and only those;
- lambdas, function types and functions as values. A `var` a lambda refers to lives in a
  reference cell in the program state (one heap per type), so the lambda and the code
  around it share it, as GALA's capture by reference says; a `func() T` parameter turns
  a plain argument into a thunk;
- a placeholder lambda of one `_`, `rs.Map(_.Lo)`, as the lambda the transformer makes
  of it (`(p) => p.Lo`), where the argument is built only of the `_`, literals, names,
  operators, selects, interpolated strings of these and calls on the `_` with literal or
  name arguments (`_.Contains(1)`), and its call is not itself inside a call argument, up
  to the nearest enclosing lambda, case clause or top-level declaration: then no other
  slot can take the `_`, and the transformer's rewrite reaches it. Every other `_` is
  reported (`placeholder-lambda`): two or more in one argument, one in an if or match
  expression, a block or an interpolated string, and one for a slot that is not a
  function type of one parameter;
- `Some`, `None`, `Left` and `Right`, as the prelude's `GOption` and `GEither`, with
  their patterns, and the methods std declares for them (`IsDefined`, `IsEmpty`, `Get`,
  `GetOrElse`, `OrElse`, `OnSome`, `OnNone`, `ForEach`, `Map`, `FlatMap`, `Filter`,
  `Exists`, `ForAll`; `IsLeft`, `IsRight`, `GetLeft`, `GetRight`, `Swap`, `OnRight`,
  `OnLeft`, `Fold`, `Map`, `FlatMap`), each modelled in the prelude after the std code it
  cites: the receiver, then the arguments, as Go evaluates a call, then the method, which
  calls a function argument only where std does; where std panics (`Get` on `None`,
  `GetLeft` on a `Right`, `GetRight` on a `Left`) the model panics with the same class,
  `other`. Each has a case in `internal/formal/gala2lean/testdata` whose output the gala
  CLI printed; another method of the two (`Hash`, ...) is reported (`option-method`);
- methods with a value receiver `(c T)`, a plain function `T.m` taking the receiver
  first, and with a pointer receiver, `(c *T)` or `(c Ptr[T])` (the same Go receiver;
  `Ptr[T]` only where the package cannot declare a `Ptr` of its own, which a type
  declaration Core JSON does not name might be), by state passing: `T.m` takes the value
  and returns its result with the updated value, which the caller writes back to the
  `var` it called the method on, after the arguments are evaluated and the method
  returns (a method that panics writes nothing back; no output is lost, since a panic
  ends the run); the `Copy` (with named arguments) and `Equal` GALA generates for a
  struct, and the `isC()` it generates for each case `C` of a non-generic sealed type
  (a `match` on the case), unless the type declares that method itself;
- `inout` receivers `(inout c T)`, as pointer receivers (they lower to one), whose
  body may also read and replace the receiver whole; and `inout` parameters of
  top-level functions, by the same state passing: a function with `inout` parameters
  returns their final values beside its result, and a call evaluates the other
  arguments in the order written, then reads each lent place (`&x`, `&x.f`, or an
  `inout` parameter of the caller), so the callee sees what the arguments wrote
  (`f(&x, g(&x))`), and after the call writes each final value back;
- generic functions, structs and sealed types, as Lean definitions and types with their
  type parameters as parameters, whose type arguments Lean infers, or takes as written
  where a call gives them (`id[int](x)`, `None[string]()`, `EmptyArray[int]()`: Core
  JSON's `bracket`, read as types when the callee is a generic function or constructor
  or a modelled std constructor, else reported as `index`). A type parameter's
  values print as their type prints (`GShow`), and under `comparable` compare with the
  `BEq` derived for their type, which compares as GALA's generated `Equal` does: field
  by field, and case then fields for a sealed value. `Option`, `Either`, `Tuple`,
  `Tuple3` and `Tuple4` in types are the prelude's `GOption`, `GEither` and `T2` to
  `T4`;
- std's `When`, `Unless`, `Tuple`, `Tuple3` and `Tuple4`, `collection_immutable`'s
  `ArrayTabulate` and `ArrayFill`, and Go's `int(x)` and `int64(x)` of a value that is
  already an `int` or `int64` (the same value: both are 64 bits, as galaspec takes Go's
  `int` to be; any other conversion is reported, `conversion`), each only where the name
  resolves to it: the program declares no such name, the file the name is in
  dot-imports no package but std and `collection_immutable` (the mutable collections
  have their own `ArrayTabulate`), and, for a name of `collection_immutable`
  (`ArrayOf`, `ArrayTabulate`, `Array`, ...), that file dot-imports it. A dot import
  counts only in its own file, as in the Go the transpiler writes; a library name or
  collections type a file's imports rule out is reported;
- `Array` and `List` from the dot-imported `collection_immutable`, as the prelude's
  `GArray` and `GList`, with the functions the prelude models (`ArrayOf`, `ListOf`,
  `EmptyArray`, `EmptyList`, `Size`, `Get`, `Map`, `Filter`, `FoldLeft`, `FoldRight`,
  `Reduce`, `Partition`, `MkString`, `FlatMap`, `Collect`, `Zip`, `TakeWhile`,
  `DropWhile`, `Distinct`, `FindLast`, `Grouped`, ...; `Prelude.lean` lists them), each
  after the std code it cites: the functions a call passes run in the order std runs
  them, with the same early exits, and where std panics (`Get` out of range, `Reduce`
  of an empty sequence, `Grouped` of a size below 1) the model panics with the same
  class, `other`. Each function has a case in
  `internal/formal/gala2lean/testdata` whose output the gala CLI printed;
- `SortWith(less)` on an `Array` or a `List`, where `less` is a lambda that can have no
  effect and cannot panic: its body is literals, names (read only), field reads, unary
  `!`, `-`, `+` and `^`, comparisons, `&&`, `||`, wrapping `+`, `-`, `*`, the bit
  operators, `/` and `%` by a non-zero integer literal, `if` expressions and tuples of
  these (no call, index, shift or interpolation; else `sort-less-not-pure`; a comparison
  that would call a user `Equal` is reported, `custom-equal`, as anywhere). std sorts the
  positions with gc's sort under `less`, ties broken by position; the model sorts them by
  the same order and checks it is a strict total order on every pair, so any correct
  sort, gc's included, gives its result (trusted: gc's sort sorts). Where it is not (a
  `less` that is not consistent), the Lean run stops, `unsupported:sort-inconsistent-less`;
- a function's or method's variadic parameter `xs ...T`, as a Lean `List` of its
  arguments: a call passes the trailing arguments, evaluated in the order written with
  the others, as one list (`[]` for none, or when a call with named arguments leaves it
  out; `xs = v` is one value), and a spread `f(xs...)` passes a variadic parameter on
  whole. Its only uses that translate are a spread into a declared variadic function,
  `ArrayOf` or `ListOf`, and `xs.Size()` (Go's `len`); any other use is reported
  (`slice-value`), so no slice is written, compared or printed. Reported too: a spread
  of anything else or beside other arguments, a named spread (`spread-arg`), a variadic
  function or method used as a value (`variadic-func-value`), and a function with both
  `inout` and variadic parameters (`variadic-inout`);
- in the items below, a qualifier such as `fmt` or `strings` means Go's package only
  where the file the call is in imports that package by that name, imports nothing
  else by it, and binds that name nowhere (no val, var, parameter, receiver or pattern
  binder in the file), and the program declares nothing of that name: imports resolve
  per file;
- Go's `fmt.Println` and `fmt.Print` called as statements with `int`, `string` and
  `bool` operands, where `fmt` resolves to Go's package: Go's `%v` of those prints as
  GALA's `Println` and `Print` do, with the same spacing (a trusted model, tested in
  `internal/formal/gala2lean/testdata/go_fmt.gala`). Any other operand
  (`fmt-operand`), or the call used as a value (`fmt-value`), is reported;
- Go's `strings.HasPrefix`, `HasSuffix`, `TrimPrefix`, `TrimSuffix`, `Contains`,
  `ReplaceAll` and `Repeat` (its negative-count, overflow and allocation panics, class `other`),
  and `strconv.Itoa`, where the package name resolves to Go's: trusted models in the
  prelude, after the Go 1.25.5 source, that match characters where Go matches bytes,
  which agree on the valid UTF-8 every translated string is (a string literal that is
  not valid UTF-8 is reported, `invalid-utf8`). Tested in
  `internal/formal/gala2lean/testdata/go_strings*.gala`;
- `strings.NewReplacer(oldnew...).Replace(s)`, built and used at once, with the old/new
  strings written out or spread from a variadic parameter or an `Array`'s or `List`'s
  `ToGoSlice()`: the earliest pair whose old string starts at a position wins, as Go's
  four replacer algorithms do on valid UTF-8; an odd count panics, class `other`. Where an
  empty old string's match is followed by a character beyond ASCII that no other
  old string takes, the Lean run stops,
  `unsupported:replacer-empty-old` (Go would write inside a character), counted as
  unsupported. Tested in `internal/formal/gala2lean/testdata/go_replacer*.gala`;
- the packages of the program's module it imports (`gala2lean -module`): each one's
  declarations under a Lean namespace of its own, translated where the program uses
  them; `pkg.X` means package `pkg`'s `X` unless a local hides `pkg`. Every
  package-level variable of every package it imports, directly or not, is
  initialized, used or not, in Go's order (every import of a compiled program
  initializes its package: an import its source does not use is rejected,
  GALA-E0120, and a blank import `import _ "pkg"` is there for its
  initialization): packages sorted by import path, each
  once the packages it imports are, and within a package as Go orders it. A function
  counts as translated (`-stats`) only when what it uses of other packages
  translates.

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
  default case (`sealed-zero-value`); a `var x T` with no value whose default value takes a
  declared field default (`field-default-value`); a package-level variable whose type is not plain to
  see from its initializer (`untyped-global`); local functions and structs;
- a call of a pointer method on a place another path can reach while the method runs
  (a package-level `var`, a `var` a lambda captures), or a pointer method that uses its
  receiver other than through a field or a method call (`ptr-escape`); a pointer method
  on a value that is not a `var` (`pointer-method-on-value`); likewise for `inout`:
  lending such a place, lending overlapping places in one call, or an `inout`
  parameter or receiver a lambda refers to (`ptr-escape`), an `inout` argument that
  is not `&` of a `var` (`inout-arg`); `inout` parameters of methods
  (`inout-method-param`) and lambdas (`inout-lambda-param`) and in function types
  (`param-modifier`), a function with `inout` parameters as a value
  (`inout-func-value`), and `&x` anywhere but an `inout` argument (`address-of`);
  printing or comparing a value of a type with its own `String`, `Error`, `Format`, `GoString` or `Equal`, seen
  or not (a method with a generic receiver), or holding one
  (`custom-string`, `custom-equal`); in a program with a method whose receiver type is
  not plain to see, printing or comparing anything but `int`, `string` and `bool`
  (`method`);
- a type argument that is or holds a function (`func-type-arg`), which Go prints as an
  address; printing or comparing a value of a type parameter in a program where some
  type has its own `String` or `Equal`, holds a function, or has a method gala2lean does
  not see (`custom-string`, `custom-equal`, `print-pointer`, `method`), since the type
  argument might be that type; the zero value of a type parameter (`type-param-zero`);
  a value of a type parameter kept in the program state (`generic-in-state`); a
  constraint other than `any` and `comparable` (`type-constraint`); a generic function
  used as a value (`generic-func-value`, `index` for `id[int]` without a call); a
  bracket before a call that is an index (`fs[i](x)`) or whose items are not types
  (`index`); a type nested in itself through a generic type,
  `Next Option[Node]` (`nested-recursive-type`); printing a value whose raw text (a
  lower-case field `fmt` prints as a Go struct) reaches a generic type, a type
  parameter, `Option` or a tuple (`print-raw-generic`); methods of generic types;
- `HashMap` and `HashSet` (`hash-order`), whose printing, iteration and folding follow
  their hash trie's order, which the prelude does not model; a function of `Array` or
  `List` the prelude does not model (`collection-method`); comparing values that hold
  a function (`func-in-equal`); an import, in any file it translates, reached or
  not, of a package it is not given (`import`; `blank-import` for `import _ "pkg"`),
  whose initializers Go runs and it cannot, except an import of one of the packages
  galaspec lists (`Json.noOutputInitImports`, exact paths, the same list and the same
  assumption: they print nothing while they initialize), which is reported only where
  the program uses a name of it outside what gala2lean models; a type of a package
  outside the module (`qualified-type`), a dot import of a package of the module (`dot-import-module`),
  an `init` function (`init-func`).

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
| AGREE | 29 | 1 | 75 | 3 |
| DISAGREE | 0 | 0 | 0 | 0 |
| UNSUPPORTED | 480 | 34 | 434 | 32 |
| INCONCLUSIVE | 0 | 0 | 0 | 0 |

Examples that run wholly inside the subset and agree with the spec: **29** (ratchet baseline 29).
Examples gala2lean translates whose Lean run agrees with Go: **75** (ratchet baseline 75).

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
| `file` | yes | yes: 30 agree | no | translated: 78 agree |
| `import` | yes | no: 1 agree | no | translated: 29 agree |
| `val` | yes | yes: 12 agree | no | translated: 53 agree |
| `var` | yes | yes: 4 agree | no | translated: 7 agree |
| `func` | yes | yes: 30 agree | no | translated: 78 agree |
| `receiver` | not seen | no | no | translated: 7 agree |
| `type-param` | not seen | no | no | translated: 16 agree |
| `param` | yes | yes: 27 agree | no | translated: 65 agree |
| `struct` | yes | yes: 4 agree | no | translated: 20 agree |
| `field` | yes | yes: 8 agree | no | translated: 40 agree |
| `sealed` | yes | yes: 8 agree | no | translated: 27 agree |
| `variant` | yes | yes: 8 agree | no | translated: 27 agree |
| `type-name` | yes | yes: 28 agree | no | translated: 71 agree |
| `block` | yes | yes: 30 agree | no | translated: 78 agree |
| `if` | yes | yes: 7 agree | no | translated: 11 agree |
| `return` | yes | yes: 5 agree | no | translated: 19 agree |
| `assign` | yes | yes: 4 agree | no | translated: 7 agree |
| `incdec` | not seen | no | no | not translated |
| `int` | yes | yes: 27 agree | no | translated: 64 agree |
| `bool` | yes | yes: 8 agree | no | translated: 16 agree |
| `string` | yes | yes: 22 agree | no | translated: 62 agree |
| `name` | yes | yes: 30 agree | no | translated: 78 agree |
| `unary` | yes | yes: 6 agree | no | translated: 13 agree |
| `address-of` | not seen | no | no | not translated |
| `binary` | yes | yes: 23 agree | no | translated: 48 agree |
| `call` | yes | yes: 30 agree | no | translated: 78 agree |
| `select` | yes | yes: 4 agree | no | translated: 41 agree |
| `paren` | yes | no: 2 agree | no | translated: 4 agree |
| `tuple` | not seen | no | no | translated: 5 agree |
| `if-expr` | yes | yes: 10 agree | no | translated: 13 agree |
| `match` | yes | yes: 14 agree | no | translated: 37 agree |
| `case` | yes | yes: 14 agree | no | translated: 37 agree |
| `pat-wild` | yes | yes: 8 agree | no | translated: 21 agree |
| `pat-name` | yes | yes: 8 agree | no | translated: 28 agree |
| `pat-lit` | yes | yes: 8 agree | no | translated: 14 agree |
| `pat-tuple` | not seen | no | no | translated: 1 agree |
| `pat-extract` | yes | yes: 4 agree | no | translated: 25 agree |
| `named-arg` | yes | yes: 5 agree | no | translated: 17 agree |
| `spread` | not seen | no | no | translated: 1 agree |
| `lambda` | yes | yes: 5 agree | no | translated: 21 agree |
| `type-func` | yes | yes: 3 agree | no | translated: 7 agree |
| `interp` | yes | yes: 14 agree | no | translated: 36 agree |
| `interp-text` | yes | yes: 14 agree | no | translated: 36 agree |

### Outside the subset

The constructs galaspec stopped at, by how many programs it stopped at each (40 kinds, the top 20 shown):

| Construct | Programs |
|---|---|
| `import` | 286 |
| `method-call` | 54 |
| `generic-type` | 42 |
| `index` | 38 |
| `generic-decl` | 15 |
| `address-of` | 10 |
| `unresolved-name` | 9 |
| `composite-literal` | 8 |
| `float-literal` | 7 |
| `method` | 6 |
| `stuck` | 3 |
| `by-name-arg` | 2 |
| `char-literal` | 2 |
| `for` | 2 |
| `format-string` | 2 |
| `type-Value` | 2 |
| `type-any` | 2 |
| `typed-pattern` | 2 |
| `blank-import` | 1 |
| `defined-type` | 1 |

### Outside what gala2lean translates

The first construct gala2lean did not translate in each program, by how many programs (94 kinds, the top 20 shown):

| Construct | Programs |
|---|---|
| `import` | 106 |
| `generic-type` | 35 |
| `index` | 31 |
| `qualified-type` | 24 |
| `pointer-type` | 14 |
| `type-float64` | 14 |
| `fmt-operand` | 13 |
| `method-call` | 13 |
| `type-error` | 10 |
| `for` | 8 |
| `slice-type` | 8 |
| `bind` | 7 |
| `partial-function` | 7 |
| `return-inside-expression` | 7 |
| `collection-method` | 6 |
| `composite-literal` | 6 |
| `extractor` | 6 |
| `go-results` | 6 |
| `untyped-global` | 6 |
| `expression-pattern` | 5 |

### Known disagreements

None.
### GoCore: the Go the transformer emitted

GoCore (`formal/Gala/GoCore/`), the Lean model of the Go the transpiler emits, runs
each compiled program's actual Go output, read back by the Go AST exporter, and is
compared with `go run` of the same Go: stdout, and how it ends, a panic by the text Go
prints. These measure GoCore's fidelity to Go, tested, not proved. RESOURCE is a run
Go aborted for lack of stack or memory, or that ran past its time limit: reported,
never compared. A GoCore DISAGREE is a GoCore or exporter bug and fails the run.

| GoCore against go run | Examples | Generated programs |
|---|---|---|
| AGREE | 354 | 34 |
| DISAGREE | 0 | 0 |
| UNSUPPORTED | 74 | 1 |
| INCONCLUSIVE | 0 | 0 |
| RESOURCE | 0 | 0 |

Programs GoCore agrees with go run on: **388** (ratchet baseline 388).
Examples where GoCore agrees with go run and galaspec agrees with go run: **29** (ratchet baseline 29).

They are: `examples/complex.gala`, `examples/composite_lit_if_cond.gala`, `examples/composite_lit_if_expr_cond.gala`, `examples/constant_expressions.gala`, `examples/default_params.gala`, `examples/default_params_expr.gala`, `examples/hex_literals.gala`, `examples/if_expr_trailing_match.gala`, `examples/if_expression_block.gala`, `examples/interpolation_percent_text.gala`, `examples/lambda_explicit_ret_block.gala`, `examples/lambda_explicit_return_type.gala`, `examples/match_nested_subject_unread.gala`, `examples/match_stmt_guard_dispatch.gala`, `examples/match_stmt_mixed_void.gala`, `examples/match_stmt_true_dangle.gala`, `examples/match_void_dispatch.gala`, `examples/named_args.gala`, `examples/sealed_case_field_defaults.gala`, `examples/sealed_func_field_string.gala`, `examples/sealed_widen_call_arg.gala`, `examples/semicolon_statement_separator.gala`, `examples/shadowed_function.gala`, `examples/tco_deep_sum.gala`, `examples/tco_factorial.gala`, `examples/tco_match_self_call.gala`, `examples/trailing_comma_params.gala`, `examples/typed_val_lambda_inference.gala`, `examples/void_expression_function.gala`.

The construct GoCore stopped at in each program it did not run to the end, by how many programs (59 kinds, the top 20 shown):

| Construct | Programs |
|---|---|
| `recover-runtime-error` | 3 |
| `fmt.Formatter` | 2 |
| `io.Reader` | 2 |
| `map-range-order` | 2 |
| `net/url.URL` | 2 |
| `net/url.Values` | 2 |
| `os.TempDir` | 2 |
| `regexp.MustCompile` | 2 |
| `slice-of-string` | 2 |
| `std.Copy-reflective` | 2 |
| `strings.ReplaceAll` | 2 |
| `strings.ToUpper` | 2 |
| `sync.OnceValues` | 2 |
| `time.After` | 2 |
| `type-complex64` | 2 |
| `bytes.Buffer` | 1 |
| `bytes.NewBufferString` | 1 |
| `context.Background` | 1 |
| `crypto/sha256.New` | 1 |
| `dot-import-math` | 1 |
<!-- difftest:end -->

<!-- ownership:begin -->
## Ownership: why pointers are not unique

Generated by `bazel run //internal/formal/ownershipcov/update`; do not edit by hand.
It is the output of phase 1 of [the ownership analysis](../docs/OWNERSHIP.MD#what-the-ide-and-gala-doc-show) (inferred
uniqueness, `internal/transpiler/ownership`) over the repository's GALA code. A pointer
binding (a `Ptr`/`*T` receiver or parameter, or a local `val` bound to a pointer) is
**unique** when it is of fresh origin, only ever lent downward, never reaches Go, and points
at an owned-shaped value. Phase 1 is what the compiler's checks rely on.

The rows marked *phase 3* are analysis only, not enforced: there the analysis treats a
closure passed to a non-escaping parameter (one the callee only calls during the call) as
borrowing the pointers it uses, where phase 1 counts a capture. These are analysis
results, not proofs.

| | Libraries | Examples | 
|---|---|---|
| Functions and methods | 1758 | 1787 | 
| …with a pointer binding | 584 | 203 | 
| …whose pointer bindings are all unique | 0 (0.0%) | 39 (19.2%) | 
| Pointer bindings | 834 | 271 | 
| …receivers unique | 0 of 467 | 10 of 69 | 
| …params unique | 0 of 90 | 27 of 89 | 
| …locals unique | 0 of 277 | 8 of 113 | 
| *Phase 3* (analysis only, not enforced): functions whose pointer bindings are all unique | 0 of 584 (0.0%) | 39 of 203 (19.2%) | 
| *Phase 3*: pointer bindings unique | 0 of 834 | 45 of 271 | 
| *Phase 3*: …of which a closure passed to a non-escaping parameter uses | 0 | 0 | 
| Packages that did not analyze | 0 | 2 | 

### Reasons

Each binding that is not unique is counted once under every reason it has. The first
location is where the analysis first met the reason.

| Reason | Meaning | Libraries | Examples | First location |
|---|---|---|---|---|
| `stored` | stored in a field, constructor, literal, tuple or Option | 18 | 5 | `collection_mutable/array.gala:431` |
| `collection` | aliased through a collection | 24 | 7 | `collection_mutable/array.gala:387` |
| `aliased` | bound to another name | 13 | 1 | `collection_immutable/array.gala:422` |
| `returned` | returned while not fresh | 75 | 5 | `collection_immutable/array.gala:499` |
| `captured` | captured by a closure, method value, by-name argument or bind | 116 | 18 | `collection_immutable/array.gala:638` |
| `passed-to-go` | it, or a reference inside its pointee, flows into Go | 318 | 45 | `collection_immutable/array.gala:184` |
| `compared` | compared or matched | 1 | 13 | `std/ptr.gala:14` |
| `passed-twice` | two arguments of one call reach it | 0 | 1 | `examples/inout_interfaces.gala:37` |
| `escaping-callee` | lent to a parameter or receiver that escapes | 295 | 77 | `collection_immutable/array.gala:98` |
| `unknown-callee` | used in a call without a summary | 22 | 15 | `collection_mutable/array.gala:106` |
| `rebound` | its name is bound more than once | 20 | 0 | `collection_immutable/treemap.gala:329` |
| `other` | another use (reassigned, indexed, address taken, an operand) | 26 | 2 | `collection_immutable/array.gala:332` |
| `origin` | not of fresh origin | 282 | 67 | `collection_immutable/array.gala:96` |
| `from-go` | received from Go | 497 | 78 | `collection_immutable/array.gala:183` |
| `shape` | the pointee is not owned-shaped | 834 | 153 | `collection_immutable/array.gala:96` |
| `nullable` | a `*T` that may be nil | 11 | 87 | `collection_immutable/array.gala:422` |

### Where pointers reach Go

The Go functions, methods and conversions that `passed-to-go` bindings reach, by how many
bindings reach each (the 15 most frequent).

| Go side | Bindings |
|---|---|
| `passed-to-go:a reference inside it` | 159 |
| `passed-to-go:a reference inside it, passed to the function value f` | 48 |
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
