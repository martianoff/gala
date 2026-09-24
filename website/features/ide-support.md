---
layout: default
title: "IDE Support — IntelliJ Plugin, LSP Server & Claude Code"
description: "GALA's GoLand/IntelliJ plugin and LSP server provide syntax highlighting, type-aware code completion, inlay hints, and real-time diagnostics — including go-to-definition and completion into Go stdlib and third-party Go modules."
keywords: "gala ide support, gala intellij plugin, gala goland plugin, gala lsp server, gala code completion, gala inlay hints, gala syntax highlighting, gala go to definition, gala go interop completion, gala vscode neovim lsp, gala claude code plugin"
permalink: /features/ide-support/
last_modified_at: 2026-09-13
---

<div class="breadcrumb">
  <a href="{{ '/' | relative_url }}">Home</a> &rsaquo;
  <a href="{{ '/features/' | relative_url }}">Features</a> &rsaquo;
  IDE Support
</div>

# IDE Support

GALA ships with a **GoLand/IntelliJ plugin**, a **VS Code extension** and an **LSP server** that work together to provide a full development experience. The plugins handle local features instantly (syntax highlighting, code folding, live templates), while the LSP server (`gala lsp`) adds type-aware intelligence (diagnostics, completion, inlay hints, go-to-definition).

---

## Syntax Highlighting

The plugin provides rich syntax highlighting for all GALA constructs — keywords, types, string interpolation expressions, comments, operators, and built-in functions. Methods with receivers, pattern matching, and sealed type destructuring are all highlighted with distinct colors.

<img src="{{ '/assets/images/ide/syntax-highlighting.png' | relative_url }}" alt="GALA syntax highlighting in IntelliJ showing methods, pattern matching, and string interpolation" style="max-width: 100%; border: 1px solid #e1e4e8; border-radius: 6px; margin: 1rem 0;">

The screenshot shows methods with receivers (`func (p Person) FullInfo()`), string interpolation (`s"${p.name} (age ${p.age})"`), and pattern matching on sealed types (`case Circle(r) => ...`) — all with distinct semantic coloring.

---

## Type-Aware Code Completion

After typing a dot (`.`), the LSP server resolves the receiver's type and offers **context-aware completions** — only the methods and fields that belong to that type. Each suggestion includes the full type signature.

<img src="{{ '/assets/images/ide/dot-completion.png' | relative_url }}" alt="GALA dot completion popup showing type-aware method and field suggestions for Order type" style="max-width: 100%; border: 1px solid #e1e4e8; border-radius: 6px; margin: 1rem 0;">

Here the cursor is inside a `.Map()` lambda on an `Order` value. The completion popup shows `Order`'s methods (`ApplyDiscount`, `ToSummary`, `Validate`) with return types, and its fields (`id`, `items`, `total`) — all resolved from the transpiler's type information.

---

## Go Interop Intelligence

Values whose type comes from a **Go** package used to be a dead end in the editor: no completion, nothing to click. That gap is closed — Go-backed code now behaves like GALA code.

- **Go stdlib.** Dot completion on a Go value (`val b = bytes.NewBufferString(...)` then `b.`) lists the Go type's method set and fields. Go-to-definition on a package name, a package function, or a method navigates into the Go SDK source tree.
- **Cross-package return types.** A call like `sha256.New()` returns `hash.Hash`, defined in a package you never imported. The analyzer records the method set of named types reached through a function's return values, so completion still works on the result.
- **Third-party Go modules.** Modules such as `github.com/google/uuid` resolve the same way the stdlib does: the LSP maps each module import path to its cached `.go` source directory using the same logic the compiler uses, so their types get completion and their symbols get go-to-definition.
- **`go_interop` and Go-only packages** are navigable — imports are resolved straight from the document, so both the package name and `pkg.Symbol` references land on their declaration.

### `.Size()` / `.ByteSize()` magic methods

The transpiler lowers `.Size()` / `.ByteSize()` on Go primitive receivers (string, slice, map) to `len(...)` / `utf8.RuneCountInString(...)`. Those receivers carry no GALA type metadata, so the editor used to know nothing about them. The LSP now mirrors the transpiler's rule: both resolve to `int`, `Size()` is offered on string/slice/map and `ByteSize()` on string, and neither raises a false diagnostic. Because these methods have **no source definition**, go-to-definition on them correctly resolves to nothing rather than jumping to an unrelated same-named method.

---

## Inlay Type Hints

The LSP server displays **inferred types** as inline hints next to `val` and `var` declarations. These hints come directly from the transpiler's type resolver — the same types used for code generation — so they are always accurate.

### Basic declarations and tuples

<img src="{{ '/assets/images/ide/inlay-hints-basic.png' | relative_url }}" alt="GALA inlay hints showing inferred types for tuple declarations and function return values" style="max-width: 100%; border: 1px solid #e1e4e8; border-radius: 6px; margin: 1rem 0;">

Inlay hints show `Tuple[int, int]` for pair declarations, `int` for simple values, and track types through function returns — all without explicit type annotations in the source code.

### Method chains with Option types

<img src="{{ '/assets/images/ide/inlay-hints-chains.png' | relative_url }}" alt="GALA inlay hints tracking types through Option Map and GetOrElse method chains" style="max-width: 100%; border: 1px solid #e1e4e8; border-radius: 6px; margin: 1rem 0;">

