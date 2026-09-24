# GALA for VS Code

VS Code support for the [GALA programming language](https://gala.fyi): syntax
highlighting plus the complete GALA language server.

The extension is a thin client. All analysis runs in `gala lsp`, the language
server built into the GALA CLI, so the editor sees exactly what the transpiler
sees.

## Features

- **Syntax highlighting** for keywords, types, strings (including `s"..."` /
  `f"..."` interpolation), comments, numbers and operators.
- **Diagnostics** — parse errors, transpilation errors, unused variables and
  match exhaustiveness, updated as you type.
- **Hover** — type signatures with fields, methods and sealed cases.
- **Go to definition** — across files, into the standard library and into Go
  modules, including `go_interop`.
- **Find references** across all files of a package.
- **Completion** — type-aware dot completion for GALA and Go types, named
  arguments, sealed case patterns and keywords.
- **Signature help** while typing call arguments.
- **Inlay hints** — inferred types on `val` / `var` declarations.
- **Document and workspace symbols**, including sealed variants and
  package-level vals.

## Requirements

The GALA CLI must be installed and include the `lsp` subcommand:

```bash
gala version   # must work in the terminal you start VS Code from
```

Download it from the [releases page](https://github.com/martianoff/gala/releases)
and put it on your `PATH`, or point the extension at it with the
`gala.serverPath` setting or the `GALA_PATH` environment variable.

## Installation

Install the `.vsix` from the [releases page](https://github.com/martianoff/gala/releases):

```bash
code --install-extension gala-vscode-<version>.vsix
```

Or in VS Code: **Extensions** → `...` → **Install from VSIX...**.

## Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `gala.serverPath` | `gala` | Path to the GALA CLI; the extension runs `<path> lsp`. When empty, `GALA_PATH` is used, then `gala` on `PATH`. |
| `gala.trace.server` | `off` | Traces the LSP traffic in the **GALA Language Server** output channel. |

Run **GALA: Restart Language Server** from the command palette after changing
the CLI on disk.

## Building from source

```bash
cd ide/vscode
npm install
npm run compile     # typecheck + esbuild bundle into dist/
npm run package     # produces gala-<version>.vsix
```

To debug, open `ide/vscode` in VS Code and press `F5` — this launches an
Extension Development Host with the extension loaded.

## Tests

`npm test` runs the extension in a real VS Code instance (`@vscode/test-electron`)
and asserts that the extension activates, that `.gala` files get the `gala`
language id, and that diagnostics arrive from the language server. Point it at a
GALA CLI build (a Linux CI needs `xvfb-run`):

```bash
GALA_BIN=/path/to/gala xvfb-run -a npm test
```

Without `GALA_BIN` the run still checks activation and language registration and
skips the language server assertions. The test workspace is
`src/test/fixtures/workspace`.

## Layout

| Path | Purpose |
|------|---------|
| `src/extension.ts` | Language client: spawns `gala lsp` over stdio and wires the document selector. |
| `syntaxes/gala.tmLanguage.json` | TextMate grammar (mirrors the token classes of the ANTLR lexer in `internal/parser/grammar/gala.g4`). |
| `language-configuration.json` | Comments, brackets, auto-closing and indentation rules. |
| `src/test/` | `@vscode/test-electron` end-to-end tests. |

The extension does not embed the GALA CLI and does not implement analysis; the
language server lives in the main repository (`internal/lsp`).
