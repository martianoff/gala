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
9. [Limitations and Edge Cases](#limitations-and-edge-cases)
10. [Key Files](#key-files)

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
| `expr.Get()` | Immutable inner type |
| `NewImmutable(x)` | Immutable wrapper type |

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
