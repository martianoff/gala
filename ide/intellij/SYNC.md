# GALA IntelliJ Plugin — Synchronization Guide

This document lists all hardcoded language data in the plugin and its authoritative source.
Run `/gala-ide-sync` to automatically verify and fix sync issues.

## Sync Points

### 1. Keywords

**Plugin files:** `GalaCompletionContributor.kt` (DECLARATION_KEYWORDS, CONTROL_KEYWORDS, LITERAL_KEYWORDS, TYPE_KEYWORDS)

**Source of truth:** `internal/parser/grammar/gala.g4` — named lexer rules (VAL, VAR, FUNC, etc.) and inline keyword literals in parser rules ('true', 'false', 'nil', 'map', 'return', etc.)

**How to extract:**
```bash
# Named keyword tokens
grep "^[A-Z_]*:" internal/parser/grammar/gala.g4 | grep "'" | head -20

# Inline keyword literals used in parser rules
grep -oP "'[a-z]+'" internal/parser/grammar/gala.g4 | sort -u
```

### 2. Built-in Types

**Plugin files:** `GalaCompletionContributor.kt` (BUILTIN_TYPES), `GalaAnnotator.kt` (BUILTIN_TYPE_NAMES), `GalaSyntaxHighlighter.kt` (BUILTIN_TYPE_NAMES)

**Source of truth:** `internal/transpiler/types.go` — `IsPrimitiveType()` function

**How to extract:**
```bash
grep -A20 "func IsPrimitiveType" internal/transpiler/types.go
```

### 3. Standard Library Auto-Imported Types

**Plugin file:** `GalaCompletionContributor.kt` (STD_AUTO_IMPORTED)

**Source of truth:** `std/*.gala` — ONLY types that are auto-imported (available without explicit `import`). These are the core sealed types, their constructors, and fundamental types like `Tuple` and `Immutable`.

**Important:** Types from other packages (`collection_immutable`, `collection_mutable`, `io`, `stream`, `concurrent`, etc.) require explicit `import` and should NOT be in static completion lists. They will be suggested by the LSP server (Phase 5) when imports are resolved.

**How to extract auto-imported types:**
```bash
# Sealed types + constructors
grep -h "^sealed type \|^    case " std/*.gala | head -20

# Core struct types always available
grep -h "^type " std/tuple.gala std/immutable.gala
```

### 4. Standard Library Methods (postfix completion)

**Plugin file:** `GalaCompletionContributor.kt` (POSTFIX_TEMPLATES)

**Source of truth:** `std/*.gala` — public method definitions (`func (receiver) MethodName(...)`)

**How to extract:**
```bash
# Public methods (capitalized) on std types
grep -h "^func (" std/*.gala | grep -oP '\) [A-Z]\w+' | sed 's/) //' | sort -u
```

### 5. Importable Package Types (NOT in static completion)

Collection types, IO types, stream types, etc. are NOT in static completion lists.
They require explicit `import` and will be handled by the LSP server (Phase 5).

Packages with importable types:
- `collection_immutable` — Array, List, HashMap, HashSet, TreeMap, TreeSet
- `collection_mutable` — Array, List, HashMap, HashSet, TreeMap, TreeSet
- `io` — Reader, Writer, etc.
- `stream` — Stream
- `concurrent` — Future, ExecutionContext
- `string_utils` — StringBuilder
- `regex` — Regex, Match

### 6. Syntax Highlighter Token Mapping

**Plugin file:** `GalaSyntaxHighlighter.kt` — keyword token list in `getTokenHighlights()`

**Source of truth:** `internal/parser/grammar/gala.g4` — named lexer tokens, mapped to `galaLexer.*` constants

**How to verify:** Compare the list of `galaLexer.XXX` entries in the `when` block against named tokens in gala.g4. The ANTLR-generated Java lexer constants are always authoritative — if a new keyword is added to gala.g4, regenerate the Java sources and add the new constant to the highlighter.

## When to Sync

Sync the plugin after any of these changes:
- **Grammar change** (`gala.g4`) — keywords, new syntax
- **New std type** (`std/*.gala`) — types, sealed cases, methods
- **New collection type** (`collection_immutable/`, `collection_mutable/`)
- **Built-in type change** (`internal/transpiler/types.go`)

## Automated Sync

Run the Claude skill:
```
/gala-ide-sync
```

This reads the sources of truth, compares against the plugin code, and fixes any discrepancies.
