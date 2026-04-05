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

### 3. Standard Library Types

**Plugin file:** `GalaCompletionContributor.kt` (STD_TYPES)

**Source of truth:** `std/*.gala` — all `type` and `sealed type` declarations

**How to extract:**
```bash
# Types
grep -h "^type \|^sealed type " std/*.gala | sed 's/\[.*//' | awk '{print $NF}'

# Sealed cases (constructors)
grep -h "^    case " std/*.gala | awk '{print $2}' | sed 's/(.*//'
```

### 4. Standard Library Methods (postfix completion)

**Plugin file:** `GalaCompletionContributor.kt` (POSTFIX_TEMPLATES)

**Source of truth:** `std/*.gala` — public method definitions (`func (receiver) MethodName(...)`)

**How to extract:**
```bash
# Public methods (capitalized) on std types
grep -h "^func (" std/*.gala | grep -oP '\) [A-Z]\w+' | sed 's/) //' | sort -u
```

### 5. Collection Types (non-std packages)

**Plugin file:** `GalaCompletionContributor.kt` (STD_TYPES — some are collection types)

**Source of truth:** `collection_immutable/*.gala`, `collection_mutable/*.gala`

**How to extract:**
```bash
grep -rh "^type " collection_immutable/*.gala collection_mutable/*.gala | grep "^type [A-Z]" | sed 's/\[.*//' | awk '{print $2}' | sort -u
```

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
