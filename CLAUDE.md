# Claude Code Rules for GALA Project

## Project Overview

This is the GALA programming language transpiler - a language that compiles to Go. The project uses Bazel for building and testing.

## Project Structure

Maintain this folder structure:
- `internal/parser/grammar` - GALA grammar in ANTLR4 format
- `internal/transpiler/generator` - Generate Go code from GALA AST tree
- `internal/transpiler/transformer` - Transform GALA AST tree to Go AST tree
- `std` - GALA standard library written in GALA (common classes/functions, e.g., `Immutable` class)
- `test` - GALA test framework
- `examples` - Example GALA programs for verification
- `docs` - Documentation files

## Strict Rules

**DO NOT modify generated files in `internal/parser/grammar/*.go`** - These are ANTLR-generated files.

**Standard library (`std`) must not receive special treatment** - GALA code in `std` must be processed through the same import/resolution mechanisms as any other GALA library. Do not hardcode or give special treatment to std library code in the transpiler.

**GALA is a type-safe language** - Transpiler should always generate concrete types instead of "any", unless "any" is explicitly asked in GALA code, and fail if it is not possible.

## Building and Testing

### Running Tests
```shell
bazel test //...
```

### Building the Project
```shell
bazel build //...
```

### Generating BUILD Files
```shell
bazel run //:gazelle
```

### Updating Go Dependencies
```shell
go mod tidy
bazel run //:gazelle
bazel run //:gazelle-update-repos
bazel run //:gazelle
bazel mod tidy
```

## Code Style Guidelines

### Go Code
- Follow Go best practices
- For each interface implementation, add a compile-safe validator:
  ```go
  var _ Interface = (*Implementation)(nil)
  ```
- Prefer generics over reflection where possible
- Use dependency injection with explicit construction - pass dependencies via constructors
- Avoid global variables or singletons
- Use interfaces to define dependencies for better testability
- Define custom error types for different error categories using the errors package
- Use context for request-scoped values and cancellation

### GALA Code
- Prefer functional code, pattern matching, and immutable variables
- Prefer generic methods and type-safe programming
- Prefer implicit var/val declarations
- Prefer table driven tests
- Do not provide explicit types when it is not required

## Testing Requirements

- Write unit tests for business logic
- Use Go's `testing` package and `testify` for assertions
- Prefer table-driven tests
- Use multi-line input when new lines are required in test cases
- When adding new GALA language features or semantic changes, verify with a new example in the `examples` folder

## Documentation Updates

When making changes to the grammar or adding new features, update:
- `docs/GALA.MD` - Language documentation
- `docs/TYPE_INFERENCE.md` - Type inference documentation
- `docs/examples.MD` - Examples (prefer short functional syntax to demonstrate benefits over Go)

## Before Submitting Changes

1. Ensure the project builds: `bazel build //...`
2. Run all tests: `bazel test //...`
3. Regenerate BUILD files if needed: `bazel run //:gazelle`
4. Verify examples in `examples/` folder are executable without compiler errors
