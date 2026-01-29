# GALA Dependency Management

GALA provides a dependency management system similar to Go modules, using `gala.mod` and `gala.sum` files to track dependencies and their checksums.

---

## Table of Contents

1. [Quick Start](#1-quick-start)
2. [File Formats](#2-file-formats)
   - [gala.mod](#galamod)
   - [gala.sum](#galasum)
3. [CLI Commands](#3-cli-commands)
   - [gala mod init](#gala-mod-init)
   - [gala mod add](#gala-mod-add)
   - [gala mod remove](#gala-mod-remove)
   - [gala mod update](#gala-mod-update)
   - [gala mod tidy](#gala-mod-tidy)
   - [gala mod graph](#gala-mod-graph)
   - [gala mod verify](#gala-mod-verify)
4. [Cache Structure](#4-cache-structure)
5. [Go Dependencies](#5-go-dependencies)
6. [Bazel Integration](#6-bazel-integration)
   - [WORKSPACE (Legacy)](#workspace-legacy)
   - [MODULE.bazel (Bzlmod)](#modulebazel-bzlmod)
   - [BUILD Files](#build-files)
7. [Version Constraints](#7-version-constraints)
8. [Migration Guide](#8-migration-guide)

---

## 1. Quick Start

Initialize a new GALA module:

```bash
gala mod init github.com/user/myproject
```

Add dependencies:

```bash
# Add a GALA dependency
gala mod add github.com/example/gala-utils@v1.2.3

# Add a Go dependency
gala mod add github.com/google/uuid@v1.6.0 --go
```

Your project structure:

```
myproject/
  gala.mod          # Module manifest
  gala.sum          # Dependency checksums
  main.gala         # Your GALA code
  BUILD.bazel       # Bazel build file
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
```

Each line contains:
- Module path
- Version
- Optional file suffix (e.g., `/gala.mod`)
- Hash prefix (`h1:`) and SHA-256 hash

---

## 3. CLI Commands

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

## 4. Cache Structure

Dependencies are cached in `~/.gala/pkg/mod/`:

```
~/.gala/
  pkg/
    mod/
      cache/
        download/
          github.com/example/utils/@v/
            v1.2.3.zip
            v1.2.3.info
            v1.2.3.mod
      github.com/example/utils@v1.2.3/
        lib.gala
        gala.mod
        BUILD.bazel
```

| Directory | Purpose |
|-----------|---------|
| `cache/download/` | Downloaded archives and metadata |
| `<module>@<version>/` | Extracted module source files |

Environment variable:
- `GALA_MODCACHE`: Override the cache directory (default: `~/.gala/pkg/mod`)

---

## 5. Go Dependencies

GALA can depend on Go packages (not just GALA modules). Mark Go dependencies with `// go` or use the `--go` flag:

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
3. Bazel handles fetching via `go_repository` rules

**Usage in GALA code:**

```gala
package main

import "github.com/google/uuid"

func GenerateID() string = uuid.New().String()
```

---

## 6. Bazel Integration

GALA provides Bazel rules for dependency management. Dependencies are declared in `gala.mod` and automatically loaded into Bazel.

### WORKSPACE (Legacy)

Load all dependencies from `gala.mod`:

```python
load("@gala//:gala_deps.bzl", "gala_dependencies")

# Load all GALA dependencies from gala.mod
gala_dependencies()
```

This reads your `gala.mod` file and creates repository rules for each GALA dependency.

### MODULE.bazel (Bzlmod)

For Bzlmod-based builds, use the `gala` extension:

```python
bazel_dep(name = "gala", version = "1.0.0")

gala = use_extension("@gala//:extensions.bzl", "gala")

# Load from gala.mod file
gala.from_file(gala_mod = "//:gala.mod")

use_repo(gala, "com_github_example_utils")
```

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

The target within each repository uses the package name from the GALA source (e.g., `//:utils`).

---

## 7. Version Constraints

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

## 8. Migration Guide

### From Manual Imports

If you have GALA code with manual import paths:

1. Initialize the module:
   ```bash
   gala mod init github.com/user/project
   ```

2. Run tidy to detect imports:
   ```bash
   gala mod tidy
   ```

3. Update BUILD files to use `gala_deps`:
   ```python
   gala_library(
       name = "mylib",
       src = "mylib.gala",
       importpath = "github.com/user/project/mylib",
       gala_deps = ["@com_github_example_utils//:utils"],
   )
   ```

### From Go modules

If migrating from a Go project:

1. GALA auto-detects module path from `go.mod`:
   ```bash
   gala mod init
   ```

2. Add existing Go dependencies:
   ```bash
   gala mod add github.com/google/uuid@v1.6.0 --go
   ```

3. GALA and Go modules can coexist in the same project

---

## Best Practices

1. **Commit both files**: Always commit `gala.mod` and `gala.sum` to version control
2. **Run verify in CI**: Add `gala mod verify` to your CI pipeline
3. **Use tidy regularly**: Run `gala mod tidy` after changing imports
4. **Pin versions**: Use exact versions in production, constraints in libraries
5. **Separate Go deps**: Mark Go dependencies with `// go` for clarity
