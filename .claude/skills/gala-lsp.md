# GALA LSP & IntelliJ Plugin

Fix bugs and add features to the GALA Language Server (LSP) and IntelliJ plugin.

**Argument:** Description of the issue or feature. Can include:
- Screenshot descriptions or error messages
- File paths where the issue occurs (e.g., `C:\Users\maxmr\GolandProjects\gala-server\server.gala`)
- Expected vs actual behavior

---

## Architecture

### LSP Server (`internal/lsp/`)
| File | Purpose |
|------|---------|
| `server.go` | Core handler: DidOpen/DidChange/DidSave, cancel-and-restart analysis, publishDiagnostics |
| `completion.go` | Completion: dot, named args, match cases, package, type-specific methods |
| `typeatpos.go` | Type resolution: typeAtDot, resolveChainType, resolveMethodReturn, findType |
| `definition.go` | Go-to-definition: types, methods, functions, sealed variants, fields |
| `hover.go` | Hover info: type metadata, builtin docs |
| `inlay_hints.go` | Type hints: val/var declarations, lambda params, pattern match bindings |
| `gotype.go` | Helpers: cleanGoTypeForDisplay, stripTypeParams |

### Key Data Flow
```
DidChange → analyzeFile → parser.Parse → analyzer.Analyze → transformer.TransformForLSP
                                                              ↓
                                              richAST (types, functions, packages)
                                              varTypes (scoped: "funcName.varName" → type)
                                                              ↓
                                              Completion / Hover / Definition / InlayHints
```

### IntelliJ Plugin (`ide/intellij/`)
- ANTLR-based parser for PSI tree (syntax highlighting, folding, structure)
- LSP client via `GalaLspServerProvider` — runs `gala lsp` binary
- Plugin errors (PsiErrorElement) are INDEPENDENT of LSP diagnostics

---

## Instructions

### Phase 1: Reproduce

**CRITICAL: Always reproduce the issue in a test BEFORE fixing.**

1. Create a test function in `internal/lsp/server_test.go` or `internal/lsp/error_lines_test.go`
2. Use the exact GALA code pattern from the bug report
3. For completion: use `h.Completion(uri, line, char)` and check items
4. For definition: use `h.Definition(uri, line, char)` and check location
5. For hints: use `h.Call("textDocument/inlayHint", ...)` and check JSON
6. For diagnostics: use `h.Diagnostics(uri)` after `time.Sleep`
7. The test MUST FAIL before the fix and PASS after

**Test patterns for common scenarios:**

```go
// Single file test
h := newHarness(t)
uri := openFileOnDisk(t, h, "package main\n\n...")

// Multi-file test (cross-package)
dir := createTestProject(t, []testProjectFile{
    {Name: "types.gala", Src: "package mylib\n\n..."},
    {Name: "handler.gala", Src: "package mylib\n\n..."},
})
openProjectFile(t, h, dir, "types.gala")
uri := openProjectFile(t, h, dir, "handler.gala")

// Simulating user typing (valid code → broken → fixed)
uri := openFileOnDisk(t, h, validCode)    // builds richAST cache
time.Sleep(200 * time.Millisecond)
h.DidChange(uri, 1, brokenCode)           // parse error, cached richAST survives
time.Sleep(200 * time.Millisecond)
list, _ := h.Completion(uri, line, char)  // uses cached richAST
```

**NEVER use `h.ClearDiagnostics()`** — simulate real IDE behavior with `time.Sleep` + `h.Diagnostics(uri)`.

### Phase 2: Fix

