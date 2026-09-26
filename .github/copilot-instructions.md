# GALA - GitHub Copilot Instructions

All agent instructions for this repository live in [AGENTS.md](../AGENTS.md) at the
repository root. Read and follow it before making changes — in particular its
**CRITICAL RULES** and **Pull Request Checklist**.

The most important points, for tools that do not follow links:

- Build and test only with Bazel: `bazel build //...`, `bazel test //...` (never `go test`).
- Never hand-write or commit the generated parser (`internal/parser/grammar/*.go`); edit `gala.g4`.
- The transpiler never special-cases the standard library and never emits `any`/`interface{}`
  unless the GALA source asked for it.
- Fix transpiler bugs with a repro test; never work around them in GALA code.
- No internal ticket IDs or private tracker links in code, docs, or commit messages.
- Read `docs/GALA_BEST_PRACTICES.MD` before writing GALA; GALA is not Scala or Kotlin.
- Disclose AI assistance in the PR (template checkbox + `Co-Authored-By:` trailer).
