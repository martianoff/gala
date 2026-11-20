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
| Package-level `val`/`var`, initialized in Go's dependency order (a declaration with no value depends on what its type's declared field defaults refer to) | yes | no | no |
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
| S2: named arguments and default values of parameters, struct fields and case fields: arguments evaluated in the order written, then passed in parameter order; each left-out parameter's default evaluated after them, in parameter order, in the callee's scope (a function's default sees the parameters before it, a field's default no field) | yes | no | the defaults phase (T4): it does not read the caller's locals, and each default's frame binds exactly the earlier parameters; the order: written arguments, then defaults in parameter order (T3c) |

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
| `Gala.Spec.bindDefaults_callerLocals` | **T4, default arguments.** The defaults phase of a call (`bindDefaults`, the second half of `bindArgs`: each parameter the call leaves out takes its default, evaluated in the callee's scope) does not read the caller's locals. Run with any other locals, it gives the same values or the same `Stop` and leaves the same output, heap, package-level variables and steps. When it succeeds, the caller's locals are back as they were. This covers a function's defaults and a field's (`Gala/Defaults.lean`). It failed before galaspec resolved a by-name default's bare name in the default's own frame (fixture `default_byname`). Not covered: that a call's written arguments are evaluated first, in the caller's scope, which is `bindArgs`'s definition (an ordering statement belongs to T3). Also not covered: that the caller's locals come back after a panic inside a default, which no run can observe, since nothing catches a panic. |
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
| `Gala.M5.expr_correct` | **M5, step 2a: call-free expressions against GoCore** (`Gala/M5/Expr.lean`). The reference lowering of a call-free expression into GoCore for whole programs (`Gala/GoCore/*.lean`) is correct with respect to galaspec, in GoCore's order of evaluation (left to right, which is gc's for operands that are not calls). The lowering is type-directed: it is given each local's kind (`int`, `bool`, `string`) and accepts an operator only on the kinds Go type-checks it on. For an expression it accepts and with no constant operator node (what galaspec's constant folding leaves), from related states (`LocRel`: each local the lowering knows is bound on both sides to related values of its kind, GoCore's typed; `Predeclared`: nothing shadows `true` and `false`), galaspec's state is unchanged apart from steps and, at every depth from `gdepth e` on, GoCore's evaluation gives a related value (`VRel`: the same `int`, `bool` or `string`, typed or an untyped constant) of the expression's kind, or the same run-time panic, leaving GoCore's state as it was. Nothing is claimed when galaspec runs out of fuel or stops `stuck` or `unsupported`. Covered: `int`, `bool` and `string` literals (a negative one as Go's `-` of its magnitude), locals, parentheses, unary `-` `+` `^` `!`, `+ - * / %` and `< <= > >=` on `int`s, `==` and `!=` on one kind, string `+`, `&&`, `||`. Not covered yet: shifts, the bitwise operators, string ordering, and everything with a call (later M5 steps). An instance in the same file meets every hypothesis, on `x + 1`. |

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

