# GALA Project - Claude Code Instructions

**Environment:** If `~/.claude/CLAUDE.md` (global instructions) exists, follow its Environment, Shell, and File Path conventions. Those global settings override any defaults in this file.

GALA is a programming language that transpiles to Go. Build system: Bazel.

---

## CRITICAL RULES (NEVER VIOLATE)

1. **NEVER modify `internal/parser/grammar/*.go`** - These are ANTLR-generated. Changes will be overwritten.

2. **NEVER give special treatment to `std` library** - The standard library MUST use the same import/resolution mechanisms as any other GALA library. No hardcoding, no special cases in the transpiler.

3. **ALWAYS generate concrete types** - GALA is type-safe. The transpiler MUST generate concrete Go types, NEVER `any`/`interface{}` unless explicitly requested in GALA source. If type cannot be resolved, fail with an error.

4. **ALWAYS use bazel for testing, compilation** - Both GO and GALA have built-in bazel actions for compilation and testing.

5. **ALWAYS research GALA syntax and best practices before writing GALA code** - Start with [docs/GALA_BEST_PRACTICES.MD](docs/GALA_BEST_PRACTICES.MD) for the rule list; fall back to [docs/GALA.MD](docs/GALA.MD) for full specification details. GALA is a functional language that extensively relies on pattern matching.

6. **NEVER try to find workaround if you found a transpiler bug, you must create a repro test case and fix the transpiler issue before moving forward**

7. **NEVER put internal references to internal bug reports in the codebase** - Do not reference internal issue numbers, ticket IDs, incident tags, private bug-tracker links, or other internal-only identifiers in source code, tests, comments, docs, commit messages, or any other file checked into the repo. Describe the problem and fix on their own terms.
---

## Project Structure

| Directory | Purpose |
|-----------|---------|
| `internal/parser/grammar` | ANTLR4 grammar (DO NOT edit `*.go` files) |
| `internal/transpiler/analyzer` | Package metadata extraction; Go type info via the Go SDK |
| `internal/transpiler/infer` | Hindley-Milner inference (Layer 2) |
| `internal/transpiler/transformer` | GALA AST to Go AST transformation |
| `internal/transpiler/generator` | Go code generation from AST |
| `internal/lsp` | Language server (`gala lsp`) |
| `internal/build` | `gala build` / `gala test` driver |
| `std` | Standard library (written in GALA) |
| `test` | Test framework |
| `examples` | Verification programs |
| `docs` | Documentation (mirrored for the site in `website/`) |
| `ide/intellij`, `ide/claude-code` | IntelliJ plugin; Claude Code plugin (ships `/gala-lint`) |
| `.claude/skills` | Project skills: `gala-code`, `gala-lint`, `gala-pr-review`, `gala-ide-sync`, `gala-lsp` |

The full layout and the feature-by-feature workflow are in [CONTRIBUTING.MD](CONTRIBUTING.MD).

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

Two things to know:
- The helper needs a `gala` on `PATH` new enough to support `gala imports
  --json` (or point at one with `# gazelle:gala_helper`); an older CLI fails
  with `unknown flag: --json` and no GALA deps get resolved.
- Packages that mix hand-written `.go` with `.gala` sources are intentionally
  left to manual `gala_bootstrap_transpile` + `go_library` wiring — gazelle
  detects the mix and backs off rather than generating a `gala_library`.

### Prerequisites

- **Bazel** (via Bazelisk): install from https://github.com/bazelbuild/bazelisk.
  Use Bazelisk, **not** a directly installed `bazel` — only Bazelisk reads
  `//.bazelversion`, which pins 9.2.0. Check with `bazel --version`.

  This matters concretely. `MODULE.bazel.lock` is lockFileVersion 28, which
  Bazel 8 cannot read: a directly installed Bazel 8 silently discards it and,
  under the default `--lockfile_mode=update`, **rewrites the lockfile back to
  version 16**, dirtying the tree and flip-flopping the file depending on who
  built last. The pin is about reproducibility, not disk — see
  [docs/BUILD_DISK_USAGE.MD](docs/BUILD_DISK_USAGE.MD) for why Bazel 9's repo
  contents cache does not reduce the per-worktree duplication.
- **Go SDK**: required for Go type inference. Ensure `go` is on PATH or set `GOROOT`.
  Bazel compiles with its own pinned Go (`go_sdk.download` in `MODULE.bazel`,
  1.25.5), downloaded per output base; the host Go is only used for type inference.

Builds are disk-hungry and need occasional maintenance — see
[docs/BUILD_DISK_USAGE.MD](docs/BUILD_DISK_USAGE.MD) and `tools/bazel_gc.sh`.

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
- Use `bazel test`, not `go test` — CI runs Bazel. CI also runs the parser and analyzer
  suites under the race detector (`--@rules_go//go/config:race`); run them that way when
  touching concurrent code.

---

## Documentation

When modifying grammar or adding features, update:
- `docs/GALA.MD` - Language reference
- `docs/TYPE_INFERENCE.MD` - Type inference rules
- `docs/EXAMPLES.MD` - Feature examples (use concise functional syntax)
- The matching page under `website/docs/` — the site is a hand-maintained mirror, not
  generated from `docs/`
- New diagnostic codes need a page in both `docs/errors/` and `website/docs/errors/`
- Grammar, keyword, or stdlib type changes: re-sync the IDE plugin and LSP data (`/gala-ide-sync`)

---

## Pre-Commit Checklist

MUST complete ALL steps in order before considering work done:

1. [ ] `bazel build //...` passes
2. [ ] `bazel test //...` passes
3. [ ] `bazel run //:gazelle` (if files added/removed — manages Go, GALA, and mixed GALA+GO BUILD files)
4. [ ] Examples in `examples/` compile without errors
5. [ ] Documentation updated (if grammar/features changed)

## Pull Request Checklist

EVERY pull request MUST go through all of these before it is considered ready:

1. [ ] Pre-commit checklist above is complete
2. [ ] `/gala-lint` on the changed `.gala` files — every finding fixed or explicitly justified
3. [ ] `/simplify` on the change — reuse, simplification, and efficiency cleanups applied
4. [ ] `/review` on the change — every correctness finding fixed or explicitly justified
5. [ ] The PR description matches the actual diff: every claim is true, every notable
       change (including breaking changes) is mentioned
6. [ ] **CI is all green on the final commit.** This is the last gate. Re-check after every
       push. "No checks reported" usually means the branch conflicts with `master`, not
       that it passed — rebase and wait for the checks to run.

To review someone else's PR, use `/gala-pr-review`.
