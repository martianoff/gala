# GALA Project - Claude Code Instructions

GALA is a programming language that transpiles to Go. Build system: Bazel.

---

## CRITICAL RULES (NEVER VIOLATE)

1. **NEVER modify `internal/parser/grammar/*.go`** - These are ANTLR-generated. Changes will be overwritten.

2. **NEVER give special treatment to `std` library** - The standard library MUST use the same import/resolution mechanisms as any other GALA library. No hardcoding, no special cases in the transpiler.

3. **ALWAYS generate concrete types** - GALA is type-safe. The transpiler MUST generate concrete Go types, NEVER `any`/`interface{}` unless explicitly requested in GALA source. If type cannot be resolved, fail with an error.

4. **ALWAYS use bazel for testing, compilation** - Both GO and GALA have build-in bazel actions for compilation and testing.

5. **ALWAYS research GALA syntax and best practices from docs/GALA.md before writing GALA code** - GALA is a functional language that extensively relies on pattern matching.
---

## Project Structure

| Directory | Purpose |
|-----------|---------|
| `internal/parser/grammar` | ANTLR4 grammar (DO NOT edit `*.go` files) |
| `internal/transpiler/generator` | Go code generation from AST |
| `internal/transpiler/transformer` | GALA AST to Go AST transformation |
| `std` | Standard library (written in GALA) |
| `test` | Test framework |
| `examples` | Verification programs |
| `docs` | Documentation |

---

## Commands Reference

| Task | Command |
|------|---------|
| Build | `bazel build //...` |
| Test | `bazel test //...` |
| Generate BUILD files | `bazel run //:gazelle` |

**Update Go dependencies (run in order):**
```shell
go mod tidy && bazel run //:gazelle && bazel run //:gazelle-update-repos && bazel run //:gazelle && bazel mod tidy
```

---

## Code Style

### Go Code

- Add compile-time interface checks: `var _ Interface = (*Implementation)(nil)`
- Prefer generics over reflection
- Use dependency injection via constructors
- Avoid global variables and singletons
- Define custom error types with the errors package
- Use context for request-scoped values

### GALA Code

- Prefer: functional style, pattern matching, immutable variables
- Prefer: generics, type-safe programming
- Prefer: implicit `var`/`val` declarations (omit types when inferrable)
- Prefer: table-driven tests

---

## Testing

- Use Go `testing` package with `testify` assertions
- Write table-driven tests
- Use multi-line strings for test inputs requiring newlines
- **When adding GALA language features:** MUST add verification example in `examples/`

---

## Documentation

When modifying grammar or adding features, update:
- `docs/GALA.MD` - Language reference
- `docs/TYPE_INFERENCE.md` - Type inference rules
- `docs/examples.MD` - Feature examples (use concise functional syntax)

---

## Pre-Commit Checklist

MUST complete ALL steps in order before considering work done:

1. [ ] `bazel build //...` passes
2. [ ] `bazel test //...` passes
3. [ ] `bazel run //:gazelle` (if files added/removed)
4. [ ] Examples in `examples/` compile without errors
5. [ ] Documentation updated (if grammar/features changed)
