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