The transpiler tracks types through method chains: `validated.Map(...)` produces `Option[Order]`, chaining `.Map((o) => o.ToSummary())` produces `Option[string]`, and `.GetOrElse("No order")` unwraps to `string`. Each step is visible in the editor.

---

## Structure View

The plugin provides a **structure view** panel showing the outline of your GALA file — types, sealed variants, methods, and fields at a glance.

<img src="{{ '/assets/images/ide/structure-view.png' | relative_url }}" alt="GALA structure view showing sealed type Shape with Circle, Rectangle, Triangle variants and their fields" style="max-width: 100%; border: 1px solid #e1e4e8; border-radius: 6px; margin: 1rem 0;">

The structure view displays sealed type `Shape` with its variants (`Circle`, `Rectangle`, `Triangle`), each variant's fields, and the auto-generated `Apply`/`Unapply` companion methods.

---

## Full Feature List

### Plugin features (local, no LSP needed)

- Full ANTLR-based parser with complete PSI tree
- Syntax highlighting for keywords, types, strings, comments, operators, built-in functions, and std types
- Semantic annotator for built-in types, std types, built-in functions, and string interpolation
- Code folding for blocks, sealed types, and imports
- Brace matching
- Structure view with functions, types, and sealed types with cases
- Comment/uncomment
- Color settings page
- Keyword support for `use` (scoped-resource binding) and `bind` / `also` (do-notation), with the bound name clickable, renameable, and find-usages-aware
- **12 live templates**: `func`, `val`, `var`, `match`, `if`, `for`, `sealed`, `struct`, `lambda`, `main`, `println`, `sinterp`

### LSP features (via `gala lsp`)

- **Diagnostics** — parse errors, transpilation errors, unused variables, match exhaustiveness, and the surface guardrails: bare Go builtins (**GALA-E0035**) and bare Go statement keywords such as `defer` (**GALA-E0036**)
- **Hover** — type signatures with fields, methods, sealed cases, built-in function docs
- **Go to Definition** — cross-file (including the files of a multi-file `main` program), local declarations, pattern bindings, named arg fields, Go stdlib and third-party Go module sources, `go_interop` and other Go-only packages
- **Find References** — usages of a name across all files of its package
- **Completion** — type-aware dot completion (GALA *and* Go types), named arguments, sealed case patterns, keywords including `use`/`bind`/`also`, and `.Size()`/`.ByteSize()` on Go primitives
- **No dead ends** — the E0035-forbidden builtins (`len`, `append`, `make`, `panic`, …) are filtered out of completion against the transpiler's own authoritative list, so the editor never suggests code the compiler rejects
- **Inlay hints** — compiler-inferred types for all `val`/`var` declarations
- **Document symbols** — the file's own types, sealed variants, methods, functions and package-level vals, at their exact positions
- **Workspace symbols** — search declarations by name across every `.gala` file in the project
- **Debounced analysis** — 500ms delay after last keystroke to prevent noise while typing

---

## Installation

### GoLand / IntelliJ IDEA

1. Install the GALA CLI from [releases](https://github.com/martianoff/gala/releases) and add it to your PATH
2. Install the plugin: **Settings > Plugins > Install from Disk** > select `gala-intellij-plugin.zip` from [releases](https://github.com/martianoff/gala/releases)
3. Restart the IDE — the LSP server starts automatically when you open a `.gala` file

### VS Code

Install the GALA extension from the [releases](https://github.com/martianoff/gala/releases) page:

```bash
code --install-extension gala-vscode-<version>.vsix
```

Or in VS Code: **Extensions** > `...` > **Install from VSIX...**. The extension is a thin client: it registers the `gala` language for `.gala` files (syntax highlighting, comments, brackets) and starts `gala lsp` for every feature listed above — diagnostics, completion, inlay hints, go-to-definition and symbols. It requires the GALA CLI on `PATH` (check `gala version`); point it elsewhere with the `gala.serverPath` setting or the `GALA_PATH` environment variable.

### Neovim

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

### Claude Code

The GALA plugin connects [Claude Code](https://code.claude.com) to `gala lsp`, so an AI agent editing `.gala` files uses the same analysis as your editor:

- **Diagnostics after every edit.** Parse errors, non-exhaustive matches and other transpiler errors are pushed into Claude's context right after it changes a file, so it fixes them without running a build.
- **Hover, go-to-definition and find references.** Claude looks up inferred types and declarations instead of guessing them.
- **A skill that steers Claude to the language server.** It edits `.gala` files with the tools that report diagnostics and finds standard library methods through definitions and symbols instead of grepping.

The plugin starts the `gala` binary but does not include it. Install the GALA CLI first (see [Getting Started]({{ '/getting-started/' | relative_url }})) and check that `gala version` works in the terminal you start Claude Code from. Then run in Claude Code:

```
/plugin marketplace add martianoff/gala
/plugin install gala@gala
```

Projects created with `gala new` include a `.claude/settings.json` that declares the marketplace, so Claude Code installs and enables the plugin once you trust the project folder.

If `gala` is not on the PATH, Claude's LSP tool fails with `Command 'gala' not found` and no GALA diagnostics reach it. Diagnostics cover what the GALA transpiler checks; errors that only the Go compiler finds in the generated code still need `gala build`. Full instructions: [ide/claude-code](https://github.com/martianoff/gala/tree/master/ide/claude-code).

### Verify

Run `gala lsp` in a terminal. It should start and wait for JSON-RPC messages on stdin/stdout. If you see no output and no errors, the server is running correctly.
