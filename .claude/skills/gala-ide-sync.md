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

**Standard library types** from `std/*.gala`:
- `type` and `sealed type` declarations (public, capitalized names only)
- `case` declarations inside sealed types (constructors like `Some`, `None`, `Left`, `Right`, etc.)

**Standard library methods** from `std/*.gala`:
- Public methods (capitalized) on std types: `func (receiver) MethodName(...)`
- Extract with: `grep -h "^func (" std/*.gala | grep -oP '\) [A-Z]\w+' | sed 's/) //' | sort -u`

**Collection types** from `collection_immutable/*.gala` and `collection_mutable/*.gala`:
- Public type declarations (capitalized names, exclude internal lowercase types)

### Step 2: Compare against plugin

Read these plugin files and compare:

| Plugin File | Data | Source |
|-------------|------|--------|
| `ide/intellij/.../GalaCompletionContributor.kt` | DECLARATION_KEYWORDS | gala.g4 named tokens |
| `ide/intellij/.../GalaCompletionContributor.kt` | CONTROL_KEYWORDS | gala.g4 inline keywords |
| `ide/intellij/.../GalaCompletionContributor.kt` | LITERAL_KEYWORDS | gala.g4 literal rule |
| `ide/intellij/.../GalaCompletionContributor.kt` | TYPE_KEYWORDS | gala.g4 type rule |
| `ide/intellij/.../GalaCompletionContributor.kt` | BUILTIN_TYPES | types.go IsPrimitiveType |
| `ide/intellij/.../GalaCompletionContributor.kt` | STD_TYPES | std/*.gala types + cases |
| `ide/intellij/.../GalaCompletionContributor.kt` | POSTFIX_TEMPLATES | std/*.gala public methods |
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
2. **Sealed cases ARE valid completion items** — `Some`, `None`, `Left`, `Right`, `Success`, `Failure` are constructors users type frequently
3. **Collection types are separate packages**, not std — label them as "collection" not "std" in completion
4. **Don't remove method templates** that are conceptually correct even if the exact method signature isn't in std (e.g., `match` is a language keyword, not a method)
5. **Built-in types must be identical** across all 3 files that define them
