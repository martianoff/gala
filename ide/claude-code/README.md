# GALA plugin for Claude Code

> **Requires the GALA CLI.** This plugin only tells Claude Code to start
> `gala lsp`. It does not include the `gala` binary. Install GALA and make sure
> `gala version` works in the terminal you start Claude Code from before you
> install the plugin.

Connects [Claude Code](https://code.claude.com) to the GALA language server
(`gala lsp`), so Claude works on `.gala` files with the same type information
your editor has:

- **Diagnostics after every edit.** Parse errors, non-exhaustive matches, the
  `GALA-E*` guardrails and other transpiler errors are pushed into Claude's
  context right after it changes a file. It fixes them without waiting for a
  build.
- **Hover.** Claude can ask for the inferred type of any `val`, lambda parameter
  or expression, plus the documentation of the symbol.
- **Go to definition and find references**, across GALA packages, the Go
  standard library and third-party Go modules.

## Skills

The plugin ships two skills.

- **`gala-code-intelligence`** loads automatically when Claude works on `.gala`
  files. It tells Claude to edit them with its Edit and Write tools (shell edits
  never reach the language server, so they get no diagnostics), to look up
  inferred types and std signatures through the language server instead of
  guessing or grepping cache directories, and to finish with a build.
- **`/gala-lint`** is the best-practice rulebook: the `GALA-E*` hard errors, the
  immutability and sealed-type conventions, the `Option`-over-sentinel rules and
  the expression-body style the compiler's own codebase follows. Run it on a
  file, a directory, or the whole project.

The rulebook applies to any GALA project, which is why it ships here rather than
staying in the compiler repo — a downstream project is where a linter is most
needed. This directory holds the canonical copy; the compiler repo's own
`.claude/skills/gala-lint` is a short pointer to it (not a symlink, so it also
works on Windows checkouts), so there is one copy to keep current.

Three skills in the compiler repo are deliberately **not** shipped, because they
assume that repo: `gala-code` (generates GALA with tests, but its layout and
build steps are Bazel- and monorepo-shaped), `gala-ide-sync` (re-syncs the
IntelliJ plugin and LSP server against the grammar) and `gala-lsp`
(language-server development).

## Install

1. **Install the GALA CLI** and put `gala` on your `PATH`: download a binary
   from [releases](https://github.com/martianoff/gala/releases) or follow
   [Getting Started](https://gala.fyi/getting-started/). Check it with:

   ```
   gala version
   ```

2. **Install the plugin** in Claude Code:

   ```
   /plugin marketplace add martianoff/gala
   /plugin install gala@gala
   ```

   The same from a shell: `claude plugin marketplace add martianoff/gala` and
   `claude plugin install gala@gala`.

Projects created with `gala new` already contain a `.claude/settings.json` that
declares this marketplace and enables the plugin. When you trust the project
folder, Claude Code installs and enables the plugin on its own; you still need
step 1. To do the same for an existing project, put this in its
`.claude/settings.json`:

```json
{
  "extraKnownMarketplaces": {
    "gala": {
      "source": { "source": "github", "repo": "martianoff/gala" }
    }
  },
  "enabledPlugins": {
    "gala@gala": true
  }
}
```

## Check that it works

Ask Claude to hover a symbol in a `.gala` file, for example *"use the LSP tool to
hover `Println` in main.gala"*. It should return the symbol's signature.

If `gala` is missing from the `PATH` Claude Code was started with, the LSP tool
fails with:

```
Command 'gala' not found or is in an unsafe location (current directory)
```

and Claude gets no GALA diagnostics. Install the CLI (step 1) and restart Claude
Code.

Starting Claude Code with `claude --debug` logs the server's start-up line,
which names the GALA version and the standard library directory it resolved:

```
[gala-lsp] version=0.77.0 stdlib=/home/you/.gala/stdlib/v0.77.0
```

## Limits

- The server reports what the GALA transpiler detects. Errors that only the Go
  compiler finds in the generated code, such as adding a `float64` to a
  `string`, need `gala build` or `bazel build`.
- Claude receives diagnostics with the result of its next tool call after the
  edit, not in the edit's own result.
