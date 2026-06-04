# GALA Project - Claude Code Instructions

**Environment:** If `~/.claude/CLAUDE.md` (global instructions) exists, follow its Environment, Shell, and File Path conventions. Those global settings override any defaults in this file.

GALA is a programming language that transpiles to Go. Build system: Bazel.

---

## CRITICAL RULES (NEVER VIOLATE)

1. **NEVER modify `internal/parser/grammar/*.go`** - These are ANTLR-generated. Changes will be overwritten.

2. **NEVER give special treatment to `std` library** - The standard library MUST use the same import/resolution mechanisms as any other GALA library. No hardcoding, no special cases in the transpiler.

3. **ALWAYS generate concrete types** - GALA is type-safe. The transpiler MUST generate concrete Go types, NEVER `any`/`interface{}` unless explicitly requested in GALA source. If type cannot be resolved, fail with an error.

4. **ALWAYS use bazel for testing, compilation** - Both GO and GALA have build-in bazel actions for compilation and testing.

5. **ALWAYS research GALA syntax and best practices before writing GALA code** - Start with [docs/GALA_BEST_PRACTICES.MD](docs/GALA_BEST_PRACTICES.MD) for the rule list; fall back to [docs/GALA.MD](docs/GALA.MD) for full specification details. GALA is a functional language that extensively relies on pattern matching.

6. **NEVER try to find workaround if you found a transpiler bug, you must create a repro test case and fix the transpiler issue before moving forward**

7. **NEVER put internal references to internal bug reports in the codebase** - Do not reference internal issue numbers, ticket IDs, incident tags, private bug-tracker links, or other internal-only identifiers in source code, tests, comments, docs, commit messages, or any other file checked into the repo. Describe the problem and fix on their own terms.
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
| Test (verbose) | `bazel test //... --test_output=errors --verbose_failures` |
| Test single target | `bazel test //examples:match_type_inference` |
| Generate BUILD files | `bazel run //:gazelle` (covers Go, GALA, and mixed GALA+GO packages) |

**Update Go dependencies (run in order):**
```shell
go mod tidy && bazel run //:gazelle && bazel run //:gazelle-update-repos && bazel run //:gazelle && bazel mod tidy
```

### Managing BUILD files with gazelle

`bazel run //:gazelle` is the recommended way to generate and maintain BUILD
files. A single pass now manages Go, GALA, and mixed GALA+GO packages — prefer
it over hand-authoring `gala_library` / `gala_binary` / `gala_test` targets.

GALA support ships from rules-gala as the `gala_gazelle` Bazel module (consumed
via `bazel_dep`); it uses the `gala imports` CLI subcommand as a parse-only
import helper. Steer it with `# gazelle:` directives in your BUILD files — set
`gala_prefix` to your module's import prefix so generated `deps` resolve
correctly. The full directive reference (`gala_prefix`, `gala_helper`,
`gala_stdlib_prefix`, `gala_stdlib_repo`, `gala_implicit_dep`) lives in
rules-gala's `gazelle/README.md`.

### Prerequisites

- **Bazel** (via Bazelisk): install from https://github.com/bazelbuild/bazelisk
- **Go SDK**: required for Go type inference. Ensure `go` is on PATH or set `GOROOT`.

### How Bazel finds the Go SDK

The `.bazelrc` file includes `--action_env=GOROOT` and `--action_env=PATH` so that
transpilation genrules can access the Go SDK for type inference (resolving function
return types, struct fields, method signatures from Go packages). The genrules also
use `tags = ["no-sandbox"]` in the rules_gala transpile rules to allow filesystem
access to the SDK.

If the Go SDK is not found, transpilation still succeeds but Go type inference is
disabled (a warning is printed to stderr). This only affects type resolution for
Go stdlib/third-party packages — GALA's own type system works without it.

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

See [docs/GALA_BEST_PRACTICES.MD](docs/GALA_BEST_PRACTICES.MD) for the full rule list (variables, type inference, collections, pattern matching, lambdas, strings, error handling, naming, performance). Top-level highlights:

- Functional style, pattern matching, immutable variables (`val` over `var`); generics over reflection
- Implicit typing everywhere — omit generic params, lambda param types, variable types, method type params, and FoldLeft accumulator types
- `s"..."` / `f"..."` over `fmt.Sprintf`; `Println`/`Print` over `fmt.Println`/`fmt.Print` (no `fmt` import needed)
- GALA collections (`Array`/`List`/`HashMap` from `collection_immutable`) over Go slices/maps; `go_interop` only for Go interop
- `Option`/`Try`/`Either` over `nil` and naked error pairs
- Table-driven tests

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
3. [ ] `bazel run //:gazelle` (if files added/removed — manages Go, GALA, and mixed GALA+GO BUILD files)
4. [ ] Examples in `examples/` compile without errors
5. [ ] Documentation updated (if grammar/features changed)
