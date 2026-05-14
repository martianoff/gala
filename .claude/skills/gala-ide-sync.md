---
description: Sync the IntelliJ plugin and LSP server's hardcoded language data with the GALA grammar, std library, and transpiler. TRIGGER when grammar, std, or collection types change, or when user asks to sync/update the IDE plugin or LSP server data.
user-invocable: true
---

# GALA IDE Sync

Verify and fix synchronization between the IntelliJ plugin + LSP server's hardcoded language data and the authoritative sources (grammar, std library, transpiler).

**Argument:** `$ARGUMENTS` - Optional: `check` (report only, no changes) or `fix` (default: fix all discrepancies)

**Reference:** See `ide/intellij/SYNC.md` for the full sync architecture documentation.

---

## Instructions

### Step 1: Extract authoritative data

Read these source-of-truth files and extract:

**Keywords** from `internal/parser/grammar/gala.g4`:
- Named lexer tokens: `VAL`, `VAR`, `FUNC`, `TYPE`, `STRUCT`, `INTERFACE`, `MATCH`, `CASE`, `IF`, `ELSE`, `FOR`, `RANGE`, `RETURN`, `IMPORT`, `PACKAGE`, `SEALED`, `EMBED`
- Inline keyword literals in parser rules: `'true'`, `'false'`, `'nil'`, `'map'`, `'return'`, `'struct'`, etc.
- Extract with: `grep -oP "'[a-z]+'" internal/parser/grammar/gala.g4 | sort -u`

**Built-in types** from `internal/transpiler/types.go`:
- Find the `IsPrimitiveType()` function and extract the type name list

**Standard library auto-imported types** from `std/*.gala`:
- ONLY types available without explicit `import` — core sealed types (Option, Either, Try), their constructors (Some, None, Left, Right, Success, Failure), and fundamental types (Tuple, Immutable)
- Types from other packages require explicit import and are NOT in static completion

**Standard library methods** from `std/*.gala`:
- Public methods (capitalized) on std types: `func (receiver) MethodName(...)`
- Extract with: `grep -h "^func (" std/*.gala | grep -oP '\) [A-Z]\w+' | sed 's/) //' | sort -u`

### Step 2: Compare against plugin AND LSP server

Read these files and compare:

**IntelliJ Plugin:**

| File | Data | Source |
|------|------|--------|
| `ide/intellij/.../GalaCompletionContributor.kt` | DECLARATION_KEYWORDS | gala.g4 named tokens |
| `ide/intellij/.../GalaCompletionContributor.kt` | CONTROL_KEYWORDS | gala.g4 inline keywords |
| `ide/intellij/.../GalaCompletionContributor.kt` | LITERAL_KEYWORDS | gala.g4 literal rule |
| `ide/intellij/.../GalaCompletionContributor.kt` | TYPE_KEYWORDS | gala.g4 type rule |
| `ide/intellij/.../GalaCompletionContributor.kt` | BUILTIN_TYPES | types.go IsPrimitiveType |
| `ide/intellij/.../GalaCompletionContributor.kt` | STD_AUTO_IMPORTED | std/*.gala auto-imported types only |
| `ide/intellij/.../GalaCompletionContributor.kt` | DOT_METHODS | std/*.gala public methods |
| `ide/intellij/.../GalaAnnotator.kt` | BUILTIN_TYPE_NAMES | types.go IsPrimitiveType |
| `ide/intellij/.../GalaSyntaxHighlighter.kt` | keyword token list | gala.g4 named lexer tokens |

**LSP Server:**

| File | Data | Source |
|------|------|--------|
| `internal/lsp/completion.go` | `keywordCompletions()` | gala.g4 keywords |
| `internal/lsp/inlay_hints.go` | `inferType()` literal patterns | transpiler type system |
| `internal/lsp/diagnostics.go` | `checkMatchExhaustiveness()` | RichAST (runtime, no hardcoded data) |

### Step 3: Report discrepancies

Print a table for both plugin and LSP:

```
| Component | Data | Has | Source Has | Status |
|-----------|------|-----|-----------|--------|
| Plugin | Keywords | 28 | 25 | 3 EXTRA |
| LSP | Keywords | 21 | 25 | 4 MISSING |
```

### Step 4: Fix (unless `$ARGUMENTS` is `check`)

For each discrepancy:
- Edit the plugin file to match the source of truth
- Edit the LSP server file to match the source of truth
- Keep both in sync with each other
- Keep the same code style (list format, comments)

### Step 5: Verify

After fixes:
```bash
bazel build //cmd/gala-lsp:gala-lsp //ide/intellij:plugin
```

### Step 6: Report

Print summary of changes made.

## Rules

1. **Only add keywords that exist in gala.g4** — if a keyword isn't in the grammar, it's not GALA syntax
2. **Only auto-imported std types in static completion** — `Option`, `Some`, `None`, `Either`, `Left`, `Right`, `Try`, `Success`, `Failure`, `Tuple`, `Immutable` are always available without import
3. **NEVER add importable package types to static completion** — types from `collection_immutable`, `collection_mutable`, `io`, `stream`, `concurrent`, `regex`, `strings` etc. require explicit `import` and are handled by the LSP server dynamically
4. **Don't remove method templates** that are conceptually correct even if the exact method signature isn't in std (e.g., `match` is a language keyword, not a method)
5. **Built-in types must be identical** across plugin (3 files) and LSP server
6. **Keywords must be identical** between plugin (`GalaCompletionContributor.kt`) and LSP server (`completion.go`)
7. **Always commit with `core.autocrlf=false`** for BUILD.bazel and .sh files to prevent CRLF in CI
