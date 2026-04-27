# GALA-E0020 — Package not found

**When it fires.** The analyzer was asked to resolve a GALA package by
its import path or relative path, but no matching directory exists on
any of the configured search paths. Triggered both by explicit
`import` statements and by directory-walk passes that follow them.

**Error output.**

```
[SemanticError GALA-E0020] line 0:0 package not found: <path> (hint: check that the directory exists on a search path; for cross-module imports verify gala.mod has a `require` (and `replace` if local) for the module)
```

**Fix.** Three common causes:

1. **Typo in the import path** — `import . "github.com/example/foo"` when the directory is `github.com/example/Foo` (case mismatch). GALA preserves case from the filesystem.
2. **Missing search path** — when running outside Bazel, the standalone CLI needs `--search` to point at the workspace's module root. Bazel rules pass this automatically.
3. **Cross-module dependency without `replace`** — `gala.mod` has `require github.com/example/foo v0.1.0` but no matching `replace ...` directive points at the local checkout. Add a replace, or run inside a workspace where `go mod download` has fetched the module.

**Rationale.** Before this code existed, the failure surfaced as a
generic `package not found: <path>` from the analyzer, with no stable
identifier for tools or CI to grep. Promoting it to GALA-E0020 lets
build tooling distinguish "user import is wrong" from other infrastructure
failures (file-system errors, parser crashes, etc.).

**Scope.** Only the analyzer's package-resolution failures, not Go-level
import errors that surface from `go build` after transpilation. Those
still appear as Go compiler messages (the GALA layer has already
finished its work).
