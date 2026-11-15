# gala-lean: a draft GALA toolchain in Lean

This is a **draft**, combined `gala-lean` executable. It wires the Lean
components that already exist in this repository behind one command line, so
there is a working artifact to grow the Lean implementation from:

| Command | Component | Go counterpart |
|---|---|---|
| `run` | `formal/Gala/` — the reference interpreter (`galaspec`) | `gala run`, via Core JSON |
| `interp` | `formal/interpolation/` — the `s"..."`/`f"..."` splitter | `internal/interpolation` |
| `diag` | `formal/diagnostics/` — terminal and JSON renderers | `galaerr` |
| `mod` | `formal/manifest/` — the `gala.mod` reader and writer | `internal/depman/mod` |

It is **not** a GALA compiler yet. The missing half is the Go pipeline that
parses GALA source, type-checks it and emits Go; nothing in Lean does that
today. `run` therefore takes Core JSON, the surface-syntax document the
formal semantics defines (`internal/formal/coreexport/SCHEMA.md`). On a
`.gala` argument it says so and points at the exporter:

```bash
bazel run //internal/formal/coreexport/galacore -- prog.gala > prog.json
./.lake/build/bin/gala-lean run prog.json
```

The same rules as the rest of `formal/` apply: this is a development and CI
tool, never part of a GALA release (docs/FORMAL_SEMANTICS.MD, "Licensing"),
and nothing here is claimed to be verified beyond what `formal/COVERAGE.md`
lists.

## Build

The package is a Lake project with no dependencies. It borrows the reference
interpreter and the three ports by source directory (`srcDir` in
`lakefile.toml`), so there is one build graph and the ports keep their
standalone executables.

```bash
cd formal/gala-lean
lake build gala-lean
./.lake/build/bin/gala-lean version
```

`lake` is the one from `formal/lean-toolchain` (through elan), or any Lean 4.30+
`lake`; the code compiles with the Lean nixpkgs packages too. The
`test.sh` script builds the binary and drives every subcommand end to end.

With Nix, the same binary is `.#gala-lean.main`:

```bash
nix build .#gala-lean.main
nix run .#gala-lean.main -- version
```

The group `gala-lean` lives in `legacyPackages` because flake `packages` must
be flat. The plain `gala-lean` package name stays reserved for the complete
Lean implementation.

## Commands

```text
gala-lean run [--fuel N] <file.json|->  run a Core JSON program (galaspec)
gala-lean interp [--format] [--batch]   split s"..." / f"..." bodies (stdin)
gala-lean diag [--batch]                render diagnostics (stdin)
gala-lean mod init [module-path]        create gala.mod and gala.sum
gala-lean mod show [gala.mod]           summarize a manifest
gala-lean mod format [gala.mod]         print the canonical manifest
gala-lean mod --batch                   the manifest port's protocol (stdin)
gala-lean version
gala-lean help
```

`run` leaves galaspec's exit codes and outcome values unchanged (`ok` 0,
`panic` 1, `fuel` 2, `unsupported` 3, `malformed` 4); the outcome line is
prefixed `gala-lean:` instead of `galaspec:`. A lone `.gala` argument is
treated as `run`, so `gala-lean prog.gala` explains how to export Core JSON.
`interp`, `diag` and `mod --batch` delegate to the ports' existing command
lines, so their documented batch protocols are exactly the ports' protocols.

`mod init` is the one new user-facing piece. It writes:

```text
module example.com/draft

gala 0.101.0
```

`galaVersion` in `Main.lean` is the release version the draft writes; keep it
in step with the repository's `gala.mod` when the release version changes.
`mod show` and `mod format` parse the manifest with the port and, on a parse
error, render it with the diagnostics port.

## Layout

| Path | Purpose |
|---|---|
| `lakefile.toml` | the Lake package: one library per borrowed component, plus the `gala-lean` executable |
| `Main.lean` | the command line and the `mod init`/`show`/`format` commands |
| `test.sh` | builds and exercises every subcommand end to end |
| `.lake/`, `lake-manifest.json` | Lake's build output and generated (empty) manifest; both gitignored, so the package builds with any Lean 4.30+ Lake |
| `../interpolation/InterpolationCli.lean`, `../diagnostics/DiagnosticsCli.lean`, `../manifest/ManifestCli.lean` | the ports' command logic, shared by their standalone executables and this one |

The port `Main.lean` files are now thin wrappers: they call the `*Cli`
module's `main`. Keeping the command logic in a library module is what lets
one binary import all three without colliding on top-level names.

## Next steps

In rough order of value:

1. A GALA source front end in Lean (lexer/parser to the existing
   `Gala.Syntax`), so `run main.gala` needs no Go exporter;
2. a Lean model of the Go subset the transpiler emits (GoCore) and an
   interpreter for it, so `build`/`transpile` have something to run;
3. `gala-lean build` once there is an emitted-Go model to check;
4. fold more of the Go pipeline into ports, each with its own differential
   oracle, and promote it when it needs the semantics.

Do not claim coverage beyond `formal/COVERAGE.md`. The reference interpreter
covers subset S1 and part of S2; the ports cover their own functions exactly,
under their own differential tests.
