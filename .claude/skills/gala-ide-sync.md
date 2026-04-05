---
description: Sync the IntelliJ plugin's hardcoded language data with the GALA grammar, std library, and transpiler. TRIGGER when grammar, std, or collection types change, or when user asks to sync/update the IDE plugin data.
user-invocable: true
---

# GALA IntelliJ Plugin Sync

Verify and fix synchronization between the IntelliJ plugin's hardcoded language data and the authoritative sources (grammar, std library, transpiler).

**Argument:** `$ARGUMENTS` - Optional: `check` (report only, no changes) or `fix` (default: fix all discrepancies)

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
- These are Go primitive types available in GALA

**Standard library auto-imported types** from `std/*.gala`:
- ONLY types available without explicit `import` — core sealed types (Option, Either, Try), their constructors (Some, None, Left, Right, Success, Failure), and fundamental types (Tuple, Immutable)
- Types from other packages (collection_immutable, io, stream, etc.) require explicit import and are NOT in static completion — they will be handled by the LSP server

**Standard library methods** from `std/*.gala`:
- Public methods (capitalized) on std types: `func (receiver) MethodName(...)`
- Extract with: `grep -h "^func (" std/*.gala | grep -oP '\) [A-Z]\w+' | sed 's/) //' | sort -u`

### Step 2: Compare against plugin

Read these plugin files and compare:

| Plugin File | Data | Source |
|-------------|------|--------|
| `ide/intellij/.../GalaCompletionContributor.kt` | DECLARATION_KEYWORDS | gala.g4 named tokens |
| `ide/intellij/.../GalaCompletionContributor.kt` | CONTROL_KEYWORDS | gala.g4 inline keywords |
| `ide/intellij/.../GalaCompletionContributor.kt` | LITERAL_KEYWORDS | gala.g4 literal rule |
| `ide/intellij/.../GalaCompletionContributor.kt` | TYPE_KEYWORDS | gala.g4 type rule |
| `ide/intellij/.../GalaCompletionContributor.kt` | BUILTIN_TYPES | types.go IsPrimitiveType |
| `ide/intellij/.../GalaCompletionContributor.kt` | STD_AUTO_IMPORTED | std/*.gala auto-imported types only |
| `ide/intellij/.../GalaCompletionContributor.kt` | DOT_METHODS | std/*.gala public methods |
| `ide/intellij/.../GalaAnnotator.kt` | BUILTIN_TYPE_NAMES | types.go IsPrimitiveType |
| `ide/intellij/.../GalaSyntaxHighlighter.kt` | keyword token list | gala.g4 named lexer tokens |

### Step 3: Report discrepancies

Print a table:

```
| Data | Plugin Has | Source Has | Status |
|------|-----------|-----------|--------|
| Keywords | 28 | 25 | 3 EXTRA: break, continue, defer |
| Std Types | 14 | 22 | 8 MISSING: Traversable, ... |
```

### Step 4: Fix (unless `$ARGUMENTS` is `check`)

For each discrepancy, edit the plugin file to match the source of truth:
- Add missing items
- Remove items not in the source
- Keep the same code style (list format, comments)

### Step 5: Verify

After fixes:
```bash
bazel build //ide/intellij:plugin
```

### Step 6: Report

Print summary of changes made.

## Rules

1. **Only add keywords that exist in gala.g4** — if a keyword isn't in the grammar, it's not GALA syntax
2. **Only auto-imported std types in static completion** — `Option`, `Some`, `None`, `Either`, `Left`, `Right`, `Try`, `Success`, `Failure`, `Tuple`, `Immutable` are always available without import
3. **NEVER add importable package types to static completion** — types from `collection_immutable`, `collection_mutable`, `io`, `stream`, `concurrent`, `regex`, `string_utils` etc. require explicit `import` and will be handled by the LSP server
4. **Don't remove method templates** that are conceptually correct even if the exact method signature isn't in std (e.g., `match` is a language keyword, not a method)
5. **Built-in types must be identical** across all 3 files that define them