**Rules:**
1. Use the SAME code as the transpiler wherever possible (same analyzer, same type resolution)
2. NEVER do text-based parsing when the transpiler already handles it (e.g., exhaustiveness checking)
3. All slices returned to the client MUST use `make([]T, 0)`, NOT `var x []T` (Go nil → JSON null crashes IntelliJ)
4. URI paths must be decoded with `url.PathUnescape` (IntelliJ sends `%3A` for `:` on Windows)
5. Variable types are scoped: key format is `"funcName.varName"` in `lspVarTypes`
6. Use `lookupVarType(varTypes, funcScope, name)` for scoped lookup
7. Use `findEnclosingFunc(lines, targetLine)` to determine current function scope
8. Each `DidChange` starts analysis in a goroutine with cancel-and-restart — NO debounce
9. `DefinedIn` field must be set on TypeMetadata, MethodMetadata, and FunctionMetadata for go-to-definition
10. `findType(richAST, name)` handles package-prefixed names via simple name extraction

**Common fix patterns:**

| Issue | Root Cause | Fix Location |
|-------|-----------|-------------|
| Stale diagnostics | nil slice → JSON null | `make([]T, 0)` in return values |
| Wrong type hint | Flat varTypes map | Scoped `"funcName.varName"` keys |
| No cross-file definition | Missing `DefinedIn` | Set in analyzer's `extractSiblingFullMetadata` |
| Completion shows all types | No filtering | Filter by context (match subject type, receiver type) |
| Chain completion fails | Only resolves simple identifiers | Use `resolveChainTypeN` for paren-skip chains |
| Lambda param hint on val line | Regex matches both | Skip lambda hints when `valDeclRegex` matches the line |
| Pattern hint shows `:T` | Unresolved type parameter | Skip single-letter uppercase type names |
| Go-to-definition for std types | `DefinedIn` empty (prelude) | Fallback: search package directory via `findDefinitionInDir` |

### Phase 3: Verify

1. Run the specific test: `bazel-bin/internal/lsp/lsp_test_/lsp_test.exe -test.run "TestName" -test.v`
2. Run ALL LSP tests: `bazel test //internal/lsp:lsp_test --test_output=errors --cache_test_results=no`
3. Run analyzer tests: `bazel test //internal/transpiler/analyzer:analyzer_test --test_output=errors`
4. Run transformer tests: `bazel test //internal/transpiler/transformer:transformer_test --test_output=errors`
5. Build examples: `bazel build examples:bug013_type_alias_lib_test` (catches redefinition regressions)
6. Full build: `bazel build //internal/... //cmd/...`

### Phase 4: Create PR

1. Create branch from master: `git checkout -b fix/lsp-description`
2. Commit with tests: every fix MUST have a corresponding test
3. Push branch: `git push -u origin fix/lsp-description`
4. Reset master to last release tag: `git checkout master && git reset --hard <tag> && git push --force-with-lease`
5. Create PR: `gh pr create --base master --head fix/lsp-description --title "..." --body "..."`
6. Wait for CI: `gh pr checks <number> --watch`
7. If CI fails: checkout branch, fix, push, wait again
8. Merge when green: `gh pr merge <number> --admin --merge --delete-branch`

---

## Known Limitations

| Area | Limitation | Reason |
|------|-----------|--------|
| Go pointer types (`*httpcore.ServerBuilder`) | No completion | Go struct methods not in GALA type metadata |
| `var parsed map[string]string` | No type tracking | Go raw types not tracked through GALA type system |
| Chain method definition in tests | May not resolve | std type methods need `EnsureStdlib` (works in real IDE) |
| Prelude types `DefinedIn` | Empty | Types from registry, not file analysis — uses directory search fallback |

## Quality Checklist

Before creating PR:
- [ ] Every fix has a FAILING test that PASSES after the fix
- [ ] No `var x []T` that could produce JSON null (use `make([]T, 0)`)
- [ ] No `NewSemanticError()` without line numbers (use `NewSemanticErrorAt`)
- [ ] No text-based parsing when the transpiler handles it
- [ ] No `ClearDiagnostics()` in tests — use realistic `time.Sleep` + `h.Diagnostics()`
- [ ] Cross-file tests use `createTestProject` + `openProjectFile`
- [ ] All 15 internal test suites pass
- [ ] `bazel build //internal/... //cmd/...` succeeds
