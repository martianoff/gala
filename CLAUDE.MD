# GALA Project - Claude Code Instructions

@AGENTS.md

---

## Claude Code specifics

The rules above are shared by every agent; this section only adds what is specific to
Claude Code. Keep project rules in `AGENTS.md`, not here.

**Environment:** If `~/.claude/CLAUDE.md` (global instructions) exists, follow its
Environment, Shell, and File Path conventions. Those global settings override any
defaults in this file.

- Project skills live in `.claude/skills` (`gala-code`, `gala-lint`, `gala-pr-review`,
  `gala-ide-sync`, `gala-lsp`); the installable plugin in `ide/claude-code` adds
  `gala-code-intelligence` and wires `gala lsp` in as the language server.
- The PR checklist's review passes map to `/gala-lint`, `/simplify`, and `/review`.
- Disclose AI assistance with a `Co-Authored-By:` trailer naming the model.
