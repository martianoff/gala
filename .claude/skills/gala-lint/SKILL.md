---
description: Lint GALA code for best practices and coding standards. TRIGGER when user asks to lint, review, or check GALA code quality, or when reviewing .gala files for style/pattern issues.
user-invocable: true
---

# GALA Best Practices Linter

The rulebook lives in one place, the copy shipped in the Claude Code plugin:
[`ide/claude-code/skills/gala-lint/SKILL.md`](../../../ide/claude-code/skills/gala-lint/SKILL.md).

Read that file now and follow its instructions exactly, passing along
`$ARGUMENTS` (an optional `.gala` file or directory to lint; default: the whole
project).

This file is a pointer rather than a symlink so the skill also loads on checkouts
without symlink support (Windows with `core.symlinks=false`). Edit the rules in
the plugin copy, never here.
