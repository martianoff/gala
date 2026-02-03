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
   - [gala clean](#gala-clean)
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
7. [Bazel Integration](#7-bazel-integration)
   - [MODULE.bazel (Bzlmod)](#modulebazel-bzlmod)
   - [BUILD Files](#build-files)
8. [Version Constraints](#8-version-constraints)

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

For Bazel projects, `gala mod tidy` generates the necessary `go.mod` and `go.sum` files:

```bash
# Initialize
gala mod init github.com/user/myproject

# Add dependencies
gala mod add github.com/example/gala-utils@v1.2.3
gala mod add github.com/google/uuid@v1.6.0 --go

# Sync dependencies (generates go.mod and go.sum for Bazel)
gala mod tidy

# Build with Bazel
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
- Hash prefix (`h1:`) and SHA-256 hash

---

## 3. CLI Commands

### gala build

Build a GALA project to a binary.

```bash
# Build with default output name (project directory name)
gala build

# Build with custom output name
gala build -o myapp

# Build with verbose output
gala build -v
```

**What happens:**
1. Transpiles `.gala` files to Go in a workspace at `~/.gala/build/<hash>/`
2. Downloads Go dependencies to `~/.gala/go/pkg/mod/`
3. Runs `go build` to produce the binary in your project directory

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

### gala clean

Clean build artifacts.

```bash
# Clean the workspace for this project
gala clean

# Clean all GALA caches
gala clean --all

# Clean stale workspaces (projects that no longer exist)
gala clean --stale
```

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
```

The command will:
1. Fetch the module from Git
2. Cache it locally
3. Update `gala.mod` with the dependency
4. Update `gala.sum` with checksums

### gala mod remove

Remove a dependency from your module.

```bash
gala mod remove github.com/example/utils
```

### gala mod update

Update dependencies to their latest versions.

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
- **For Bazel projects**: Generates `go.mod` and `go.sum` with Go dependencies

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
3. **Transpiles** your `.gala` files to `.gen.go` files in the workspace
4. **Generates** a `go.mod` in the workspace with absolute paths to dependencies
5. **Downloads** Go dependencies to `~/.gala/go/pkg/mod/`
6. **Runs** `go build` to create the binary

**Key benefits:**
- Your project folder stays clean (no go.mod, no _gala folder, no .gen.go files)
- Dependencies are cached globally
- No symlinks used (works on all platforms including Windows)

### With Bazel

For Bazel projects:

1. Run `gala mod tidy` to generate `go.mod` and `go.sum` (Go dependencies only)
2. GALA dependencies are handled by the bzlmod extension
3. Build with `bazel build //:target`

---

## 5. Cache Structure

All GALA caches are stored in `~/.gala/`:

```
~/.gala/
  build/                              # Build workspaces (per-project)
    <hash>/                           # Workspace for a specific project
      gen/                            # Generated .gen.go files
      go.mod                          # Generated go.mod with absolute paths
      go.sum                          # Generated go.sum

  stdlib/                             # Stdlib versions
    vdev/                             # Development version
      std/                            # Standard library packages
      go_interop/
      collection_immutable/
      collection_mutable/
      concurrent/

  pkg/mod/                            # GALA package cache
    github.com/example/utils@v1.2.3/  # Cached GALA modules

  go/pkg/mod/                         # Go package cache (GOMODCACHE)
    github.com/google/uuid@v1.6.0/    # Cached Go modules
```

| Directory | Purpose |
|-----------|---------|
| `build/<hash>/` | Per-project build workspace |
| `stdlib/v<version>/` | GALA standard library |
| `pkg/mod/` | GALA module cache |
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
3. For non-Bazel builds: `gala build` downloads them via `go mod tidy`
4. For Bazel builds: Gazelle's `go_deps` extension fetches them

**Usage in GALA code:**

```gala
package main

import "github.com/google/uuid"

func GenerateID() string = uuid.New().String()
```

---

## 7. Bazel Integration

GALA provides Bazel rules for dependency management. Dependencies are declared in `gala.mod` and automatically loaded into Bazel.

### MODULE.bazel (Bzlmod)

```python
module(
    name = "myproject",
    version = "0.0.1",
)

# GALA toolchain
bazel_dep(name = "gala", version = "1.0.0")

# Go rules (required for Go dependencies)
bazel_dep(name = "rules_go", version = "0.50.1")
bazel_dep(name = "gazelle", version = "0.39.1")

# Go dependencies (auto-generated by 'gala mod tidy')
go_deps = use_extension("@gazelle//:extensions.bzl", "go_deps")
go_deps.from_file(go_mod = "//:go.mod")
use_repo(go_deps, "com_github_google_uuid")

# GALA dependencies
gala = use_extension("@gala//:extensions.bzl", "gala")
gala.from_file(gala_mod = "//:gala.mod")
use_repo(gala, "com_github_example_utils")
```

**Important:** Run `gala mod tidy` to generate `go.mod` and `go.sum` files that Bazel needs for Go dependencies.

### BUILD Files

Reference dependencies in the standard `deps` attribute:

```python
load("//:gala.bzl", "gala_library", "gala_binary")

gala_library(
    name = "mylib",
    src = "mylib.gala",
    importpath = "github.com/user/project/mylib",
    deps = ["@com_github_example_utils//:utils"],
)

gala_binary(
    name = "myapp",
    src = "main.gala",
    deps = ["@com_github_example_utils//:utils"],
)
```

### Repository Naming Convention

Module paths are converted to Bazel repository names:

| Module Path | Repository Name |
|-------------|-----------------|
| `github.com/example/utils` | `@com_github_example_utils` |
| `gitlab.com/user/lib` | `@com_gitlab_user_lib` |

---

## 8. Version Constraints

GALA supports several version constraint syntaxes:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `v1.2.3` | Exact version | Exactly v1.2.3 |
| `^1.2.3` | Compatible | >=1.2.3, <2.0.0 |
| `~1.2.3` | Approximate | >=1.2.3, <1.3.0 |
| `>=1.2.3` | Minimum | At least v1.2.3 |
| `latest` | Latest stable | Most recent tagged version |

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
