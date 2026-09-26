# Contributing to GALA

Thank you for your interest in contributing to GALA. This guide explains how to set up
your development environment, run tests, and submit changes.

## Prerequisites

You will need the following installed on your system:

| Tool      | Version              | Purpose                                                  |
|-----------|----------------------|----------------------------------------------------------|
| Go        | 1.25+                | Host language; the transpiler uses it for Go type inference |
| Bazelisk  | latest               | Runs the Bazel version pinned in `.bazelversion` (9.2.0) |
| Git       | 2.x+                 | Version control                                          |
| JDK       | 17+ (optional)       | Only for the IntelliJ plugin target (`//ide/intellij:plugin`) |

Install Bazel through [Bazelisk](https://github.com/bazelbuild/bazelisk), **not** as a
directly installed `bazel` — only Bazelisk reads `.bazelversion`. Check with
`bazel --version`. `MODULE.bazel.lock` uses a format Bazel 8 cannot read; a directly
installed Bazel 8 silently rewrites it to an older version, dirtying your tree.

Keep `go` on your `PATH` (or set `GOROOT`). `.bazelrc` forwards both into the build so
the transpiler can resolve Go stdlib and third-party signatures. Without the Go SDK,
transpilation still succeeds but Go type inference is disabled (a warning is printed).

Bazel builds are disk-hungry, especially with several worktrees — see
[docs/BUILD_DISK_USAGE.MD](docs/BUILD_DISK_USAGE.MD) and `tools/bazel_gc.sh`.

## Setting Up the Development Environment

1. Fork and clone the repository:

   ```bash
   git clone https://github.com/<your-username>/gala.git
   cd gala
   ```

2. Verify the build works:

   ```bash
   bazel build //...
   ```

3. Run all tests:

   ```bash
   bazel test //...
   ```

4. Build the CLI binary:

   ```bash
   bazel build //cmd/gala:gala
   ```

The binary will be at `bazel-bin/cmd/gala/gala_/gala` (or `gala.exe` on Windows).

### Nix (optional)

[`flake.nix`](flake.nix) provides the same toolchain for a reproducible
environment:

```bash
nix develop       # shell with bazelisk, go, jdk21
direnv allow      # with direnv: enter the shell automatically on cd
```

`nix build .#gala` builds the CLI from source. The stdlib is transpiled by
the released `gala` binary pinned in [`nix/gala.nix`](nix/gala.nix)
(`galaReleaseVersion` and its four asset hashes). Releases can lag the tree
(codegen or grammar changes), so for local transpiler or grammar work use the
escape hatch, which builds `cmd/gala_bootstrap` from this tree instead:

```bash
nix build .#gala-local
```

The flake covers `x86_64-linux`, `aarch64-linux`, `x86_64-darwin`, and
`aarch64-darwin`.

> **NixOS:** the dev shell provides `bazel` via Bazelisk, which downloads a
> dynamically linked Bazel. On NixOS that binary needs `nix-ld` or an FHS
> wrapper; on macOS and non-NixOS Linux it runs as-is.

## Running Tests

All testing is done through Bazel:

```bash
# Run all tests
bazel test //...

# Run a specific test
bazel test //examples:hello

# Run transpiler unit tests
bazel test //internal/transpiler/...

# Show failing test output
bazel test //... --test_output=errors --verbose_failures
```

Use `bazel test`, not `go test` — CI runs Bazel, and some packages only build under it.

GALA has two categories of tests:

- **Unit tests** (Go `testing` package with `testify`): Located under `internal/` and `std/`.
  These test transpiler internals, type inference, and code generation.
- **Output comparison tests** (`gala_exec_test`): Located under `examples/`. Each `.gala` file
  has a corresponding `.out` file with expected output. The test compiles and runs the GALA
  program, then compares stdout against the `.out` file. (`gala_test` targets are tests written
  with GALA's own `test` framework.)

Pull requests run three CI jobs (`.github/workflows/test.yml`): `bazel build //...` and
`bazel test //...`; the IntelliJ plugin build (`bazel build //ide/intellij:plugin`, tagged
`manual` so `//...` skips it); and the parser and analyzer test suites under the Go race
detector (`--@rules_go//go/config:race`).

## Code Style

### Go Code (transpiler, standard library runtime)

- Add compile-time interface checks: `var _ Interface = (*Implementation)(nil)`
- Prefer generics over reflection
- Use dependency injection via constructors
- Avoid global variables and singletons
- Define custom error types with the `errors` package
- Use context for request-scoped values
- Write table-driven tests

### GALA Code (standard library, examples)

[docs/GALA_BEST_PRACTICES.MD](docs/GALA_BEST_PRACTICES.MD) is the full rule list; read it
before writing GALA. The highlights:

- Functional style, pattern matching, and immutable variables (`val` over `var`)
- Implicit typing everywhere: omit generic params, lambda param types, variable types,
  method type params, and FoldLeft accumulator types when they can be inferred
- `s"..."` / `f"..."` over `fmt.Sprintf`; `Println` / `Print` over `fmt.Println` / `fmt.Print`
- GALA collections (`Array` / `List` / `HashMap` from `collection_immutable`) over Go slices
  and maps; `go_interop` only at the Go boundary
- `Option` / `Try` / `Either` over `nil` and naked error pairs
- Sealed types instead of iota enums

### Transpiler rules

These are non-negotiable for changes under `internal/`:

- **The standard library gets no special treatment.** `std` and the other stdlib packages
  resolve through the same import mechanism as any third-party GALA library — no hardcoded
  names or special cases in the transpiler.
- **Generate concrete Go types.** Never emit `any` / `interface{}` unless the GALA source
  asked for it. If a type cannot be resolved, fail with an error.
- **Fix transpiler bugs, don't work around them.** If GALA code hits a transpiler bug, add a
  repro test and fix the transpiler rather than rewriting the GALA code to dodge it.
- **No private references in the repo.** Code, comments, tests, docs, and commit messages
  describe a problem on its own terms — no internal ticket IDs or private tracker links.

## Project Architecture

```
gala/
  cmd/
    gala/                          # CLI entry point (Cobra)
    gala_bootstrap/                # Minimal transpiler used to build the stdlib in Bazel
  internal/
    build/                         # `gala build` / `gala test` driver: transpile into a
                                   #   workspace, generate its go.mod, invoke the Go toolchain
    depman/                        # Module resolution and the gala.mod reader/writer
    interpolation/                 # s"..." / f"..." string interpolation parsing
    lsp/                           # Language server (diagnostics, hover, completion)
    stdlib/                        # Embedded standard-library snapshot + extraction
    parser/grammar/                # ANTLR4 grammar (parser is generated by Bazel)
    transpiler/                    # Pipeline overview: internal/transpiler/README.MD
      analyzer/                    # Package metadata extraction; Go type info via the Go SDK
      concurrency/                 # Data-race safety checks (Shareable, closure captures)
      infer/                       # Hindley-Milner inference (Layer 2)
      module/                      # Module/import path resolution
      profiler/                    # Optional phase timing (GALA_PROFILE)
      registry/                    # Type and function registries
      resolver/                    # Symbol resolution
      scopewalk/                   # Shared lexical-scope walker (captures, undefined symbols)
      generator/                   # Go source code generation from Go AST
      transformer/                 # GALA parse tree -> Go AST transformation
  std/                             # Standard library core (Option, Either, Try, etc.)
  collection_immutable/            # Immutable collections (Array, List, HashMap, ...)
  collection_mutable/              # Mutable collection variants
  concurrent/                      # Future, Promise, ExecutionContext
  stream/                          # Lazy sequences
  fs/ io/ json/ yaml/ regex/ ...   # Further stdlib packages, written in GALA
  go_interop/                      # Explicit Go slice/map interop
  test/                            # Test framework
  examples/                        # Verification programs (output comparison tests)
  docs/                            # Language specification and documentation
  website/                         # GitHub Pages site (Jekyll)
  ide/intellij/                    # IntelliJ IDEA plugin
  ide/claude-code/                 # Claude Code plugin (runs `gala lsp`)
  nix/, flake.nix                  # Nix dev shell and package
  tools/                           # Build helpers (bazel_gc.sh, workspace status)
```

## How to Add a New Language Feature

Adding a feature to GALA follows a pipeline. Complete each step in order:

### Step 1: Grammar (if new syntax is needed)

Edit the ANTLR4 grammar file at `internal/parser/grammar/gala.g4`. The parser is
regenerated automatically by Bazel -- no local ANTLR4 installation is needed:

```bash
# Bazel regenerates the parser from gala.g4 automatically
bazel build //internal/parser/grammar:gala_grammar_go
```

**Important:** Never hand-edit or check in the generated parser `.go` files. Bazel
regenerates them from `gala.g4` on every build; `gala.g4` is the only source of truth.

Grammar changes also reach the IDE tooling: the IntelliJ plugin is generated from the same
grammar (`bazel build //ide/intellij:plugin` regenerates and tests it), and new keywords or
built-in types must be synced into the plugin and LSP server as described in
[ide/intellij/SYNC.md](ide/intellij/SYNC.md).

### Step 2: Analyzer

If your feature introduces new types, declarations, or resolution rules, update the
analyzer in `internal/transpiler/analyzer/`. The analyzer extracts metadata from the
parse tree (struct fields, sealed type variants, method signatures, etc.) that the
transformer uses later.

### Step 3: Transformer

Implement the GALA-to-Go AST transformation in `internal/transpiler/transformer/`.
This is where GALA semantics are converted to Go semantics. Key files:

- `transformer.go` -- main visitor, dispatches to specialized handlers
- `match.go` -- pattern matching
- `sealed.go` -- sealed type generation
- `type_inference.go` -- type inference engine
- `lambdas.go` -- lambda expressions and closures
- `calls.go` -- function and method call transformation

### Step 4: Generator (if needed)

If the Go AST output needs new rendering logic, update `internal/transpiler/generator/`.
Most features do not require generator changes since Go AST nodes map directly to Go source.

### Step 5: Tests

Every new feature requires tests at two levels:

1. **Unit tests** -- Add Go test cases in the relevant `*_test.go` file under `internal/`.
   Use table-driven tests with `testify` assertions.

2. **Output comparison tests** -- Add a `.gala` file and corresponding `.out` file in
   `examples/`. This verifies end-to-end correctness.

### Step 6: Documentation

Update the relevant documentation files:

- `docs/GALA.MD` -- Language reference (required for all syntax changes)
- `docs/TYPE_INFERENCE.MD` -- If type inference rules changed
- `docs/EXAMPLES.MD` -- Add a concise example of the feature

### Step 7: Pre-Commit Verification

Before submitting, complete these in order:

```bash
bazel build //...
bazel test //...
bazel run //:gazelle       # if files were added or removed
```

- `bazel run //:gazelle` manages BUILD files for Go, GALA, and mixed GALA+Go packages;
  prefer it over hand-writing `gala_library` / `gala_binary` / `gala_test` targets. It
  needs a `gala` CLI on `PATH` recent enough to support `gala imports --json`.
- Every program in `examples/` must still compile (covered by `bazel test //...`).
- Documentation is updated for any grammar or feature change (Step 6).

## Submitting a Pull Request

1. Create a feature branch from `master`. Pick a descriptive slug — see
   [Branch naming](#branch-naming) for the convention:

   ```bash
   git checkout -b feat/my-feature master
   ```

2. Make your changes, following the steps above.

3. Ensure all builds and tests pass:

   ```bash
   bazel build //...
   bazel test //...
   ```

4. Write a clear commit message in [Conventional Commits](https://www.conventionalcommits.org/)
   style — a type prefix, optionally with a scope (`fix(lsp): ...`, `feat(nix): ...`):

   - `feat:` -- new feature
   - `fix:` -- bug fix
   - `docs:` -- documentation only
   - `refactor:` -- code restructuring with no behavior change
   - `perf:` -- performance improvement
   - `test:` -- adding or updating tests
   - `build:` / `ci:` -- Bazel, toolchain, or CI workflow changes
   - `chore:` -- maintenance (version bumps, housekeeping)

5. Run the project's review passes over your change. They are Claude Code skills in
   `.claude/skills` and built-in commands:
   - `/gala-lint` on the changed `.gala` files
   - `/simplify` for reuse, simplification, and efficiency cleanups
   - `/review` for correctness

   Fix each finding or explain in the PR why it doesn't apply.

6. Push and open a pull request against `master`.

7. In the PR description, include:
   - What the change does and why
   - Example GALA code demonstrating the feature (if applicable)
   - Which documentation files were updated
   - Any breaking change, and who it affects

   Keep it accurate: every claim should be true of the diff, and every notable change in
   the diff should be mentioned.

8. Wait for CI to be green on your final commit. A PR is ready for review only when
   every check has passed. "No checks reported" usually means the branch conflicts with
   `master`; rebase so the checks can run.

## Branch naming

Use a `<type>/<slug>` shape — type prefix matches the commit prefix from
the previous section, slug is a short kebab-case description. Examples:

```
feat/yaml-codec
fix/copy-chained-field-access
fix/cross-module-apply-lowering
refactor/audit-systemic-correctness
test/hex-literal-parser-regression
docs/gala-deps-vs-deps
```

When the work corresponds to a public GitHub issue or PR, you may include
its number in the slug so the branch is grep-able later:

```
fix/PR260-panic-as-match-arm
fix/issue142-windows-path-normalization
```

**Avoid sequence-number-only slugs** like `fix/FIX-001-...`. Per-author or
per-batch counters get reused, so two unrelated branches end up with the
same ID and can't be told apart in `git log` or PR titles. Without a GitHub
issue/PR number to anchor against, write a slug that identifies the bug on
its own (`fix/null-pointer-in-immutable-unwrap`, not `fix/FIX-001`).

This is a recommendation, not enforced by tooling. Pick what reads best
to a future reviewer searching `git log` for the change.

## Reporting Bugs

**Security issues are not bug reports.** If the problem has security impact, do not
open a public issue — follow [SECURITY.md](SECURITY.md) and report it privately
through GitHub security advisories.

When filing a bug report, include:

- GALA source code that reproduces the issue (minimal example)
- Expected behavior
- Actual behavior (error message or incorrect output)
- GALA version (`gala version`) and OS

## Questions?

Open a GitHub Discussion or Issue if you have questions about contributing.
