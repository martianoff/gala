# LSP integration test suite

End-to-end tests that drive the **real `gala lsp` binary** the way an editor
does: spawn the process, speak LSP JSON-RPC 3.17 over stdin/stdout, send
`initialize` / `textDocument/didOpen` / `textDocument/inlayHint`, and read back
`publishDiagnostics` notifications and request responses.

## Why this suite exists

The in-process handler tests (`internal/lsp/*_test.go`) construct `GalaHandler`
directly and call its methods. That misses an entire class of bugs that only
appear at the process boundary:

- **URI handling** — editors send `file:///abs/path`; the in-process harness
  historically built `file:////abs/path` (four slashes), which accidentally
  hid a bug where `uriToPath` dropped the POSIX leading slash. That single bug
  silently disabled **sibling-file discovery for every analyzed file**, so the
  analyzer never saw the rest of a package and emitted false positives like
  *"unused variable"* and *"cannot infer type of matched expression"* on code
  that `gala build` compiles cleanly.
- **Project-root resolution** (`initialize.rootUri`, `gala.mod`, dependency
  search paths).
- **The embedded standard library** shipped inside the binary, rather than the
  `std/` source tree the in-process tests point at.

These can only be exercised through the actual binary with real-editor URIs.
This suite is the canonical home for such regressions.

## What's covered

`diagnostics_test.go`:

- `TestCrossFileSealedTypeNoFalsePositives` — a sealed type with a nullary
  variant (`case JNull`, no parens) defined in one file and used from siblings
  via a bare-constructor match and a cross-file method chain. Asserts the whole
  package resolves and **no file reports diagnostics**. This is the direct
  regression for the URI / sibling-discovery outage.
- `TestPathWithSpacesResolvesSiblings` — the project lives under a directory
  whose name contains a space, so the URI is percent-encoded (`%20`). Verifies
  `uriToPath` decodes it and sibling discovery still works.

## Running

```sh
# Bazel (uses the freshly built binary via the //cmd/gala data dependency):
bazel test //internal/lsp/lspintegration:lspintegration_test

# Plain go test (falls back to `gala` on PATH; skips if not found):
go test ./internal/lsp/lspintegration/...
```

## Adding a regression

1. Add a `*.gala` fixture map to `writeFixture` inside a new `Test...` function.
   Reproduce the failing editor scenario as closely as possible — most LSP
   regressions are **cross-file**, so include the sibling files.
2. Drive it through `startLSP` → `initialize` → `openFile` (open every sibling,
   the same as an editor indexing a package).
3. Assert on `errorDiagnostics` (absence of false positives) and/or on a
   feature response (hover, definition, inlay hints) for positive coverage.
4. Confirm the test **fails before** the fix and **passes after** — the harness
   builds real-editor URIs (`pathToFileURI`), so it genuinely exercises the
   process boundary.

The minimal JSON-RPC client lives in `client_test.go`; extend it if you need a
new request type.
