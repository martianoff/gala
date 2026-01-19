# GALA Type Inference and Unwrapping

GALA implements a custom type inference and unwrapping system to handle its unique immutability model while transpiling to Go.

## Overview

GALA distinguishes between immutable variables (`val`) and mutable variables (`var`). In the generated Go code:
- `val` variables are represented as `std.Immutable[T]`.
- `var` variables are represented as their raw types `T`.

The transpiler is responsible for automatically wrapping values into `std.Immutable` and unwrapping them using the `.Get()` method.

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

## Type Inference Logic

The core of GALA's type inference is the `getExprTypeName` method in `internal/transpiler/transformer/types.go`. It attempts to determine the type name of an expression as a string.

### Supported Expressions
- **Identifiers**: Looks up the type in the current scope.
- **Literals**: Explicitly inferred (e.g., `1` -> `int`, `"foo"` -> `string`).
- **Function Calls**: Uses return type information from the `Functions` metadata in `RichAST`.
- **Struct Fields**: Uses field type information from the `Types` metadata in `RichAST`.
- **Selector Expressions**: Handles package-qualified names and struct fields.
- **`Immutable` Operations**:
    - `expr.Get()`: Returns the inner type of the `Immutable`.
    - `NewImmutable(expr)`: Returns the type of the `expr`.
- **Built-in Types**: Handles `Option`, `Either`, and `Tuple` by checking for specific function call patterns (e.g., `std.Some()`, `std.Left()`).

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

## Limitations and Edge Cases

- **String-based matching**: Type checks rely heavily on string prefixes and names, which can be fragile when dealing with type aliases or complex generic types.
- **Recursive Unwrapping**: `unwrapImmutable` currently performs only one level of unwrapping. But multiple wrapping should never happen in the first place
