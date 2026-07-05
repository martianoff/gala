# GALA Type Inference and Unwrapping

GALA implements a custom type inference and unwrapping system to handle its unique immutability model while transpiling to Go.

---

## Table of Contents

1. [Overview](#overview)
2. [Two-Layer Type Inference Architecture](#two-layer-type-inference-architecture)
   - [Layer 1: Manual Inference](#layer-1-manual-inference-getexprtypenamemanuall)
   - [Layer 2: Hindley-Milner Inference](#layer-2-hindley-milner-inference-inferexprtype)
   - [Main Entry Point: `getExprTypeName`](#main-entry-point-getexprtypename)
3. [Type System Bridge](#type-system-bridge)
   - [Type Conversion](#type-conversion)
   - [Expression Conversion](#expression-conversion)
   - [Environment Building](#environment-building)
4. [When to Use Each Method](#when-to-use-each-method)
5. [Variable Declarations](#variable-declarations)
   - [`val` Declarations](#val-declarations)
   - [`var` Declarations](#var-declarations)
6. [Automatic Unwrapping](#automatic-unwrapping)
   - [Identifier Transformation](#1-identifier-transformation)
   - [Expression Unwrapping](#2-expression-unwrapping)
7. [Type Resolution](#type-resolution)
8. [Supported Expressions](#supported-expressions)
9. [Downward Inference for Generic Sealed-Type Case Constructors](#downward-inference-for-generic-sealed-type-case-constructors)
10. [Downward Inference for Generic Function Phantom Type Parameters](#downward-inference-for-generic-function-phantom-type-parameters)
11. [Go Type Inference](#go-type-inference)
12. [Limitations and Edge Cases](#limitations-and-edge-cases)
13. [Key Files](#key-files)

---

## Overview

GALA distinguishes between immutable variables (`val`) and mutable variables (`var`). In the generated Go code:
- `val` variables are represented as `std.Immutable[T]`.
- `var` variables are represented as their raw types `T`.

The transpiler is responsible for automatically wrapping values into `std.Immutable` and unwrapping them using the `.Get()` method.

---

## Two-Layer Type Inference Architecture

GALA uses a **two-layer type inference system** that combines speed with power:

```
Expression
    ↓
┌─────────────────────────┐
│ Layer 1: Manual         │  Fast pattern-based inference
│ getExprTypeNameManual() │  Handles 90%+ of cases
└───────────┬─────────────┘
            │ (if unresolved or has type params)
            ↓
┌─────────────────────────┐
│ Layer 2: Hindley-Milner │  Unification-based inference
│ inferExprType()         │  Handles generics and lambdas
└─────────────────────────┘
```

### Layer 1: Manual Inference (`getExprTypeNameManual`)

**Location:** `internal/transpiler/transformer/types.go`

Fast, pattern-based inference that handles common cases without the overhead of unification:

| Expression Type | Inference Method |
|-----------------|------------------|
| Literals (`1`, `"foo"`) | Token-based: INT→int, STRING→string |
| Identifiers (`x`) | Scope lookup via `getType()` |
| Boolean literals | Hardcoded: `true`/`false` → bool |
| Binary operations | Operator-based: `+`→operand type, `==`→bool |
| Function calls | Metadata lookup in `RichAST.Functions` |
| Struct fields | Metadata lookup in `RichAST.Types` |
| Method calls | Return type from method metadata |
| Selector expressions | Package-qualified lookups |

**When it succeeds:** Returns a concrete `transpiler.Type`.

**When it fails:** Returns `NilType` or a type containing unresolved type parameters.

### Layer 2: Hindley-Milner Inference (`inferExprType`)

**Location:** `internal/transpiler/transformer/bridge.go`

Full Hindley-Milner type inference using Algorithm W with unification. Used when:

1. Manual inference returns `NilType`
2. Manual inference returns a type with unresolved type parameters (e.g., `T` instead of `int`)
3. Manual inference returns `any`

**Capabilities:**
- Generic function instantiation
- Lambda parameter type inference
- Constraint solving via unification
- Polymorphic type schemes

### Main Entry Point: `getExprTypeName`

**Location:** `internal/transpiler/transformer/types.go:563`

This is the **primary entry point** for type inference. It implements the two-layer strategy:

```go
func (t *galaASTTransformer) getExprTypeName(expr ast.Expr) transpiler.Type {
    // Try manual inference first for speed
    res := t.getExprTypeNameManual(expr)
    if !res.IsNil() && !t.hasTypeParams(res) && res.String() != "any" {
        return res  // Fast path: concrete type found
    }

    // Fallback to Hindley-Milner for complex cases
    hmRes, err := t.inferExprType(expr)
    if err == nil && !hmRes.IsNil() && hmRes.String() != "any" {
        return hmRes
    }

    return res  // Return manual result even if incomplete
}
```

**Decision flow:**
1. Call `getExprTypeNameManual(expr)`
2. If result is concrete (non-nil, no type params, not `any`): return it
3. Otherwise: call `inferExprType(expr)` via Hindley-Milner
4. If HM succeeds with concrete type: return it
5. Otherwise: return the manual result (may be partial)

---

## Type System Bridge

The bridge between the transpiler's type system and the Hindley-Milner inference system is in `internal/transpiler/transformer/bridge.go`:

### Type Conversion

| Direction | Function | Purpose |
|-----------|----------|---------|
| transpiler → HM | `toInferType()` | Convert for HM inference |
| HM → transpiler | `fromInferType()` | Convert results back |

### Expression Conversion

`toInferExpr()` converts Go AST expressions to HM expressions:

```go
ast.BasicLit → infer.Lit
ast.Ident    → infer.Var
ast.CallExpr → infer.App (curried)
ast.BinaryExpr → infer.App(App(op, x), y)
```

### Environment Building

`buildTypeEnv()` creates the HM type environment from:
1. Current scope variables (from `Scope.valTypes`)
2. Functions from `RichAST.Functions`
3. Built-in operators (arithmetic, comparison)

For generic functions, type parameters are converted to HM type variables and the scheme is generalized.

---

## When to Use Each Method

### Use `getExprTypeName` (recommended)

**The primary entry point.** Automatically handles both layers:

```go
typ := t.getExprTypeName(expr)
```

Use this for:
- Variable declarations
- Return type checking
- Argument type inference
- Any general type inference need

### Use `getExprTypeNameManual` directly

Only when you explicitly want to avoid HM inference overhead and can handle partial results:

```go
typ := t.getExprTypeNameManual(expr)
if !typ.IsNil() {
    // Use type
}
```

Use cases:
- Quick checks where you'll retry with full inference if needed
- Performance-critical loops
- When you're building input for HM yourself

### Use `inferExprType` directly

Only when you specifically need HM capabilities:

```go
typ, err := t.inferExprType(expr)
if err != nil {
    // Handle inference failure
}
```

Use cases:
- Lambda type inference
- Complex generic instantiation
- When manual inference is known to be insufficient

---

## Variable Declarations

### `val` Declarations
When a variable is declared using `val`:
1. The transpiler infers the type of the right-hand side (RHS) expression.
2. It wraps the RHS with a call to `std.NewImmutable(rhs)`.
3. The variable is added to the transpiler's scope as an immutable variable.

Example:
```gala
val x = 1
```
Transpiles to:
```go
var x = std.NewImmutable(1)
```

### `var` Declarations
When a variable is declared using `var`:
1. The transpiler infers the type of the RHS expression.
2. If the RHS is of type `Immutable[T]`, it automatically adds a `.Get()` call to unwrap it.
3. The variable is added to the transpiler's scope as a mutable variable.

Example:
```gala
val x = 1
var y = x
```
Transpiles to:
```go
var x = std.NewImmutable(1)
var y = x.Get()
```

---

## Automatic Unwrapping

GALA uses two complementary mechanisms for automatic unwrapping of `Immutable` values:

### 1. Identifier Transformation
The most common form of unwrapping happens at the identifier level. In `internal/transpiler/transformer/expressions.go`, the `transformPrimary` method checks if an identifier refers to a variable declared with `val`. If it does, the identifier is automatically transformed into a call to its `.Get()` method.

Example:
```gala
val x = 1
val y = x + 1
```
Transpiles to:
```go
var x = std.NewImmutable(1)
var y = std.NewImmutable(x.Get() + 1)
```

### 2. Expression Unwrapping
For expressions that are not simple identifiers (e.g., function calls), GALA uses the `unwrapImmutable` method. This method uses type inference (`getExprTypeName`) to determine if an expression's result is an `Immutable` type. If it is, a `.Get()` call is appended to the expression.

This mechanism is used in:
- `var` variable declarations and assignments.
- `return` statements.
- Function arguments (in some contexts).

Example:
```gala
func getImm() Immutable[int] = NewImmutable(42)
func main() {
    var w = getImm() // getImm() is unwrapped to getImm().Get()
}
```

Unwrapping is based on the inferred type name. If the type name starts with `Immutable[` or `std.Immutable[`, it is considered unwrappable.

---

## Type Resolution

Type resolution follows a 5-level precedence order (defined in `scope.go`):

1. **Current scope** - Local variables in the innermost scope
2. **Outer scopes** - Variables from enclosing functions/blocks
3. **Current package types** - Types defined in the current file
4. **Prelude packages** - Auto-imported packages (std library)
5. **Explicitly imported packages** - User imports

The `getType(name)` method in scope.go implements this resolution order.

**Note**: Dot imports take precedence over aliased imports for unqualified name resolution. If both `. "collection_immutable"` and `cm "collection_mutable"` are imported, bare `Array` resolves to `collection_immutable.Array`.

---

## Supported Expressions

### Manual Inference (`getExprTypeNameManual`)

| Expression | Inference Strategy |
|------------|-------------------|
| `1`, `1.5`, `'a'`, `"s"` | Literal kind → type |
| `true`, `false` | → `bool` |
| `nil` | → `NilType` |
| `x` | Scope lookup |
| `x.field` | Struct field lookup |
| `pkg.Type` | Package-qualified lookup |
| `f(args)` | Function metadata lookup |
| `x.Method()` | Method metadata lookup |
| `x + y`, `x == y` | Operator-based |
| `&x` | → `PointerType{Elem: type(x)}` |
| `*p` | Dereference pointer type |
| `arr[i]` | Array element type |
| `Some(x)`, `None[T]()` | Option type construction |
| `Left(x)`, `Right(x)` | Either type construction |
| `expr.Get()` | Immutable inner type, generic container element (`Array[T].Get()` → `T`), or — when the receiver is a non-generic named type with a user-declared `Get` method — that method's declared return type |
| `NewImmutable(x)` | Immutable wrapper type |

> **Note:** `Get` is special-cased because it backs implicit `Immutable` unwrapping and the built-in container accessors. A user type may still declare its own `Get` method (e.g. a `Get() Option[T]` accessor); its declared return type is honored so that chained higher-order calls on the result (`expr.Get().FlatMap(...)`, `.Map(...)`) keep their concrete element type instead of widening to `any`.

### Hindley-Milner Inference (`inferExprType`)

Additional capabilities beyond manual:

| Expression | HM Capability |
|------------|---------------|
| Generic calls | Type parameter instantiation |
| Lambdas | Parameter type inference |
| Complex compositions | Unification-based resolution |
| Polymorphic functions | Scheme instantiation |

### Lambda Parameter Type Inference

When a lambda is passed as an argument to a method with known parameter types, the lambda's parameter types can be inferred from the method's signature. This allows omitting explicit parameter type annotations. Combined with Go's type parameter inference, method type parameters can also be omitted:

```gala
// Before: all types explicit
val doubled = opt.Map[int]((x int) => x * 2)

// After: both lambda param and method type param inferred
val doubled = opt.Map((x) => x * 2)  // x inferred as int, Map's U inferred as int
```

The inference works by extracting expected parameter types from `FuncType` metadata in the analyzer and passing them to `transformLambdaWithExpectedType`. When a parameter has no type annotation and an expected type is available, the expected type is used instead of defaulting to `any`. Method type parameters (like `U` in `Map[U]`) are inferred by Go from the concrete lambda return type.

Lambda parameter inference works for:

1. **Generic receiver types** — Methods on generic types like `Array[T]`, `Option[T]`, where receiver type params are resolved and substituted into method parameter types.

2. **Non-generic wrapper types** — Methods on concrete types like `Str` that take function parameters (e.g., `Str.Map(func(rune) rune)`, `Str.Filter(func(rune) bool)`). The expected function type is extracted from method metadata and passed to the lambda.

3. **FoldLeft/FoldRight accumulator** — The accumulator type parameter `U` in `FoldLeft[U](zero U, f func(U, T) U)` is inferred from the zero value argument's type, without requiring explicit type annotation:

```gala
val sum = list.FoldLeft(0, (acc, x) => acc + x)        // acc inferred as int from 0
val joined = list.FoldLeft("", (acc, x) => acc + x)     // acc inferred as string from ""
```

4. **Function-typed declaration contexts** — When a lambda initializes a slot
   with a declared function type, that signature is threaded into the lambda so
   its parameters resolve to the declared types. This covers a direct
   initializer, both branches of an if-expression feeding the slot, and curried
   lambdas (the return type is decomposed into the body lambda):

```gala
val direct func(int) int = (x) => x + 1                       // x inferred as int
val branched func(int) int = if (c) { (x) => x } else { (x) => x * 2 }
val curried func(int) func(int) int = (a) => (b) => a + b     // a, b inferred as int
```

When a lambda parameter has no annotation **and** no expected type can be
inferred — e.g. a bare `val f = (x) => x + 1` with no declared type — the
transpiler rejects it with [`GALA-E0033`](errors/GALA-E0033.md) rather than
defaulting the parameter to `any`. Annotate the parameter (`(x int) => …`) or
place the lambda in one of the typed contexts above. (Call-argument lambdas
whose parameter types cannot yet be inferred still fall back to `any`; that is a
separate inference-completeness concern, not a hard error.)

### By-Name Argument Lifting (Thunk Sugar)

When a call argument's expected type is a **zero-arg** function type (`func() T`
or void `func()`) and the argument is a plain expression rather than a lambda or
a function value, the transpiler lifts it into `func() T { return expr }`
(`func() { expr }` for void). This lets `Future(compute())` mean
`Future(() => compute())`.

The thunk's result type `T` is resolved as follows:

- If the expected result type is already concrete (e.g. `Future[int](compute())`
  or a non-generic `func() int` parameter), it is used directly.
- Otherwise — the un-annotated generic case such as `Future(compute())` — the
  companion `Apply`'s parameter type still mentions an unresolved type parameter.
  The expected-type resolver propagates a **masked** function type whose
  type-param-bearing results become `NilType`, so the argument transformer falls
  back to the lifted expression's own inferred type (`compute()`'s `int`) for the
  thunk's declared result. The concrete `func() int` literal then drives the
  companion's type-parameter unification, yielding `Future[int]`.

The masking is what keeps the lambda and bare-expression paths consistent: a
lambda argument in the same position sees the masked result as `any`
(`typeToExpr(NilType)`), stays non-concrete, and self-infers its return type from
its body exactly as before.

The lift is purely additive — passing a bare `T` where a `func() T` is expected
is otherwise a type error — and an argument whose own type is already a function
is passed through untouched, so an existing thunk is never double-wrapped.

---

## `bind` / `also` Element and Result Inference

The `bind`/`also` lowering (`internal/transpiler/transformer/bind.go`) resolves three
things by type inference: the **result monad** `M[R]`, each clause's **bound element
type** `A`, and — for an `also` group with a `Zip` — the **product tuple** type.

- **Result monad `M[R]`.** Taken from the enclosing block's declared result type (the
  function's return type, or the expected type pushed onto the block). It fixes both the
  base monad `M` a clause must belong to and the FlatMap continuation's return type
  `M[R]`. The trailing value expression is transformed with `M[R]` pushed as its expected
  type, so constructors like `Success`/`Some`/`Valid` infer their type arguments from the
  block instead of needing explicit `[…]`.

- **Bound element type `A`.** For a clause `bind name = expr` whose transformed RHS has
  monad type `M[A]`, `A` is the monad's **value element** — the *last* type argument
  (`monadElemType`). For a single-argument monad (`Try[T]`, `Option[T]`) that is `T`; for
  a two-argument monad (`Either[A, B]`, `Validated[E, A]`) it is the value side (`B` /
  `A`), while the leading error/left argument is fixed. An explicit annotation on the
  clause — `bind name A = expr` — overrides the inferred element type. `name` is then
  registered as an immutable `val` of type `A`, so later reads emit a single `.Get()`
  unwrap.

- **Bindability and same-monad check.** The clause's monad base name is resolved from its
  transformed RHS and looked up in type metadata: it must expose a `FlatMap` method
  (structural — no type is special-cased) or inference fails with a "not a bindable
  monad" diagnostic. The clause's base name must also equal the result monad's base name;
  a mismatch (e.g. an `Option` clause in a `Try` block) is a "heterogeneous bind" error.

- **FlatMap type arguments.** The emitted `M_FlatMap` carries the method-level type arg
  `[R]` (the result element of `M[R]`, from `resultElemTypeArgs`); the receiver's own
  element type is appended by the generic-method emitter. The callback parameter is typed
  `A`.

- **`also` product tuple.** For a `bind` + `also` group of arity `N` (2..10) whose monad
  provides `Zip{N}`, each clause `i` contributes its inferred element type `A_i` as a
  `Zip{N}` method type argument. The `Zip{N}` result element is the std tuple
  `Tuple{N}[A_0, …, A_{N-1}]`, and the whole Zip result is `M[Tuple{N}]`. The single
  following `FlatMap` binds each `val name_i` to tuple field `V{i+1}` — tuple fields are
  already `Immutable`-wrapped, so the field is bound straight to the `val` (no extra
  wrap) and read with one `.Get()`. If the monad has no `Zip{N}`, the group falls back to
  the nested sequential `FlatMap` chain and no tuple type is formed.

---

## Downward Inference for Generic Sealed-Type Case Constructors

A generic sealed type's case constructors (e.g. `NoCmd()` for `sealed type Cmd[T any]`) carry a vestigial type parameter that cannot be inferred from the constructor's own arguments — a zero-field variant has no value of type `T` to pin against. To avoid forcing users to write `NoCmd[Msg]()` everywhere, the transpiler propagates an **expected type** downward from the surrounding context and rewrites the bare reference into an explicit instantiation (`NoCmd[Msg]{}.Apply()`).

Five contexts feed the expected type:

| # | Context | Example |
|---|---------|---------|
| 1 | `val`/`var` with explicit type | `val c Cmd[int] = NoCmd()` |
| 2 | Function return type | `func make() Cmd[Msg] = NoCmd()` |
| 3 | Function argument expected type | `runCmd(NoCmd())` where `runCmd(c Cmd[Msg])` |
| 4 | Container element type | `ArrayOf[Cmd[Msg]](NoCmd(), QuitCmd())` |
| 5 | Match arm with externally-pinned result | `val c Cmd[Msg] = m match { case Tick() => NoCmd() … }` |

### Propagation mechanism

- A LIFO stack on the transformer, `expectedArgTypes` (see
  `transformer/expected_arg_stack.go`), threads expected slot types
  from setter contexts (val decl, argument transform, tuple element)
  into the call dispatcher. Each `push` returns an idempotent unwind
  function — if a downstream `consume` removes the hint first, the
  unwind is a no-op. This replaced an earlier single-field channel
  whose mismatched save/restore bookkeeping silently leaked state
  across siblings.
- For the **zero-arg call** path (`NoCmd()` with empty parens),
  `applyCallSuffix` peeks the top hint after `inferZeroArgTypeParams`
  (which covers the enclosing-return and match-subject fallbacks for
  `None()` / `Some()`), and rewrites the receiver via
  `injectSealedVariantTypeArgs`. On a successful rewrite it consumes
  the hint so deeper sub-expressions cannot pick it up.
  `inferZeroArgTypeParams` consults `currentFuncReturnType` **before**
  `currentMatchSubjectType`: in value position the arm body's type is
  the match *result* type, which the declared return pins, while the
  subject is only a proxy that coincides with the result when the match
  maps `Option[T]` to `Option[T]`. The two diverge in e.g.
  `v.AsObject() match { case Some(fields) => …; case None() => None() }`
  inside `func firstName(v) Option[string]` — the subject is
  `Option[Array[JField]]` but the result must be `Option[string]`, so
  the return type wins and the subject stays a fallback for the
  no-return-type case.
- For the **non-zero-arg call** path, `transformCallWithArgsCtx`
  consumes the top hint at dispatcher entry and invokes the same
  rewrite.
- For **function arguments** (context 3),
  `resolveExpectedFuncArgType` returns the declared param type
  verbatim for non-FuncType params of non-generic GALA functions, so
  the argument-transform path can push it onto the stack.
- For **tuple elements** (context 4 / context 2 with tuple return),
  `transformPrimary` inspects the enclosing `currentFuncReturnType`
  for a Tuple-shaped expected type and pushes each per-slot type
  onto the stack for the duration of that element's transform.
- For **match arms** (context 5), `buildMatchExpressionFromClauses`
  promotes a pending expected slot type to `currentFuncReturnType`
  for the IIFE's body, so each arm's expression — not just the first
  — picks it up via the existing companion-of-sealed lookup.

If none of the contexts supplies a concrete type and the constructor
is a sealed variant of a generic parent, the transpiler fails fast
with [`GALA-E0018`](errors/GALA-E0018.md) at the offending call — it
does **not** silently emit an untyped `Variant{}` literal that Go
would later reject with an obscure deduction error far from the
source. The sealed-variant gate (`isSealedVariantTypeName`) splits any
package qualifier off the name and passes it as the lookup scope: the
zero-arg path derives the name from `getBaseTypeName`, which keeps the
selector for dot-imported / std variants (`std.None`), while
sealed-variant metadata is registered under the bare case name.
Matching the qualified `std.None` against the unqualified metadata
names found nothing, so the guard never fired for `std.None()` and an
uninstantiated `std.None{}.Apply()` (invalid Go) was emitted instead of
the diagnostic. Passing the qualifier as scope (rather than searching
every package) also keeps the check precise when two packages declare
a sealed case of the same name.

### Limitations

The match-arm path (context 5) currently propagates a single type
only when the match expression is itself the immediate value of one
of the surrounding contexts. Arms inside a match used purely as a
side-effect dispatch still rely on per-arm inference.

### Pointers

| File | Role |
|------|------|
| `transformer/expected_arg_stack.go` | Push/peek/consume primitives + the idempotent-unwind contract |
| `transformer/calls.go::injectSealedVariantTypeArgs` | Rewrites `Variant(...)` → `Variant[Args]` when expected is the sealed parent |
| `transformer/calls.go::applyCallSuffix` (zero-arg branch) | Peeks/consumes the top hint for `Variant()`; emits `GALA-E0018` when no signal pins the parameter |
| `transformer/calls.go::transformCallWithArgsCtx` | Consumes the top hint for `Variant(arg, …)` |
| `transformer/call_context.go::resolveExpectedFuncArgType` | Surfaces non-FuncType param types from non-generic functions |
| `transformer/constructors.go::transformPrimary` (paren/tuple case) | Pushes per-element expected type for tuple literals |
| `transformer/declarations.go::transformValDeclaration` / `transformVarDeclaration` | Pushes the explicit declared type before transforming RHS |
| `transformer/postfix.go::buildMatchExpressionFromClauses` | Promotes a pending slot type to `currentFuncReturnType` for arm bodies |

---

## Downward Inference for Generic Function Phantom Type Parameters

The same problem arises for a generic **free function** whose type parameter
appears only in its **return type**, never in a parameter — a *phantom*
result-only param. The canonical case is an error-only constructor for a
two-parameter type:

```gala
// `A` is phantom: it appears in the return type but in no parameter.
func InvalidOf[E any, A any](err E) Validated[E, A] = Invalid(ArrayOf(err))

func vName(s string) Validated[string, string] =
    if (s != "") Valid(s) else InvalidOf("name required")
```

`E` is inferable from the argument, but `A` is not — there is no value of
type `A` at the call site to pin it against. Go's own type inference cannot
recover it either, so emitting the call verbatim (`InvalidOf("…")`) produces
Go that fails to compile with `cannot infer A`. That would violate the
"generate concrete types, or fail with an error" rule by silently emitting
un-compilable output.

### Propagation mechanism

`transformCallWithArgsCtx` resolves the call's type parameters in two steps,
via `injectFuncPhantomTypeArgs` (`transformer/type_inference.go`), just before
the verbatim-call fallback:

1. **From arguments.** Unify each declared parameter type against the
   corresponding argument type, accumulating a partial substitution map. If
   this already determines *every* type parameter, the call is left unchanged
   so Go's own inference keeps producing the existing output — no explicit args
   are injected.
2. **From the expected type.** Otherwise, unify the function's declared return
   type against the expected type to fill the remaining (phantom) params. Two
   expected-type sources are tried in order: the call-site hint on the
   `expectedArgTypes` stack (consumed at dispatcher entry — e.g. an argument
   slot, a `val` with a declared type, or a `bind` block's trailing value),
   then the enclosing function's return type (`currentFuncReturnType`).

The call target is rewritten into an explicit instantiation
(`InvalidOf[string, string]("…")`) **only when every type parameter resolves to
a concrete type**. If any remains unresolved, the target is left unchanged.
This makes the rewrite strictly *broken-to-working or a no-op*: it never turns
output Go already accepts into something it rejects, and it is skipped entirely
when arguments alone suffice.

This mirrors the sealed-variant mechanism above (`injectSealedVariantTypeArgs`)
— both propagate an expected type downward to pin a parameter the call's own
arguments cannot — but applies to any generic function rather than only
sealed-case constructors.

### Pointers

| File | Role |
|------|------|
| `transformer/type_inference.go::injectFuncPhantomTypeArgs` | Fills phantom function type params from args + expected type; wraps the target in explicit type args when all resolve |
| `transformer/calls.go::transformCallWithArgsCtx` | Invokes the fill just before the verbatim-call fallback, passing the consumed call-site hint |

---

## Go Type Inference

When the Go SDK is available (via `GOROOT` or `PATH`), the transpiler can infer types from Go standard library and third-party packages:

- **Function return types**: `os.TempDir()` → inferred as `string`
- **Method return types**: `strings.NewReader("hello").Read(buf)` → inferred from Go method signature
- **Struct field types**: `req.URL.Host` → inferred as `string`

This enables seamless interop without explicit type annotations when calling Go functions.

Go type inference is optional — if the Go SDK is not found, transpilation still succeeds but Go type resolution is disabled (a warning is printed to stderr).

### GOROOT discovery (`findGOROOT` in `internal/transpiler/analyzer/go_types.go`)

The transpiler resolves the Go SDK in this priority order:

1. **Hermetic Bazel SDK** — when running inside a Bazel action, the SDK that
   `rules_go` materializes in the execution root (`external/<…go_sdk…>/`) is
   preferred. This keeps type inference on the exact Go version Bazel compiles the
   generated code with, instead of whatever `go` is installed on the host.
2. `GOROOT` environment variable (e.g. via `--goroot` / `--action_env=GOROOT`).
3. `runtime.GOROOT()` of the running binary.
4. Walking up from the running binary to a Go SDK layout.
5. A `go` binary on `PATH`. Symlinks are resolved (so a Homebrew `go`, which links
   into `…/Cellar/go/<ver>/libexec`, derives the correct root), with `go env
   GOROOT` as an authoritative fallback.

Standalone `gala build` skips step 1 (no Bazel execution root) and uses the host
Go discovered in steps 2–5.

---

## Limitations and Edge Cases

- **String-based matching**: Type checks rely heavily on string prefixes and names, which can be fragile when dealing with type aliases or complex generic types.
- **Recursive Unwrapping**: `unwrapImmutable` currently performs only one level of unwrapping. But multiple wrapping should never happen in the first place.
- **Partial inference**: When both layers fail, you may get `NilType` or a type with unresolved parameters. Callers must handle these cases.
- **Type aliases**: Not fully supported yet (see `declarations.go:649`).

---

## Key Files

| File | Purpose |
|------|---------|
| `transformer/types.go` | Main type inference (`getExprTypeName`, `getExprTypeNameManual`) |
| `transformer/bridge.go` | HM bridge (`toInferType`, `fromInferType`, `inferExprType`) |
| `transformer/scope.go` | Type resolution (`getType`) |
| `infer/` | Hindley-Milner implementation |