## GoCore: the Go the transpiler emits (M4, in progress)

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
| Generic functions and types, type arguments explicit (in part too) or inferred from the arguments, through pointer, slice, array, generic and function types, typed arguments before untyped constants ("Instantiations", "Type inference"); a generic function used as a value without its type arguments, which come from its context, is not modelled (`type-inference`) | yes |
| Package-level variables in dependency order, then `init` functions; packages in import order ("Package initialization") | yes |
| `panic` with Go's printed text: a string, an error's `Error()`, a Stringer's `String()` ("Handling panics") | yes |
| `fmt.Println`, `Print`, `Sprint`, `Printf`, `Sprintf` with `%v %d %s %t %x %X %e %E %f %F %g %G %%`, and on integers, booleans and strings the flags `- + space 0 #`, a width and a precision (`fmtInteger`, `fmtS`); on floats the flags `- + space 0`, a width and a precision, the digits the exact value rounded half to even (`fmtFloat`, `strconv.FormatFloat`); `String`, `Error` and `Format` by method set; `&{...}` for a top-level pointer to a struct; no methods through unexported fields | yes (a model); a flag on a composite value, `%+v`, `%#v`, `#` and `%b %x` on floats, `*` widths and argument indexes are not |
| `std.FormatStruct`, `Copy`, `Equal`, `EvalOperand`, `recoveredError` (a recovered panic's error, its stack left nil; a panic value neither an error nor a string is not), `Immutable.Format`, `(*panicError).Format` (hand-written, reflective) | yes (models, `libFuncs` in `Gala/GoCore/Eval.lean`) |
| Slices and arrays: slices sharing an underlying array, `len`, `cap`, `make`, `append`, `copy` (from a string too), index and slice expressions (the full `a[l:h:m]` too) with Go's runtime panic text, literals with keys, `[...]T`, elided element types; arrays compared element by element, slices only with `nil` ("Slice types", "Array types", "Index expressions", "Slice expressions", "Appending to and copying slices") | yes |
| The capacity `append` gives a growing slice, which the spec leaves open | yes: gc's (Go 1.25.5, amd64), `Gala/GoCore/GcRuntime.lean`; where gc's choice depends on escape analysis, a run that would tell the two apart ends `unsupported` (`cap-escape-dependent`, `append-escape-dependent`, `slice-escape-dependent`) |
| The order of evaluation of operands, which the spec leaves open beyond the order of calls among themselves ("Order of evaluation") | yes: gc's (Go 1.25.5), `Gala/GoCore/Hoist.lean`. Calls, builtins, slice expressions, type assertions, `[]byte(s)` and `&&`/`\|\|` are evaluated at their place, the other operands after them, as gc's order pass does; a statement where gc's choice depends on a static type (a value converted to an interface, which gc copies early or reads late by its type, where the declared types the syntax shows do not settle it), or whose place cannot hold the rewrite (a `for` condition, a `switch` case, a package-level initializer), ends `unsupported` (`eval-order-*`) when the two orders could be told apart there |
| `range` over slices, arrays (through a pointer too), strings and integers; variadic functions, `f(xs...)` ("For statements with range clause", "Passing arguments to ... parameters") | yes |
| `slices.Clip` | yes (a model) |
| `fmt.Stringer`, the defined interface type `interface { String() string }`, kept distinct from an identical literal interface ("Type identity") | yes (`libType?`) |
| `errors` (`errors.go`: `New`), `strconv` (`atoi.go`, `quote.go`, `isprint.go`: `Atoi`, `ParseInt`, `ParseUint`, `Quote` and their errors, but for a bad base or bit size, which uses `Itoa`) and `unicode/utf8` (`utf8.go`: `RuneCountInString` and the rest of the file) | yes: Go's own source, read from the pinned toolchain's GOROOT and run as it is (`gocoreexport.RunnableStdlib`), when the program uses only names those files declare; `internal/stringslite.Clone`, which their errors use, is a model (strings are immutable, so the copy is the string) |
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
- gc's order of evaluation (`eval_order`, `eval_order2`, `eval_order3`, `eval_order4`): a
  variable read after a call in the same statement changes it, nested calls, `&&` and `||`,
  `len`, slice expressions, method calls, `:=`, `var`, `op=`, `if` conditions, `return` and
  `defer` arguments, output from a call before an earlier operand's panic, and the
  operands gc reads late whatever their type (a field through a pointer parameter or a
  pointer assertion, a dereferenced receiver, a struct passed as a struct); and the type-dependent
  case that stops (`unsupported_eval_order`).

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
| UNSUPPORTED | 478 | 34 | 432 | 32 |
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
| `import` | 285 |
| `method-call` | 53 |
| `generic-type` | 41 |
| `index` | 38 |
| `generic-decl` | 15 |
| `address-of` | 10 |
| `unresolved-name` | 9 |
| `composite-literal` | 8 |
| `float-literal` | 7 |
| `method` | 6 |
| `stuck` | 4 |
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
| `expression-pattern` | 5 |
| `float-literal` | 5 |

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
| AGREE | 328 | 31 |
| DISAGREE | 0 | 0 |
| UNSUPPORTED | 86 | 4 |
| INCONCLUSIVE | 0 | 0 |
| RESOURCE | 0 | 0 |

Programs GoCore agrees with go run on: **359** (ratchet baseline 359).
Examples where GoCore agrees with go run and galaspec agrees with go run: **29** (ratchet baseline 29).

They are: `examples/complex.gala`, `examples/composite_lit_if_cond.gala`, `examples/composite_lit_if_expr_cond.gala`, `examples/constant_expressions.gala`, `examples/default_params.gala`, `examples/default_params_expr.gala`, `examples/hex_literals.gala`, `examples/if_expr_trailing_match.gala`, `examples/if_expression_block.gala`, `examples/interpolation_percent_text.gala`, `examples/lambda_explicit_ret_block.gala`, `examples/lambda_explicit_return_type.gala`, `examples/match_nested_subject_unread.gala`, `examples/match_stmt_guard_dispatch.gala`, `examples/match_stmt_mixed_void.gala`, `examples/match_stmt_true_dangle.gala`, `examples/match_void_dispatch.gala`, `examples/named_args.gala`, `examples/sealed_case_field_defaults.gala`, `examples/sealed_func_field_string.gala`, `examples/sealed_widen_call_arg.gala`, `examples/semicolon_statement_separator.gala`, `examples/shadowed_function.gala`, `examples/tco_deep_sum.gala`, `examples/tco_factorial.gala`, `examples/tco_match_self_call.gala`, `examples/trailing_comma_params.gala`, `examples/typed_val_lambda_inference.gala`, `examples/void_expression_function.gala`.

The construct GoCore stopped at in each program it did not run to the end, by how many programs (58 kinds, the top 20 shown):

| Construct | Programs |
|---|---|
| `map-type` | 8 |
| `slices.SortFunc` | 6 |
| `local-type` | 5 |
| `recover-runtime-error` | 3 |
| `type-inference` | 3 |
| `fmt-verb-T` | 2 |
| `fmt.Errorf` | 2 |
| `fmt.Formatter` | 2 |
| `io.Reader` | 2 |
| `net/url.URL` | 2 |
| `os.TempDir` | 2 |
| `regexp.MustCompile` | 2 |
| `slice-of-string` | 2 |
| `std.Copy-reflective` | 2 |
| `strings.ToUpper` | 2 |
| `sync.OnceValues` | 2 |
| `type-complex64` | 2 |
| `bytes.Buffer` | 1 |
| `bytes.NewBufferString` | 1 |
| `context.Background` | 1 |
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
