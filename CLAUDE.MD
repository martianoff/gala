# GALA Project - Claude Code Instructions

**Environment:** If `~/.claude/CLAUDE.md` (global instructions) exists, follow its Environment, Shell, and File Path conventions. Those global settings override any defaults in this file.

GALA is a programming language that transpiles to Go. Build system: Bazel.

---

## CRITICAL RULES (NEVER VIOLATE)

1. **NEVER modify `internal/parser/grammar/*.go`** - These are ANTLR-generated. Changes will be overwritten.

2. **NEVER give special treatment to `std` library** - The standard library MUST use the same import/resolution mechanisms as any other GALA library. No hardcoding, no special cases in the transpiler.

3. **ALWAYS generate concrete types** - GALA is type-safe. The transpiler MUST generate concrete Go types, NEVER `any`/`interface{}` unless explicitly requested in GALA source. If type cannot be resolved, fail with an error.

4. **ALWAYS use bazel for testing, compilation** - Both GO and GALA have build-in bazel actions for compilation and testing.

5. **ALWAYS research GALA syntax and best practices from docs/GALA.MD before writing GALA code** - GALA is a functional language that extensively relies on pattern matching.

6. **NEVER try to find workaround if you found a transpiler bug, you must create a repro test case and fix the transpiler issue before moving forward**
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
- Prefer: implicit typing everywhere — omit generic params (`Some(42)` not `Some[int](42)`), lambda param types (`(x) => x * 2` not `(x int) => x * 2`), variable types (`val x = 42` not `val x int = 42`), and method type params (`list.Map((x) => x * 2)` not `list.Map[int]((x) => x * 2)`)
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
- `docs/TYPE_INFERENCE.MD` - Type inference rules
- `docs/EXAMPLES.MD` - Feature examples (use concise functional syntax)

---

## Pre-Commit Checklist

MUST complete ALL steps in order before considering work done:

1. [ ] `bazel build //...` passes
2. [ ] `bazel test //...` passes
3. [ ] `bazel run //:gazelle` (if files added/removed)
4. [ ] Examples in `examples/` compile without errors
5. [ ] Documentation updated (if grammar/features changed)
