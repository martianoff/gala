# GALA IntelliJ Plugin — Synchronization Guide

This document lists all hardcoded language data in the plugin and its authoritative source.
Run `/gala-ide-sync` to automatically verify and fix sync issues.

## Sync Points

### 1. Keywords

> **Note:** `GalaCompletionContributor.kt` is intentionally empty — all
> completion items (keywords, types, methods, etc.) come from the LSP server.
> The plugin's only hardcoded keyword data is the syntax-highlighter token list
> (sync point #6). Keyword *completion* lives in the LSP server (sync point #7).

**LSP file:** `internal/lsp/completion.go` (`keywordCompletions()`)

**Source of truth:** `internal/parser/grammar/gala.g4` — named lexer rules (VAL, VAR, FUNC, etc.) and inline keyword literals in parser rules ('true', 'false', 'nil', 'map', 'return', etc.)

**How to extract:**
```bash
# Named keyword tokens
grep "^[A-Z_]*:" internal/parser/grammar/gala.g4 | grep "'" | head -20

# Inline keyword literals used in parser rules
grep -oP "'[a-z]+'" internal/parser/grammar/gala.g4 | sort -u
```

### 2. Built-in Types

**Plugin files:** `GalaAnnotator.kt` (BUILTIN_TYPE_NAMES), `GalaSyntaxHighlighter.kt` (BUILTIN_TYPE_NAMES)

**Source of truth:** `internal/transpiler/types.go` — `IsPrimitiveType()` function

**How to extract:**
```bash
grep -A20 "func IsPrimitiveType" internal/transpiler/types.go
```

### 3. Standard Library Auto-Imported Types

**Plugin file:** `GalaAnnotator.kt` (STD_TYPE_NAMES)

**Source of truth:** exported types in `package std` (`std/*.gala`) plus the prelude
list in `internal/transpiler/registry/std.go` (`StdPackageInfo().Types`) — ONLY
types that are auto-imported (available without explicit `import`). These are the
core sealed types and their constructors, the `Tuple`/`Tuple3..10` family, and
fundamental types like `Immutable`, `ConstPtr`, `Void`, `EmbeddedFS`, the
collection traits (`Traversable`, `Iterable`, `Seq`, `Ordered`, `Hashable`), and
the reflection-free codec metadata types (`StructMeta`, `FieldEncoder`,
`FieldDecoder`). Dynamic per-type completion for these comes from the LSP server
via the analyzer's `RichAST`; the plugin set only drives semantic highlighting.

**Important:** Types from other packages (`collection_immutable`, `collection_mutable`, `io`, `stream`, `concurrent`, etc.) require explicit `import` and should NOT be in static completion lists. They will be suggested by the LSP server (Phase 5) when imports are resolved.

**How to extract auto-imported types:**
```bash
# Sealed types + constructors
grep -h "^sealed type \|^    case " std/*.gala | head -20

# Core struct types always available
grep -h "^type " std/tuple.gala std/immutable.gala
```

### 4. Standard Library Methods (dot completion)

**LSP file:** `internal/lsp/completion.go` (`typeSpecificCompletions()`) — driven
dynamically by the analyzer's `RichAST` method metadata; no hardcoded method list.

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
- `strings` — Str, StringBuilder
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

## LSP Server Sync Points

The LSP server (`internal/lsp/`) also has hardcoded data that must stay in sync:

### 7. LSP Completion Keywords

**LSP file:** `internal/lsp/completion.go` — `keywordCompletions()`

**Source of truth:** Same as Plugin Sync Point #1 — `gala.g4` keywords.

### 8. LSP Type Inference

**LSP file:** `internal/lsp/inlay_hints.go` — `inferType()`

**Source of truth:** Transpiler's type system (`internal/transpiler/types.go`). The inference is text-based and covers literals, constructors, and function calls. Full type inference comes from the analyzer's `RichAST`.

### 9. LSP Match Exhaustiveness

**LSP file:** `internal/lsp/diagnostics.go` — `checkMatchExhaustiveness()`

**Source of truth:** Sealed type variants from `RichAST.Types[*].SealedVariants`. No hardcoded data — reads from analyzer metadata at runtime.

## IDE Architecture Overview

```
+------------------+     stdio      +------------------+
|  GoLand/IntelliJ |  <--------->  |    gala-lsp       |
|  Plugin (Kotlin) |    LSP 3.17   |    (Go binary)    |
+------------------+               +------------------+
       |                                    |
       | PSI tree (local)                   | Wraps transpiler
       | Syntax highlighting                | Parser + Analyzer
       | Code folding                       | Type metadata
       | Structure view                     | Cross-file resolution
       | Live templates                     |
       +------------------------------------+
```

**Plugin** handles: syntax highlighting, PSI tree, folding, brace matching, structure view, live templates, color settings.

**LSP server** handles: diagnostics, hover, go-to-definition, completion, inlay hints, document symbols, find references, match exhaustiveness.

## Setup Instructions

### Building

```bash
# Build everything. The LSP server ships as the `gala lsp` subcommand of the
# main gala binary — there is no separate gala-lsp binary.
bazel build //ide/intellij:plugin //cmd/gala:gala
```

### Installing the Plugin

1. Go to GoLand > Settings > Plugins > gear icon > Install from Disk
2. Select `bazel-bin/ide/intellij/gala-intellij-plugin.zip`
3. Restart GoLand

### Installing the LSP Server

The LSP server is the `gala lsp` subcommand of the main `gala` binary, so
installing `gala` on PATH is all that is required.

1. Copy the binary to PATH:
   ```bash
   # Linux/macOS
   cp bazel-bin/cmd/gala/gala_/gala ~/.local/bin/

   # Windows
   copy bazel-bin\cmd\gala\gala_\gala.exe %USERPROFILE%\bin\
   ```
2. Or set the `GALA_PATH` environment variable to the `gala` binary path
3. Restart GoLand — the LSP server (`gala lsp`) starts automatically when a `.gala` file is opened

### Using with Other Editors

The LSP server works with any LSP-capable editor:

**VS Code:** Create `.vscode/settings.json`:
```json
{
  "lsp.servers": {
    "gala": {
      "command": "gala",
      "args": ["lsp"],
      "filetypes": ["gala"]
    }
  }
}
```

**Neovim (lspconfig):**
```lua
require('lspconfig.configs').gala = {
  default_config = {
    cmd = { 'gala', 'lsp' },
    filetypes = { 'gala' },
    root_dir = require('lspconfig.util').root_pattern('gala.mod', '.git'),
  },
}
require('lspconfig').gala.setup({})
```
