# GALA IntelliJ Plugin

IntelliJ IDEA language support plugin for GALA (Go Alternative LAnguage).

## Features

- **Full ANTLR-based parser** — complete PSI tree for all GALA constructs
- **Syntax highlighting** for keywords, types, strings, comments, numbers, operators
- **Code completion** for keywords, built-in types, and standard library types
- **Structure view** showing functions, types, sealed types with cases, vals, vars
- **Brace matching** for `()`, `{}`, `[]`
- **Code folding** for blocks, sealed type bodies, and import groups
- **Comment/uncomment** support (`Ctrl+/` for line, `Ctrl+Shift+/` for block)
- **File type recognition** (`.gala`)

## How to Build

```bash
bazel build //ide/intellij:plugin
```

The resulting ZIP file will be at `bazel-bin/ide/intellij/gala-intellij-plugin.zip`.

This single command generates the ANTLR Java lexer/parser from `gala.g4`, copies them into the plugin source tree, and runs Gradle to compile and package the plugin.

## How to Install

1. Open IntelliJ IDEA (2024.1+)
2. Go to **Settings** > **Plugins**
3. Click the gear icon and select **Install Plugin from Disk...**
4. Select `bazel-bin/ide/intellij/gala-intellij-plugin.zip`
5. Restart IntelliJ IDEA

## Architecture

The plugin uses the [antlr4-intellij-adaptor](https://github.com/antlr/antlr4-intellij-adaptor) library to bridge the ANTLR4 grammar (`gala.g4`) directly into IntelliJ's PSI tree:

- **Single source of truth** — the GALA grammar is defined once in `gala.g4`
- **Bazel generates** Java lexer/parser from the grammar (patching `-> skip` to `-> channel(HIDDEN)`)
- **Gradle packages** the IntelliJ plugin (invoked by Bazel)
- Generated files live in `src/main/gen/` (gitignored)

## Known Limitations

These features require the LSP server to be connected to the plugin (SDK 2025.2+) or dedicated implementation work:

| Feature | Status | Blocker |
|---------|--------|---------|
| Cross-file/cross-package navigation | Not supported | Needs LSP or stub indexing |
| Import-aware completion | Not supported | Needs LSP — plugin doesn't parse imports |
| Type inference / inlay hints | Not supported | Needs LSP — transpiler has this in RichAST |
| Type-aware dot completion | Not supported | Needs type info at cursor position |
| Named argument field completion | Not supported | Needs struct field metadata from analyzer |
| Pattern match case completion | Not supported | Needs sealed type variant info |
| Match exhaustiveness checking | Not supported | Needs sealed type + case analysis |
| Go interop (Go type navigation) | Not supported | Needs Go type info from analyzer |
| Real error highlighting | LSP server has it | Not connected to plugin yet |
| Rename refactoring | Stub only | Needs ANTLR node replacement |
| Auto-import / organize imports | Not supported | Needs package registry |
| Code formatting | Not supported | Needs formatter implementation |
| Extract variable/function | Not supported | Needs type-aware refactoring |

### Key source files

| File | Purpose |
|------|---------|
| `psi/GalaTokenTypes.kt` | Bridges ANTLR tokens/rules to IntelliJ `IElementType`s |
| `psi/GalaPsiNodes.kt` | Custom PSI node types for declarations |
| `GalaParserDefinition.kt` | Wires ANTLR lexer/parser to IntelliJ |
| `GalaSyntaxHighlighter.kt` | Token-based syntax highlighting |
| `GalaStructureViewElement.kt` | Structure view tree |
| `GalaFoldingBuilder.kt` | Code folding regions |
| `GalaCompletionContributor.kt` | Keyword and type completion |
