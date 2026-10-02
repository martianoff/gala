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
9. [`bind` / `also` Element and Result Inference](#bind--also-element-and-result-inference)
10. [Downward Inference for Generic Sealed-Type Case Constructors](#downward-inference-for-generic-sealed-type-case-constructors)
11. [Untyped Constants in Immutable Slots](#untyped-constants-in-immutable-slots)
12. [Downward Inference for Generic Function Phantom Type Parameters](#downward-inference-for-generic-function-phantom-type-parameters)
13. [Go Type Inference](#go-type-inference)
14. [Limitations and Edge Cases](#limitations-and-edge-cases)
15. [Key Files](#key-files)

---

## Overview

GALA distinguishes between immutable variables (`val`) and mutable variables (`var`). In the generated Go code:
- `val` variables are represented as `std.Immutable[T]`.
- `var` variables are represented as their raw types `T`.

The transpiler is responsible for automatically wrapping values into `std.Immutable` and unwrapping them using the `.Get()` method.

### `.Size()` / `.ByteSize()` sugar (replaces bare `len`)

Bare `len` is a forbidden Go builtin. On a Go primitive receiver the transpiler
lowers the size methods and infers `int`:

| Receiver | `.Size()` lowers to | `.ByteSize()` lowers to |
|---|---|---|
| `string` | `utf8.RuneCountInString(s)` (character count) | `len(s)` (bytes) |
| Go slice `[]T` / map | `len(x)` | — |
| GALA `Array`/`List`/`HashMap` | the type's real `Size()` method | — |

Both are inferred as `int` (see `inferCallSelectorType`). The sugar fires once
the receiver's type is known — including the value component of a multi-return
binding (`val xs, err = goCall()`), whose per-name types are resolved from the
call's Go signature. `go_builtins.Panic` lowers to Go's builtin `panic` in
every position.

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

### 3. Package-Level Bindings Across Files and Packages
The transformer only sees the declarations of the file it is transforming, so the analyzer records every package-level `val`/`var` (`RichAST.PackageVals`) with its val/var classification and element type:

- **Same package, other file** — the transformer pre-registers the package's bindings in the global scope, so a bare reference unwraps exactly like a same-file one.
- **Another package** — the analyzer's closure walk records each imported package's *exported* bindings in `RichAST.ImportedVals`, keyed by import path and then name, so two imported packages that share a name never collide. They never enter the importer's own `PackageVals`. A selector `colors.Green` (or `c.Green` through an import alias) naming a `val` types as `std.Immutable[T]` and is unwrapped at once, as a same-package reference is; a `var` is left as written. The same lookup serves calling a val whose type has `Apply` (`colors.AddTen(5)`) and matching with a val extractor (`case colors.Halve(h)`). Under a dot-import the bindings are registered by bare name, after the current package's own. Assigning to, compound-assigning or incrementing an imported `val` is rejected, checked on the parse tree like a same-package one.

The element type is read from an explicit annotation, or from the initializer when its shape settles it without full expression inference: a literal (the whole initializer, so `s"$x" == ""` is not a string) or a signed number, a reference to another package-level binding, or a non-generic call dispatched the way the transformer dispatches it — a struct literal when the argument count matches the fields, otherwise a companion's or the struct's own `Apply` result (so `val Green = NamedColor(2)` is the sealed parent `Color`), or a function's declared return type. Otherwise — including a call to a void function — the type is unknown: the unwrap is still emitted (it needs only the val/var classification), but downstream inference has less to go on, e.g. which fields of a struct-typed val are themselves `Immutable`. This reads the initializer's parse tree rather than running full inference, so a reference to a binding declared later in the same file stays unknown.

The on-disk analysis cache persists each package's own `PackageVals`, so the same metadata reaches an importer whether the package was analyzed fresh or served from the cache, by the CLI builder or the Bazel worker.

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
| `expr.Get()` | On a `val`, an immutable field, or an `Immutable[T]`: the unwrapped inner type. Otherwise an ordinary method call: a generic container element (`Array[T].Get(i)` → `T`), a user-declared `Get` method's return type (value or pointer receiver, generic or not), or a Go type's `Get` from Go type info (`url.Values.Get` → `string`) |
| `NewImmutable(x)` | Immutable wrapper type |

> **Note:** `Get` is special-cased only for implicit `Immutable` unwrapping. Every other `Get` is inferred like any other method call, so a user type's own `Get` method (e.g. a `Get() Option[T]` accessor) keeps its declared return type and chained higher-order calls on the result (`expr.Get().FlatMap(...)`, `.Map(...)`) keep their concrete element type instead of widening to `any`. Pointer receivers (`func (b *Buffer) Get() Cell`) resolve the same way, so field chains on the result (`b.Get().St.Bold`) unwrap each immutable field. A generic receiver — value or pointer — has its type arguments substituted into the declared return type even when they are type parameters of the enclosing generic function: inside `func f[T any](b *Box[T])`, `b.Get()` on `func (b *Box[T]) Get() T` is `T`. A `Get` that cannot be resolved is never typed as the receiver itself.

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

<!-- doc-check: fragment -->
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

<!-- doc-check: fragment -->
```gala
val sum = list.FoldLeft(0, (acc, x) => acc + x)        // acc inferred as int from 0
val joined = list.FoldLeft("", (acc, x) => acc + x)     // acc inferred as string from ""
```

4. **Function-typed declaration contexts** — When a lambda initializes a slot
   with a declared function type, that signature is threaded into the lambda so
   its parameters resolve to the declared types. This covers a direct
   initializer, both branches of an if-expression feeding the slot, and curried
   lambdas (the return type is decomposed into the body lambda):

<!-- doc-check: fragment -->
```gala
val direct func(int) int = (x) => x + 1                       // x inferred as int
val branched func(int) int = if (c) { (x) => x } else { (x) => x * 2 }
val curried func(int) func(int) int = (a) => (b) => a + b     // a, b inferred as int
```

5. **Partial explicit type arguments** — a generic call that writes only its
   leading type arguments binds those as written and infers the rest from the
   other arguments and the lambda's body, as Go does for the emitted call:
   `resource.Using[Res](open(p), (r) => r.Name())` types `r` as `Res`, and the
   lambda's result comes from its body.

6. **Conversion to a named function type** — for `type Handler func(int) int`,
   `Handler((x) => x + 1)` types the lambda by `Handler`'s signature.

When a lambda parameter has no annotation **and** no expected type can be
inferred — e.g. a bare `val f = (x) => x + 1` with no declared type — the
transpiler rejects it with [`GALA-E0033`](errors/GALA-E0033.md) rather than
defaulting the parameter to `any`. Annotate the parameter (`(x int) => …`) or
place the lambda in one of the typed contexts above. A call-argument lambda gets
the same error when the callee's signature is unknown, so its slot has no type
at all. A parameter whose slot is typed `any` is still lowered to `any`: as the
callee declares it, or because no argument of the call determines a type
parameter the slot names.

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

### Function Result Type (never inferred)

A named function's result type is the one its signature declares; it is never
inferred from the body. `func f() int = 42` returns an `int`. `func f() = 42`
has no result type, so it is void: its body is a statement, as in
`func f() { 42 }`, and a value nothing uses is rejected as evaluated but not used.
A call body (`func greet() = Println("hi")`) is made and its result discarded.
Only lambdas take a result type from their body (below).

### Lambda Result Type (the Return Slot)

A lambda has its own return slot (`returnSlot` in
`internal/transpiler/transformer/return_slot.go`). A `return`, a `bind` block and
the return-type fallbacks in its body use it, never the enclosing function's
return type.

- An annotated result (`(x int) Option[int] => …`) or a concrete expected
  function type fills the slot up front.
- Otherwise the body's own result values fill it, **in any order**. A `return`
  whose value has a fully resolved type fills the slot. A resolved type has no
  masked part, is not `any` or void, and names no type parameter left unbound by
  a callee (the `T` of `Failure(e)`'s `Try[T]`). The type parameters of the
  enclosing generic function or method, and of its receiver, count as resolved. Any other `return` (`return
  None()`, `return Failure(e)`) is deferred. Once the body is lowered, the promoted
  trailing value or a `bind` chain can also fill the slot. The deferred returns
  are then lowered again against it, so an early guard
  `if (x < 0) { return None() }` takes `Option[int]` from a later `Some(x + 1)`.
- Only a value's own type fills the slot, never a guess. While a `return`
  value or the body's trailing value is first lowered, the enclosing match
  subject (which `None()` would otherwise fall back to) is hidden. A trailing
  value that fails to lower that way is deferred like a `return`. The same holds
  for a `bind` block's trailing value, and a `bind` type taken from the subject
  never fills the slot.
- If nothing fills the slot, each deferred return is lowered again with that
  context restored. One that still has no settled type is an error: "cannot
  infer the result type of this lambda" (or the constructor's own error, such as
  [`GALA-E0018`](errors/GALA-E0018.md)).

A value-position match or if-expression lowers to an IIFE and gives its arms
and branches their own slot, since a `return` there leaves only the IIFE. A
statement-position match whose arms return is inlined instead, so its returns
fill the lambda's slot.

---

## `bind` / `also` Element and Result Inference

The `bind`/`also` lowering (`internal/transpiler/transformer/bind.go`) resolves three
things by type inference: the **result monad** `M[R]`, each clause's **bound element
type** `A`, and — for an `also` group with a `Zip` — the **product tuple** type.

- **Result monad `M[R]`.** Taken from the enclosing block's declared result type (the
  function's or lambda's return slot, or the expected type pushed onto the block). In a
  lambda whose result type is not known, a block whose value is consumed takes `M[R]`
  from its trailing value, which must then be a settled `M` value, and fills the
  lambda's slot with it (see [Lambda Result Type](#lambda-result-type-the-return-slot)). It fixes both the
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
  `applyCallSuffix` first peeks the top hint and rewrites the receiver
  via `injectSealedVariantTypeArgs`. On a successful rewrite it
  consumes the hint so deeper sub-expressions cannot pick it up. The
  hint is the type of the slot the constructor itself fills, so it wins
  over the enclosing-return and match-subject fallbacks of
  `inferZeroArgTypeParams`, which run only when no hint applies:
  `take(None(), 1)` for `take[T any](d Option[Drag], v T)` inside a
  function returning `Option[int]`, or inside an arm of a match on an
  `Option[int]`, is `None[Drag]`.
  `inferZeroArgTypeParams` consults `returnSlot.typ` **before**
  `currentMatchSubjectType`: in value position the arm body's type is
  the match *result* type, which the declared return pins, while the
  subject is only a proxy that coincides with the result when the match
  maps `Option[T]` to `Option[T]`. The two diverge in e.g.
  `v.AsObject() match { case Some(fields) => …; case None() => None() }`
  inside `func firstName(v) Option[string]` — the subject is
  `Option[Array[JField]]` but the result must be `Option[string]`, so
  the return type wins and the subject stays a fallback for the
  no-return-type case. A context type is skipped only when it names a
  type parameter nobody has bound yet (the `T` of a callee's
  `Option[T]`). The type parameters of the declaration being lowered —
  its own and its receiver's — are known types, so `return None()` in
  `func (b Box[T]) Pick[U any](o Option[U]) Option[U]` lowers to
  `None[U]`, and so does an early `return None()` in a lambda whose
  slot is filled with `Option[U]`. Generic functions and generic
  methods behave the same way.
- For the **non-zero-arg call** path, `transformCallWithArgsCtx`
  consumes the top hint at dispatcher entry and invokes the same
  rewrite.
- For **function arguments** (context 3),
  `resolveExpectedFuncArgType` returns the declared param type
  verbatim for a non-FuncType param that names none of the callee's
  type parameters — every param of a non-generic function, and
  `d Option[Drag]` of `take[T any](d Option[Drag], v T)` whether the
  type arguments are explicit (`take[int](None(), 1)`) or inferred
  (`take(None(), 1)`) — so the argument-transform path can push it onto
  the stack. A param that names one (`d Option[T]`) is pushed with the
  call's type arguments substituted, once explicit type arguments or
  the other arguments fix them (`take2[int](None(), 1)`,
  `take2(None(), "x")`). Methods, generic methods and named arguments
  resolve their parameter types the same way.
- For **tuple elements** (context 4 / context 2 with tuple return),
  `transformPrimary` inspects the enclosing `returnSlot.typ`
  for a Tuple-shaped expected type and pushes each per-slot type
  onto the stack for the duration of that element's transform.
- For **match arms** (context 5), `buildMatchExpressionFromClauses`
  promotes a pending expected slot type to `returnSlot.typ`
  for the IIFE's body, so each arm's expression — not just the first
  — picks it up via the existing companion-of-sealed lookup.
- A **match or if-expression with no expected type** whose value is
  used (`val m = o match { … }`, `val m = if (b) Some(v) else None()`)
  has the type its arms or branches unify to, and that — not the
  matched value's type, and not the enclosing function's result type —
  types a bare `None()` among them. `lowerBranches` lowers each arm
  with those two fallbacks hidden (`siblingTypedBranch`); an arm that
  then cannot be typed on its own (`None()` fails with E0018) is
  lowered again, after the others, against the type they unify to. So
  inside `func pick[T any](o Option[int], v T) T`,
  `o match { case Some(_) => Some(v); case None() => None() }` is an
  `Option[T]` and the arm is `None[T]`, in either arm order. When the
  other arms unify to no fully known type, the arm is lowered again
  with the fallbacks restored, as before. A body with its own result
  slot inside an arm (a lambda, a match or if with an expected type)
  is unaffected.

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
| `transformer/call_context.go::resolveExpectedFuncArgType` | Surfaces non-FuncType param types that name none of the callee's type parameters |
| `transformer/match.go::lowerBranches` | Types a match arm or if branch with no expected type from its siblings |
| `transformer/constructors.go::transformPrimary` (paren/tuple case) | Pushes per-element expected type for tuple literals |
| `transformer/declarations.go::transformValDeclaration` / `transformVarDeclaration` | Pushes the explicit declared type before transforming RHS |
| `transformer/postfix.go::buildMatchExpressionFromClauses` | Promotes a pending slot type to `returnSlot.typ` for arm bodies |

---

## Untyped Constants in Immutable Slots

An untyped numeric constant takes the type of the slot it initialises, exactly
as a Go assignment converts it: `Entry(0)` for a field declared `int64`,
`p.Copy(ratio = 0.5)` for a `float32` field, `(1, 2)` returned as
`Tuple[int64, float32]`. Immutable slots are stored as `Immutable[T]`, and Go
infers `NewImmutable`'s `T` from its argument, where an untyped constant
collapses to its default type (`int`, `float64`, `rune`). So every site that
wraps a value for a slot whose type it knows goes through one helper,
`newImmutableFor` (`transformer/constructors.go`), which spells the slot type
(`std.NewImmutable[int64](0)`) whenever that default differs.

A Go package's untyped constant is untyped in Go exactly as a literal is, so
`math.MinInt8`, `math.Pi` and constant arithmetic over them (`math.MaxInt8 -
1`) follow the same rule; the analyzer records which Go constants are untyped
(`GoTypeInfo.UntypedConstants`). A typed constant such as `time.Second` keeps
its own type.

A slot counts as numeric when its type is a predeclared numeric type, a GALA
type declared over one (`type Millis int64`, through alias chains, including
one declared further down the file), or a Go named numeric type
(`time.Duration`). The sites are the struct constructor (positional, named,
composite literal, field defaults), Copy overrides, and tuple literals with an
expected element type (return position, call argument, typed `val`, struct
field). Only the slot the tuple literal itself fills counts: a local
`val t = (1, 2)` inside a function returning `Tuple[int64, int64]` is still a
`Tuple[int, int]`. A bare `nil` always takes the slot's declared type.

Parameter defaults follow the same rule at analysis time: `func f(ms Millis =
12)` is accepted because `Millis` is declared over `int64`, while
`ms Millis = "soon"` is still GALA-E0014.

---

## Downward Inference for Generic Function Phantom Type Parameters

The same problem arises for a generic **free function** whose type parameter
appears only in its **return type**, never in a parameter — a *phantom*
result-only param. The canonical case is an error-only constructor for a
two-parameter type:

<!-- doc-check: fragment -->
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
   then the result type of the innermost enclosing function or lambda
   (`returnSlot.typ`; inside a lambda this is the lambda's own result
   type, or unset while it is not known — never the surrounding function's).

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
- **Method return types**: `strings.NewReader("hello").Len()` → inferred from Go method signature
- **Struct field types**: `req.URL.Host` → inferred as `string`

This enables seamless interop without explicit type annotations when calling Go functions.

### Go calls that return several results

A GALA expression is one value, so a call to a Go function with several results,
used as a value, is inferred as one GALA type built from the Go signature:

| Go results | Inferred GALA type | Example |
|---|---|---|
| `(T, error)` | `Try[T]` | `strconv.Atoi(s)` → `Try[int]` |
| `(A, B, error)` … up to 10 values and an `error` | `Try[Tuple[A, B]]` … `Try[Tuple10[...]]` | `net.SplitHostPort(a)` → `Try[Tuple[string, string]]` |
| `(A, B)` … up to 10 values | `Tuple[A, B]` … `Tuple10[...]` | `strings.Cut(s, "=")` → `Tuple3[string, string, bool]` |
| `error` only | `error` | `os.Remove(p)` → `error` |

- `Try(call)` and `Try(() => call)` over such a call infer the call's own type
  (`Try[T]`, never `Try[Try[T]]`); over an error-only call they infer `Try[Void]`.
- A binding of several names (`val a, err = goCall()`, `var …`, `a, err := …`)
  gives each name the type of its Go result — `a` is `T`, `err` is `error`.
- `val (a, b, c) = strings.Cut(...)` destructures the inferred Tuple; each name
  takes its component's type (`var (a, b, c)` likewise, as reassignable variables).
- Where the plain `T` is expected but the call's `Try[T]` or Tuple is given, the
  transpiler reports GALA-E0049 instead of inferring `T`.

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
