# Lean 4 port: compiler diagnostics

This is a standalone Lean 4 rewrite of the user-facing half of `galaerr`: the
rendering of typed compiler diagnostics, both as the framed terminal message
(`galaerr.RenderRich`) and as the machine-readable JSON envelope
(`galaerr.RenderJSON` / `RenderJSONWithHint`, used by `gala build --json`).

It is the second staged piece of the Lean port after
[`formal/interpolation`](../interpolation). The Go package is a good fit for the
same reasons: it is pure and deterministic once its input is known, both the
CLI and the LSP render through it, and its output is compared byte for byte.

## What is ported

`Diagnostics.lean` implements the same rendering:

| Input | Result |
| --- | --- |
| a diagnostic with a position and source | header, `-->` locus, source line, caret span, hint |
| a diagnostic without a position or source | the header, plus the hint footer |
| an explicit end column | a caret span of that width, clamped to the line |
| no explicit end | a caret over the identifier, quoted string, or character at the column |
| tabs in the source line | preserved in the caret indent, so the caret stays aligned |
| columns | counted in runes; the locus shows the 1-based column, JSON shows 1-based `column`/`endColumn` |
| a long hint | the first clause on the caret row, at most 60 runes with `…`; the full hint in the footer |
| `MultiError` | diagnostics joined by a blank line; JSON flattens them in order |
| an untyped error (e.g. `go build`) | its text unchanged; JSON records severity and message |
| `NO_COLOR`-style colorization | ANSI red/dim/cyan, or plain text; the color flag is an input |
| JSON | Go's field order, `omitempty` rules, HTML escaping off, 2-space indent, U+2028/U+2029 escaped |

`DocsURL` and `StripBOM` are ported too. The normalized `Diagnostic` is the
renderer's own view, so a `SemanticError`, a `SyntaxError` with its position,
and the `diag` case of the port all mean the same thing.

Not modelled, because it is IO rather than rendering:

- **Source resolution.** Go's renderer reads the diagnostic's file when it can
  and otherwise uses the CLI's fallback source. The port takes the source text
  the caller resolved (`fallbackSource`) and keeps the same path/fallback
  rules; the differential harness compares against Go with a path that does not
  exist.
- **`ColorEnabled`.** Whether stderr is a TTY and `NO_COLOR` is unset is the
  caller's decision; the port takes `color`.

## Build

Requires Lean 4.30+ and `leanc` on `PATH`, or `LEAN=/path/to/lean`; the Go
differential test also needs `go`. The flake's `gala-lean` dev shell provides
the nixpkgs Lean that `flake.lock` pins (currently 4.30.0) and Go: from
the repository root, run
`nix develop .#gala-lean` and then the commands below.

```bash
./build.sh
```

This runs Lean's C backend, then links the generated C with the Lean runtime:

```text
lean --c=build/c/Diagnostics.c    Diagnostics.lean
lean --c=build/c/DiagnosticsCli.c DiagnosticsCli.lean
lean --c=build/c/Main.c           Main.lean
leanc -O2 -o build/diagnostics build/c/Main.c build/c/DiagnosticsCli.c build/c/Diagnostics.c
```

### Nix

From the repository root, the same executable builds without a local Lean
installation:

```bash
nix build .#gala-lean.diagnostics
```

The flake also checks the batch protocol below (`checks.<system>.gala-lean-diagnostics`).
The `gala-lean` group marks the staged Lean port; the plain `gala-lean` package
name stays reserved for the complete Lean implementation. The derivation is a
development tool and is not part of a GALA release; the port is not part of the
Lake project, so `formal/lean-toolchain` does not apply to it.

## Protocol

`--batch` reads one case per line:

```text
r|<color>|<fallback-path-hex>|<source-hex>|<json-hint-hex>|<error>|<error>|...
```

`<color>` is `0` or `1`. Strings are hex-encoded UTF-8. An error is either a
typed diagnostic

```text
d:<code-hex>:<message-hex>:<hint-hex>:<file-hex>:<line>:<column>:<endColumn>
```

(0-based `column`/`endColumn`, as in `galaerr`) or an untyped one

```text
x:<message-hex>
```

No error is the nil error; two or more are a `MultiError`. Each case prints:

```text
R <hex of the terminal rendering>
J <hex of the JSON envelope>
--
```

The hex is not for people but for the differential harness: the renderings
contain newlines, tabs, ANSI escapes and non-ASCII text, and hex makes the
comparison exact.

## Verify

```bash
./test.sh
```

This builds everything, runs the native curated tests, then differentially
compares the Lean binary against the real `galaerr` renderers on curated cases,
every caret position of a set of sources, random diagnostics with random
messages/hints/spans, multi-error cases, untyped errors, color on and off, and
JSON hint overrides. The default run uses 20,000 random cases; set `FUZZ_CASES`
and `FUZZ_SEED` to change that.

At the time of writing:

```text
diagnostics tests: ok
differential: 33556 cases, 100668 output lines: MATCH
```

The Go oracle is `go_ref/main.go`; it imports `galaerr`, so the comparison is
against the code the CLI actually uses.

## Layout

| Path | Purpose |
| --- | --- |
| `Diagnostics.lean` | the port: terminal rendering, JSON, `DocsURL`, `StripBOM` |
| `DiagnosticsCli.lean` | native CLI / batch protocol, shared with `formal/gala-lean` |
| `Main.lean` | the standalone executable; calls `DiagnosticsCli.main` |
| `Tests.lean` | curated tests compiled into a native test binary (expectations taken from Go) |
| `go_ref/main.go` | Go differential oracle and case generator |
| `build.sh` | Lean → C → native ELF |
| `test.sh` | native tests + differential verification |
| `build/` | generated `.olean`, C, binaries, and test outputs (gitignored) |

## Scope

This is an executable port, not a machine-checked equivalence proof: the
evidence is differential testing against the production Go code. The production
compiler still renders through `galaerr`. Wiring a Lean component into the
compiler would need a C ABI (or the Lean binary) in the Bazel build; that is not
part of this piece.

See [the interpolation README](../interpolation/README.md) for the general
shape of a staged port and [formal/README.md](../README.md) for where a port
that models what a GALA program *means* belongs.
