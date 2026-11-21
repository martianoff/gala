# GALA Dependency Management

GALA provides a dependency management system similar to Go modules, using `gala.mod` and `gala.sum` files to track dependencies and their checksums. The key difference from Go is that **GALA projects only need `gala.mod`** - no `go.mod` is required in your project folder.

---

## Table of Contents

1. [Quick Start](#1-quick-start)
2. [File Formats](#2-file-formats)
   - [gala.mod](#galamod)
   - [gala.sum](#galasum)
3. [CLI Commands](#3-cli-commands)
   - [gala build](#gala-build)
   - [gala run](#gala-run)
   - [gala test](#gala-test)
   - [gala clean](#gala-clean)
   - [gala doctor](#gala-doctor)
   - [gala export](#gala-export)
   - [gala stdlib export](#gala-stdlib-export)
   - [gala mod init](#gala-mod-init)
   - [gala mod add](#gala-mod-add)
   - [gala mod remove](#gala-mod-remove)
   - [gala mod update](#gala-mod-update)
   - [gala mod tidy](#gala-mod-tidy)
   - [gala mod graph](#gala-mod-graph)
   - [gala mod verify](#gala-mod-verify)
4. [Build System](#4-build-system)
   - [Without Bazel](#without-bazel)
   - [With Bazel](#with-bazel)
5. [Cache Structure](#5-cache-structure)
6. [Go Dependencies](#6-go-dependencies)
7. [GALA Library Dependencies](#7-gala-library-dependencies)
8. [Bazel Integration](#8-bazel-integration)
   - [MODULE.bazel (Bzlmod)](#modulebazel-bzlmod)
   - [BUILD Files](#build-files)
9. [Version Constraints](#9-version-constraints)

---

## 1. Quick Start

### Non-Bazel Projects (Recommended for simple projects)

Initialize and build a GALA project:

```bash
# Create a new project
mkdir myproject && cd myproject

# Initialize gala.mod
gala mod init github.com/user/myproject

# Add dependencies
gala mod add github.com/example/gala-utils@v1.2.3
gala mod add github.com/google/uuid@v1.6.0 --go

# Create your GALA source file
cat > main.gala <<'EOF'
package main

import "fmt"
import "github.com/google/uuid"

func main() {
    fmt.Println("Hello from GALA!")
    fmt.Println("UUID:", uuid.New().String())
}
EOF

# Build and run
gala build
./myproject
```

Your project structure (no go.mod needed!):

```
myproject/
  gala.mod          # Module manifest
  gala.sum          # Dependency checksums
  main.gala         # Your GALA code
  myproject.exe     # Built binary (after gala build)
```

### Bazel Projects

For Bazel projects, `gala mod tidy` generates a portable `go.mod` (and `go.sum` when Go dependencies are present):

```bash
# Initialize
gala mod init github.com/user/myproject

# Add dependencies
gala mod add github.com/example/gala-utils@v1.2.3
gala mod add github.com/google/uuid@v1.6.0 --go

# Create MODULE.bazel, .bazelrc, and the root BUILD.bazel first
# (see "Bazel Integration" below). 'gala mod tidy' detects a Bazel
# project by the presence of MODULE.bazel — without it, no go.mod
# is generated.

# Sync dependencies (generates portable go.mod for Bazel)
gala mod tidy

# Generate BUILD targets, then build
bazel run //:gazelle
bazel build //:myapp
```

---

## 2. File Formats

### gala.mod

The `gala.mod` file declares your module's path, GALA version, and dependencies.

```
module github.com/user/myproject

gala 1.0

require (
    github.com/example/utils v1.2.3
    github.com/example/math  v2.0.0
    github.com/google/uuid   v1.6.0 // go
)

require (
    github.com/example/indirect v1.0.0 // indirect
)

replace github.com/example/utils => ../local-utils

exclude github.com/example/deprecated v0.9.0
```

#### Directives

| Directive | Description |
|-----------|-------------|
| `module` | Declares the module path |
| `gala` | Minimum GALA version required |
| `require` | Declares a dependency |
| `replace` | Substitutes a module with another (local or remote) |
| `exclude` | Prevents a specific version from being used |

#### Comments

| Comment | Meaning |
|---------|---------|
| `// indirect` | Transitive dependency (not directly imported) |
| `// go` | Go package (not GALA) - will not be transpiled |

### gala.sum

The `gala.sum` file contains cryptographic checksums for each dependency to ensure integrity.

```
github.com/example/utils v1.2.3 h1:abc123def456...
github.com/example/utils v1.2.3/gala.mod h1:xyz789...
github.com/google/uuid v1.6.0 h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=
```

Each line contains:
- Module path
- Version
- Optional file suffix (e.g., `/gala.mod`)
- Hash scheme and SHA-256 hash. For a module line, `h2:` covers every regular file of the module except VCS metadata (`.git` and the like), byte for byte, so data files a build embeds are verified too; symbolic links are not part of a module and are not fetched. A fetch records the files it stored, and the hash covers exactly those: a build never writes into the module cache, and a source file added to a cached module afterwards fails `gala mod verify`. `h1:` is the earlier module scheme and covers only `.gala`/`.go` sources, `gala.mod`, `go.sum` and `BUILD.bazel`; existing `h1:` module lines still verify, and `gala mod tidy` rewrites them as `h2:`. `/gala.mod` lines hash that one file and keep the `h1:` prefix.

> **Compatibility:** gala versions before `h2:` reject a `gala.sum` that contains an `h2:` line ("invalid hash format"). Their `gala mod verify` fails, and their `gala mod add` / `gala mod update` rewrite `gala.sum` from scratch, dropping every other entry. Everyone working on a project, and its CI, should use a gala that understands `h2:` before `gala mod tidy` rewrites its `gala.sum`. `gala build` does not read `gala.sum` and is unaffected. Current gala accepts any `h<N>:` scheme when reading `gala.sum`, and stops instead of rewriting a `gala.sum` it cannot parse.

---

## 3. CLI Commands

### gala build

Build a GALA project to a binary.

```bash
# Build with default output name (project directory name)
gala build

# Build with custom output name (no .exe added)
gala build -o myapp

# Cross-compile: GOOS/GOARCH pass through to go build
GOOS=linux GOARCH=arm64 gala build -o myapp-linux-arm64

# Build with verbose output
gala build -v
```

**What happens:**
1. Transpiles `.gala` files to Go in a workspace at `~/.gala/build/<hash>/`
2. Downloads Go dependencies to `~/.gala/go/pkg/mod/`
3. Runs `go build` to produce the binary in your project directory

**Output name**: as with `go build`, a `-o` name never gets an extension added. The default name gets `.exe` only when the target OS (`GOOS`, or the host when unset) is Windows.

**Auto-fetch**: `gala build` automatically downloads GALA dependencies listed in `gala.mod` that are not yet cached locally. No manual `gala mod download` step is needed.

### gala run

Build and run a GALA project.

```bash
# Build and run
gala run

# Pass arguments to the program
gala run -- arg1 arg2

# Verbose mode
gala run -v
```

### gala test

Run the tests of a GALA project: every `TestXxx(t T) T` function in its `_test.gala` files, in the root package and in every subpackage.

```bash
# Test the current project
gala test

# Test another directory
gala test ./myproject

# Print every test's output, not only that of failing packages
gala test -v

# Also write a JUnit XML report
gala test --junit build/test-results/junit.xml
```

Every run ends with a summary: the passed, failed and skipped tests of each package (subtests count as tests), then each failure with its reason:

```text
=== SUMMARY ===
FAIL  example.com/shop           3 passed, 1 failed, 1 skipped
ok    example.com/shop/pricing   2 passed, 0 failed, 0 skipped

Failures:
  example.com/shop: TestTotal
      expected 30, got 25

5 passed, 1 failed, 1 skipped in 2 package(s)
```

**`--junit <path>`** writes the results as a JUnit XML report, which GitHub Actions, GitLab and Jenkins read natively. The report has one `<testsuite>` per package, named by its import path, and one `<testcase>` per test, subtests included under their full name (`TestTable/large`). A failing test carries a `<failure>` with its first error as the message and its output as the text; a skipped test carries `<skipped>`. A package that does not compile is reported as an `<error>` test case named `[build failed]` with the compiler output, so it is never silently missing from the report. The report is written whether the tests pass or fail, and the directory is created if needed.

In GitHub Actions, upload the report as an artifact, and run the step even when the tests fail:

```yaml
- name: Test
  run: gala test --junit test-results/junit.xml

- name: Upload test report
  if: ${{ !cancelled() }}
  uses: actions/upload-artifact@v7
  with:
    name: junit-report
    path: test-results/junit.xml
```

To show the results on the run's page, a JUnit reporter action such as [`mikepenz/action-junit-report@v6`](https://github.com/mikepenz/action-junit-report) can read the same file (`report_paths: test-results/junit.xml`). In GitLab CI, list the file under `artifacts: reports: junit:`.

### gala clean

Clean build artifacts.

```bash
# Clean the workspace for this project
gala clean

# Clean all GALA caches
gala clean --all

# Clean stale workspaces (projects that no longer exist)
gala clean --stale

# Remove the stdlib cache directories older gala versions extracted
gala clean --stale-stdlib
```

### gala doctor

Check the environment gala runs in: the Go toolchain and its version, the Go SDK used for type inference, gala's caches, the Go build cache, and, in a project, `gala.mod` and its agreement with `go.mod`. Each check prints one line marked `OK`, `WARN` or `FAIL`, and each problem comes with a fix and a link to [Troubleshooting](TROUBLESHOOTING.MD). It exits non-zero when a check fails.

```bash
gala doctor              # Check this machine and the project here
gala doctor ./myproject  # Check another project
gala doctor --json       # The same checks as JSON, for bug reports
```

### gala export

Write a GALA project as a plain Go module, so a Go program can import it with `go get` and build it with plain `go build`, without GALA.

```bash
# Export the project in the current directory
gala export --out ../mylib-go

# Under another module path (default: the project's module path)
gala export --out ../mylib-go --go-module github.com/me/mylib-go

# With a dev build: pin the standard library version, and also write a local module proxy
gala export --out ../mylib-go --stdlib-version v0.0.0-local.1 \
    --proxy ../goproxy --version v0.1.0
```

**What happens:**
1. Transpiles and compile-checks the project as `gala build` does for a library
2. Writes every package's generated and hand-written Go (no tests, no `gala.mod` or Bazel files) to `--out`, with imports of GALA's standard library rewritten to the published Go module `go.gala.fyi/stdlib`
3. Writes a `go.mod` that requires `go.gala.fyi/stdlib` at this `gala`'s version, plus every Go module the code needs, as `go mod tidy` resolves them for the build, including modules a hand-written `.go` file reaches only through another dependency. Its `go` directive is the newer of the one generated code needs and the project's own `go.mod`'s

Run it in the project root: it always exports the whole project. `--out` must be empty, and outside the project and any Bazel workspace. `--go-module` must be a path the go command can download (its first element is a domain). A `main` package is exported with a warning, since Go code cannot import it. Not supported yet: packages that import another GALA module, and a `gala.mod` that `replace`s a Go module (a published module's `replace` directives do not apply to the programs that import it). Publishing the export and using it from Go: [Using GALA from a plain Go module](GO_INTEROP.MD#part-5-using-gala-from-a-plain-go-module).

A Go module that commits the output of a few `.gala` files, rather than exporting a whole project, uses `gala transpile --stdlib-module go.gala.fyi/stdlib` instead: it writes the same standard library imports for each generated file. See [Committing transpiled files to a plain Go module](GO_INTEROP.MD#committing-transpiled-files-to-a-plain-go-module).

### gala stdlib export

Write GALA's standard library, as embedded in this `gala` binary, as one plain Go module. GALA releases publish it as `go.gala.fyi/stdlib`; use this command to try a build before it is released.

```bash
gala stdlib export --go-module go.gala.fyi/stdlib --out ../stdlib-go \
    --proxy ../goproxy --version v0.0.0-local.1
```

`--proxy` also writes the module, as version `--version`, in the layout `GOPROXY=file://<dir>` serves.

### gala mod init

Initialize a new `gala.mod` file.

```bash
# Auto-detect module path from go.mod or directory name
gala mod init

# Explicit module path
gala mod init github.com/user/project
```

Creates:
- `gala.mod` with module declaration
- Empty `gala.sum` file
- A `.gala/` line in `.gitignore` (the file is created if there is none)

In a Go module, `gala.mod` also gets every `require` of `go.mod`, at the version `go.mod` names, marked `// go` (and `// indirect, go` for an indirect one). `gala build` takes Go module versions from `gala.mod`, so this keeps them the versions `go build` uses. The `replace` directives of `go.mod` are copied too: `gala build` applies those of `gala.mod`, to Go dependencies as to GALA ones. The command prints each requirement and replace it copied. A GALA standard library package is not copied; the command lists it with the reason. The copy is made once: after changing a version in `go.mod`, run `gala mod add path@version --go` to change it in `gala.mod`.

### gala mod add

Add a dependency to your module.

```bash
# Add latest version
gala mod add github.com/example/utils

# Add specific version
gala mod add github.com/example/utils@v1.2.3

# Add compatible version (caret constraint)
gala mod add github.com/example/utils@^1.0.0

# Add Go dependency (not GALA)
gala mod add github.com/google/uuid@v1.6.0 --go

# Add the latest version of a Go dependency, tagged or not
gala mod add github.com/hut8/goq@latest --go
```

The command will:
1. Fetch the module: a GALA module from Git, a Go module through the Go toolchain
2. Cache it locally
3. Update `gala.mod` with the dependency
4. Update `gala.sum` with checksums (GALA modules)

A module with no `.gala` file or `gala.mod` is a Go dependency, marked `// go`
in `gala.mod`. It resolves the way `go get` resolves it, whether or not you
pass `--go` (which only skips looking for a GALA module first): the Go
toolchain looks the version up through `GOPROXY` (honoring `GOPRIVATE`,
`GONOSUMDB` and the rest of your Go settings), checks it against the checksum
database, and downloads it into the Go module cache `gala build` uses
(`~/.gala/go/pkg/mod`). Vanity import paths (`golang.org/x/text`,
`gopkg.in/yaml.v3`) and major-version suffixes (`/v2`) work as they do in Go.
Its checksum is kept in the generated `go.sum`, so it gets no `gala.sum` line.

**Untagged modules and pseudo-versions.** A module with no tagged version
resolves to the pseudo-version of the latest commit on its default branch, as
in Go:

```
$ gala mod add github.com/hut8/goq@latest --go
Added github.com/hut8/goq@v0.0.0-20220603013715-3360bf22bea4 (Go dependency)
```

A pseudo-version (`v0.0.0-<UTC commit time>-<12-char commit hash>`, or one of
the forms that follow a tag, `vX.Y.(Z+1)-0.<time>-<hash>` and
`vX.Y.Z-pre.0.<time>-<hash>`) can also be given as the version, for Go and
GALA modules alike. For a GALA module, a pseudo-version is rejected when its
time is not its commit's time, its hash part is not a 12-character commit hash
prefix, or its major version is not the one the module path names (`/v2`).

**Pin the resolved version.** `gala mod add` resolves a query such as
`latest` and writes the version it resolved to (a tag or a pseudo-version)
into `gala.mod`. Keep that pinned version in `gala.mod` so every build uses
the same one.

### gala mod remove

Remove a dependency from your module.

```bash
gala mod remove github.com/example/utils
```

### gala mod update

Update dependencies to their latest versions. A Go dependency (`// go`)
updates to the version `go get <path>@latest` would pick, a pseudo-version for
a module with no tag. Like `go get -u`, it never moves a dependency to a lower
version: a pinned pseudo-version or prerelease newer than the latest tag
stays.

```bash
# Update all dependencies
gala mod update

# Update specific dependency
gala mod update github.com/example/utils

# Update to specific version
gala mod update github.com/example/utils@v2.0.0
```

### gala mod tidy

Synchronize `gala.mod` with your source code imports.

```bash
gala mod tidy
```

This command:
- Adds missing dependencies found in imports
- Removes unused dependencies
- Marks indirect dependencies appropriately
- **For Bazel projects**: Generates a portable `go.mod` with Go dependencies only (GALA stdlib deps are stripped). Generates `go.sum` only when Go dependencies are present

`gala mod tidy` handles both single-line and multi-line import blocks, and recognizes known GALA dependencies (like `std`, `collection_immutable`, etc.) automatically.

### gala mod graph

Print the dependency tree.

```bash
gala mod graph
```

Output:
```
github.com/user/myproject
├── github.com/example/utils@v1.2.3
│   └── github.com/example/common@v1.0.0
└── github.com/example/math@v2.0.0
```

### gala mod verify

Verify that dependencies match their checksums in `gala.sum`.

```bash
gala mod verify
```

---

## 4. Build System

GALA supports two build modes: standalone (without Bazel) and Bazel-based.

### Without Bazel

When you run `gala build`, GALA:

1. **Creates a build workspace** at `~/.gala/build/<hash>/` (hash is derived from your project path)
2. **Extracts the stdlib** to `~/.gala/stdlib/v<version>/`
3. **Transpiles GALA dependencies** — any GALA library in `gala.mod` (not marked `// go`) is transpiled from `.gala` source in the module cache to `.gen.go` files in `~/.gala/build/<hash>/deps/`
4. **Transpiles** your `.gala` files to `.gen.go` files in `~/.gala/build/<hash>/gen/` (`gala test` writes an `x_test.gala` to `x_test.gen_test.go`, which Go compiles only into that package's tests)
5. **Generates** a `go.mod` in the workspace with absolute paths to dependencies
6. **Downloads** Go dependencies to `~/.gala/go/pkg/mod/`
7. **Runs** `go build` to create the binary

**Key benefits:**
- Your project folder stays clean (no go.mod, no _gala folder, no .gen.go files)
- Dependencies are cached globally
- GALA library dependencies are automatically transpiled — no pre-compiled `.go` files needed in the module cache
- No symlinks used (works on all platforms including Windows)

### One workspace per project and command

A build workspace is a scratch tree, not a cache: each build clears `gen/` and
rewrites `go.mod` before transpiling into it. Two builds sharing one would
delete each other's files mid-build, so the workspace a command uses is chosen
to keep them apart:

- **`gala build` and `gala run`** use `~/.gala/build/<hash>/`.
- **`gala test`** uses `~/.gala/build/<hash>-test/`. The two commands generate
  different trees from the same sources — test adds a generated runner main and
  compiles a library as `package main` — so they never share one.
- `<hash>` comes from the project's path, so two checkouts of the same project
  already have separate workspaces.
- `gala clean` removes every workspace belonging to the current project.

Anything still pointed at the same workspace — two builds at once, two test
runs — is serialized by a lock. The second invocation waits for the first and,
if the wait does not clear, reports which process holds it.

A wait announces itself on stderr as soon as it begins, and again when the lock
comes free:

```
gala: waiting for the build workspace lock (pid=54321 started=2026-09-21T18:04:11Z)
gala: Give this build a workspace of its own:
  gala <command> --build-dir <dir>   (or set GALA_BUILD_DIR=<dir>)
gala: workspace lock acquired after 47s
```

Note that the language server does **not** take this lock: `gala lsp` reads
configuration paths only and never builds into a workspace, so an editor session
never contends with a build or a test run.

### Failing fast instead of waiting

A build waits up to ten minutes by default. `GALA_BUILD_LOCK_TIMEOUT` overrides
that — a Go duration (`30s`, `2m`), bare seconds (`45`), or `0` to wait forever:

```shell
GALA_BUILD_LOCK_TIMEOUT=30s gala test   # fail after 30s rather than blocking
GALA_BUILD_LOCK_TIMEOUT=0 gala build    # wait as long as it takes
```

Scripted and CI runs usually want the short form: there is no one watching to
notice the wait, and a job that sits behind a stray process burns the runner for
nothing. An unparseable value is ignored with a warning rather than failing the
build.

### Giving a build its own workspace

Set `GALA_BUILD_DIR`, or pass `--build-dir`, to move build workspaces somewhere
private:

```shell
gala build --build-dir /tmp/my-workspaces
GALA_BUILD_DIR=/tmp/my-workspaces gala test
```

This is the knob for CI matrices and any setup running several builds of one
project at once. It moves **only** the workspace: the stdlib, GALA module, and
Go module caches stay where they are, so an isolated build re-downloads nothing.
Redirecting `GALA_HOME` instead moves the caches too, and pays for it.

The directory may sit inside the project (`--build-dir ./build`, say, for a CI
cache step): `gala build`, `gala test` and `gala mod tidy` leave build
workspaces out of the project's sources. Exclude the directory from other tools
that scan the tree, such as Gazelle.

### With Bazel

For Bazel projects:

1. Run `gala mod tidy` to generate `go.mod` (and `go.sum` if Go dependencies are present)
2. GALA dependencies are handled by the bzlmod extension
3. Build with `bazel build //:target`

---

## 5. Cache Structure

All GALA caches are stored in `~/.gala/`:

```
~/.gala/
  build/                              # Build workspaces (per-project)
    <hash>/                           # Workspace for a specific project
      gen/                            # Transpiled .gen.go files from your project
      deps/                           # Transpiled GALA library dependencies
        github.com/user/lib@v1.0.0/   # Each dep gets its own directory
          math.gen.go                 # Transpiled Go output
          go.mod                      # Generated go.mod for the dep
      go.mod                          # Generated go.mod with absolute paths
      go.sum                          # Generated go.sum

  stdlib/                             # Stdlib versions
    vdev/                             # Development version
      std/                            # Standard library packages
      go_interop/
      collection_immutable/
      collection_mutable/
      concurrent/

  pkg/mod/                            # GALA package cache (source)
    github.com/example/utils@v1.2.3/  # Cached GALA modules (.gala source)

  go/pkg/mod/                         # Go package cache (GOMODCACHE)
    github.com/google/uuid@v1.6.0/    # Cached Go modules
```

| Directory | Purpose |
|-----------|---------|
| `build/<hash>/gen/` | Transpiled `.gen.go` files from your project |
| `build/<hash>/deps/` | Transpiled GALA library dependencies |
| `stdlib/v<version>/` | GALA standard library |
| `pkg/mod/` | GALA module cache: each module's full tree minus VCS metadata. `GALA_CACHE` moves it; `gala build` and the `gala mod` commands both use that location |
| `go/pkg/mod/` | Go module cache |

---

## 6. Go Dependencies

GALA can depend on Go packages. Mark Go dependencies with `// go` or use the `--go` flag:

```bash
gala mod add github.com/google/uuid@v1.6.0 --go
```

In `gala.mod`:
```
require (
    github.com/google/uuid v1.6.0 // go
)
```

**How it works:**

1. Go dependencies are tracked in `gala.mod` but not transpiled
2. The transpiler generates correct Go imports for Go packages
3. For non-Bazel builds: `gala build` and `gala test` read each Go dependency's source for its types before transpiling, downloading it first if it is not cached, and `go mod tidy` fetches it for the Go compile. A GALA library's own Go dependencies are read the same way when it is built as a dependency, so they need not be repeated in your `gala.mod`
4. For Bazel builds: Gazelle's `go_deps` extension fetches them
5. For non-Bazel builds, a `replace` in your `gala.mod` applies to a Go dependency as it would in `go.mod`: `replace example.com/greet => ../greet` builds the module from that directory, and `replace github.com/google/uuid => github.com/google/uuid v1.6.0` builds it at that version. As in Go, only the replaces of the module being built count, not those of its dependencies

**Usage in GALA code:**

```gala
package main

import "github.com/google/uuid"

func GenerateID() string = uuid.New().String()
```

---

## 7. GALA Library Dependencies

GALA projects can depend on other GALA libraries — packages written in GALA and published as `.gala` source files.

### How It Works

When you add a GALA dependency (without the `--go` flag), the module is fetched to the GALA module cache (`~/.gala/pkg/mod/` by default; `GALA_HOME` or `GALA_CACHE` move it). At build time, `gala build` automatically transpiles these dependencies to Go before compiling:

1. The builder scans `gala.mod` for GALA dependencies (entries without `// go`)
2. For each dep that contains `.gala` files, the transpiler pipeline runs (parse → analyze → transform → generate)
3. Transpiled `.gen.go` files and a `go.mod` are written to `~/.gala/build/<hash>/deps/<module>@<version>/`
4. The project's `go.mod` uses `replace` directives pointing to the transpiled output
5. Transitive GALA dependencies are collected recursively and transpiled as well

### Multi-File Packages

GALA library packages with multiple `.gala` files are fully supported. The transpiler uses `NewGalaAnalyzerWithPackageFiles` to enable cross-file type resolution within the dependency.

### What You Can Import From a Dependency

Not every package in a dependency is part of its API. A package under a directory named `internal` is private to the module that ships it:

```
github.com/user/lib-a/
  liba.gala                  package liba      — importable by you
  internal/
    detail/detail.gala       package detail    — private to github.com/user/lib-a
```

```gala
import "github.com/user/lib-a"                    // OK
import "github.com/user/lib-a/internal/detail"    // GALA-E0041
```

The dependency uses its own `internal` packages freely — you just cannot reach past its public surface to them. This is the same rule Go applies, stated in full in [Package Visibility](GALA.MD#package-visibility): `a/b/c/internal/d` is importable only from the tree rooted at `a/b/c`.

It applies to your own project too. A package under `internal/` at your module root is visible anywhere in your module but to no one who depends on it, which is what makes it a safe place for code you are not ready to commit to supporting. See [GALA-E0041](errors/GALA-E0041.md) for the diagnostic and how to resolve it.

### Transitive Dependencies

Dependencies of dependencies are handled automatically:

```
your-project
├── github.com/user/lib-a@v1.0.0       (GALA — transpiled)
│   └── github.com/user/lib-b@v2.0.0   (GALA — transpiled)
│       └── github.com/google/uuid      (Go — downloaded by go mod tidy)
└── github.com/other/lib@v1.0.0        (Go — downloaded by go mod tidy)
```

- GALA deps at any depth are transpiled and their `go.mod` files are generated with correct `replace` directives
- Go deps at any depth are collected and added to the project's `go.mod`
- Pure Go packages in `gala.mod` (no `.gala` files in cache) are skipped — they don't need transpilation

### Example

```bash
# Add a GALA library dependency
gala mod add github.com/martianoff/gala_demo_pkg@v0.2.3

# Build — the dep is transpiled automatically
gala build -v
```

Verbose output:
```
Found 1 GALA dependencies to transpile
  Transpiling dependency: github.com/martianoff/gala_demo_pkg@v0.2.3 (2 files)
    math.gala -> math.gen.go
    utils.gala -> utils.gen.go
Transpiling GALA files...
  main.gala -> main.gen.go
```

---

## 8. Bazel Integration

GALA provides Bazel rules for dependency management. Dependencies are declared in `gala.mod` and automatically loaded into Bazel.

### Required `.bazelrc` settings

Every Bazel project that consumes the GALA toolchain must enable the
following at the root of the project (`.bazelrc`):

```
common --enable_bzlmod
common --noenable_workspace
build  --enable_runfiles

# Resolve the rules_gala module from the registry hosted in
# github.com/martianoff/rules-gala. Order matters — list this first
# so it wins for the rules_gala module name.
common --registry=https://raw.githubusercontent.com/martianoff/rules-gala/main/
common --registry=https://bcr.bazel.build

# Cross-language type inference: pass GOROOT and PATH through to genrules
# so the transpiler can use go/importer to resolve Go package types
# (function signatures, struct fields, method sets).
build  --action_env=GOROOT
build  --action_env=PATH
```

Why each one matters:

| Flag | Why |
|---|---|
| `--enable_bzlmod` / `--noenable_workspace` | Bzlmod is the dependency model GALA's Bazel rules are wired against; legacy `WORKSPACE` mode won't resolve `@gala//...` correctly. |
| **`--enable_runfiles`** | **Required on Windows** for `gala_test` / `gala_unit_test` targets to work. Without it the test wrappers (`.bat` files) can't locate their binaries via runfiles and tests fail with "the system cannot find the path specified" before any test code runs. (On Linux/macOS Bazel materializes runfiles via symlinks by default; on Windows the manifest-only mode needs this flag.) |
| `--action_env=GOROOT` / `--action_env=PATH` | Lets the transpiler invoke `go/importer` for cross-language type inference. Without these, transpilation still succeeds but Go-side function/struct types are resolved as `any` and downstream code may fail to compile. |

### MODULE.bazel (Bzlmod)

```python
module(
    name = "myproject",
    version = "0.0.1",
)

# GALA rules + language toolchain
bazel_dep(name = "rules_gala", version = "0.6.11")
bazel_dep(name = "gala", version = "0.84.1")
register_toolchains("@gala//tools/toolchain:gala_toolchain")

# Go rules (required for Go dependencies)
bazel_dep(name = "rules_go", version = "0.50.1")
bazel_dep(name = "gazelle", version = "0.39.1")

# GALA Gazelle extension (BUILD-file generation for .gala sources)
bazel_dep(name = "gala_gazelle", version = "0.2.5", dev_dependency = True)

# Go dependencies (auto-generated by 'gala mod tidy')
go_deps = use_extension("@gazelle//:extensions.bzl", "go_deps")
go_deps.from_file(go_mod = "//:go.mod")
use_repo(go_deps, "com_github_google_uuid")

# GALA dependencies (only needed if gala.mod has external GALA deps)
gala = use_extension("@rules_gala//gala:extensions.bzl", "gala")
gala.from_file(gala_mod = "//:gala.mod")
use_repo(gala, "com_github_example_utils")
```

The `gala.from_file()` extension is only needed when your project has external GALA module dependencies listed in `gala.mod`. If your project only uses the GALA stdlib (`std`, `collection_immutable`, etc.) and Go packages, you can omit the `gala` extension block entirely — stdlib packages are provided by the `@gala` module itself.

**Important:** Run `gala mod tidy` to keep `go.mod` in sync. For Bazel projects, `go.mod` only contains Go dependencies — GALA stdlib deps are resolved by Bazel automatically.

### Generating BUILD Files with Gazelle

The recommended way to create and maintain BUILD files — for Go, GALA, and mixed
GALA+GO packages — is the GALA Gazelle extension. It ships from rules-gala as the
`gala_gazelle` Bazel module and uses the `gala imports` CLI subcommand as a
parse-only import helper (no transpilation, no Go SDK needed). A single
`bazel run //:gazelle` pass discovers `.gala` sources, groups them by package,
and emits `gala_library` / `gala_binary` / `gala_test` targets with `deps`
resolved from each file's imports.

To enable it, add the `bazel_dep` to your `MODULE.bazel` alongside the other
Gazelle wiring (use the current version published by rules-gala):

```python
bazel_dep(name = "gala_gazelle", version = "0.2.5", dev_dependency = True)
```

Then wire the `gazelle` target in your **root `BUILD.bazel`** using the
`gala_gazelle` macro — *not* the plain Go `gazelle` rule. The macro builds a
composite Gazelle binary that embeds both the Go language and the GALA language,
plus a toolchain-driven import helper:

```starlark
load("@gala_gazelle//gala:defs.bzl", "gala_gazelle")

# gazelle:gala_prefix example.com/myproject
gala_gazelle(name = "gazelle")
```

`gala_prefix` sets your module's import prefix so generated `importpath` and
`deps` resolve correctly. Run `bazel run //:gazelle` to generate and maintain
`BUILD` files; re-run it after adding or removing imports to keep `deps` up to
date, so you rarely need to hand-edit them.

> **Do not use the plain `gazelle(name = "gazelle")` rule from
> `@gazelle//:def.bzl`.** That binary only knows the Go language, so it ignores
> `.gala` sources entirely — it fails with `unknown directive: gazelle:gala_prefix`
> and generates **zero** GALA targets. The `gala_gazelle` macro above is what
> teaches Gazelle about GALA.

The full directive reference (`gala_prefix`, `gala_helper`, `gala_stdlib_prefix`,
`gala_stdlib_repo`, `gala_implicit_dep`) and the manual (non-macro) wiring — for
projects that hit the `dev_dependency` edge cases — live in rules-gala's
`gazelle/README.md`.

### BUILD Files

`gala_library` / `gala_binary` accept two related dep attributes:

- **`gala_deps`** — GALA library labels whose **source files** must be
  visible to the transpiler during this target's transpile step.
  Required whenever your code calls into the dep's **sealed-type case
  constructors** (`NoCmd[Msg]()`), **named-arg struct constructors**
  (`Program[Model, Msg](Initial = ..., …)`), or other forms where the
  analyzer needs the dep's GALA-side metadata to lower correctly.
- **`deps`** — plain Bazel labels (Go libraries / GALA libraries used
  only via fully-typed function calls). Sufficient when the call site
  doesn't trigger any GALA-side lowering.

When in doubt, **prefer `gala_deps` for any GALA library you import**.
The cost is only at transpile time (Bazel re-reads the source files);
the runtime artifact is identical.

Gazelle (see [above](#generating-build-files-with-gazelle)) generates and
maintains the targets below; the example is for reference and for the cases
where you tune the `gala_deps` / `deps` split by hand:

```python
load("@rules_gala//gala:defs.bzl", "gala_library", "gala_binary")

gala_library(
    name = "mylib",
    src = "mylib.gala",
    importpath = "github.com/user/project/mylib",
    gala_deps = ["@com_github_example_utils//:utils"],  # GALA library — analyzer needs source
    deps = ["@com_github_other_go_lib//:lib"],          # plain Go library
)

gala_binary(
    name = "myapp",
    src = "main.gala",
    gala_deps = ["@com_github_example_utils//:utils"],
)
```

**Symptom of the wrong split** (GALA dep listed under `deps` instead of
`gala_deps`): Go-side compile errors at the call site, e.g.
`missing argument in conversion to pkg.NoCmd[Msg]` or
`cannot use Model{…} as std.Immutable[Model] value in struct literal`.
The transpiled `.gen.go` emits a raw type conversion / raw struct
literal because the analyzer couldn't see that `NoCmd` is a sealed-case
constructor or that `Program`'s fields are auto-wrapped in
`std.Immutable`.

### Repository Naming Convention

Module paths are converted to Bazel repository names:

| Module Path | Repository Name |
|-------------|-----------------|
| `github.com/example/utils` | `@com_github_example_utils` |
| `gitlab.com/user/lib` | `@com_gitlab_user_lib` |

---

## 9. Version Constraints

GALA supports several version constraint syntaxes:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `v1.2.3` | Exact version | Exactly v1.2.3 |
| `^1.2.3` | Compatible | >=1.2.3, <2.0.0 |
| `~1.2.3` | Approximate | >=1.2.3, <1.3.0 |
| `>=1.2.3` | Minimum | At least v1.2.3 |
| `latest` | Latest stable | Most recent tagged version; for a module with no tag, the pseudo-version of its latest commit |

For the version `gala mod add` writes to `gala.mod`, see
[gala mod add](#gala-mod-add).

**Minimal Version Selection (MVS):**

Like Go modules, GALA uses MVS for dependency resolution:
- When multiple versions are required, the highest minimum is selected
- This ensures reproducible builds

---

## Best Practices

1. **Commit both files**: Always commit `gala.mod` and `gala.sum` to version control
2. **Run verify in CI**: Add `gala mod verify` to your CI pipeline
3. **Use tidy regularly**: Run `gala mod tidy` after changing imports
4. **Pin versions**: Use exact versions in production, constraints in libraries
5. **Separate Go deps**: Mark Go dependencies with `// go` for clarity
6. **For Bazel**: Also commit `go.mod` and `go.sum` (they're auto-generated but needed by Bazel)

---

## Example Projects

For complete working examples of GALA dependency management, see:

- **[gala_demo_pkg_import](https://github.com/martianoff/gala_demo_pkg_import)** - Non-Bazel project using `gala build`
- **[gala_demo_pkg_import_bazel](https://github.com/martianoff/gala_demo_pkg_import_bazel)** - Bazel project with bzlmod integration
