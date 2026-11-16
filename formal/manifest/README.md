# Lean 4 port: the `gala.mod` manifest

This is a standalone Lean 4 rewrite of `internal/depman/mod`: the parser and
formatter of `gala.mod`, the module system's manifest. It is the third staged
piece of the Lean port after
[`formal/interpolation`](../interpolation) and
[`formal/diagnostics`](../diagnostics).

The Go package is a good fit for the same reasons: it is pure once the file
text is known, it has no parser or transpiler state, `gala mod init`, `gala mod
add`, `gala mod remove`, module resolution and the LSP all go through it, and
its output is compared byte for byte.

## What is ported

`Manifest.lean` implements the same API and keeps the same behavior:

| Go | Lean |
| --- | --- |
| `Parse(content)` | `parse content` |
| `Format(file)` | `format file` |
| `File.Module`, `Gala`, `Require`, `Replace`, `Exclude` | `File.module`, `gala`, `requires`, `replaces`, `excludes` |
| `Require.Path`, `Version`, `Indirect`, `Go` | `Require.path`, `version`, `indirect`, `go` |
| `Replace.Old`, `New`, `ModuleVersion` | `Replace.old`, `new`, `ModuleVersion` |
| `Exclude` | `Exclude` |
| `NewFile` | `newFile` |
| `AddRequire`, `RemoveRequire`, `GetRequire` | `addRequire`, `removeRequire`, `getRequire` |
| `AddReplace`, `AddExclude` | `addReplace`, `addExclude` |
| `DirectRequires`, `IndirectRequires`, `GalaRequires`, `GoRequires` | `directRequires`, `indirectRequires`, `galaRequires`, `goRequires` |
| `ModuleVersion.IsLocal` | `ModuleVersion.isLocal` |
| `ParseError.Error()` | `ParseError.toString` |

The parser keeps the Go code's quirks, because the differential harness
compares them:

| Input | Result |
| --- | --- |
| a leading BOM | dropped exactly once |
| only a second BOM | part of the first directive, so `unknown directive: <BOM>module` |
| `//` anywhere | starts a comment, even inside a path |
| comment markers | read from the first comment, split on commas; only exact `indirect` and `go` count, in any order |
| a line ending in `(` | opens a block for `require`, `replace` or `exclude`; any other word there is `unknown block directive` |
| `)` | closes the open block; outside a block it is ignored |
| `// indirect` on a line in a `require` block | sticks to the following entries of that block |
| `// indirect` on the `require (` line | dropped with the header line |
| a line inside a block | is that block's entry, even if it starts with another directive's name |
| extra fields | ignored (`require a v extra` reads `a` and `v`) |
| Go whitespace | `strings.TrimSpace` / `bufio.ScanWords` rules: `\t\n\v\f\r`, space, U+0085, U+00A0 and Unicode `White_Space` — wider than Lean's `Char.isWhitespace` |
| formatting | a single entry stays on one line, two or more become a block; markers print `// indirect, go`; every trailing newline is trimmed and one is written |

`AddRequire` keeps its Go quirk too: updating an existing path changes its
version and `indirect` flag in place but leaves its `go` flag alone, and only
the first duplicate is updated. `AddReplace` updates only the new side of an
existing `(old path, old version)` pair, and `AddExclude` deduplicates.

Not modelled, because they are IO or outside the accepted input:

- **File IO.** `ParseFile` and `WriteFile` are the caller's job; the port takes
  the text and returns the text. The differential harness reads and writes no
  manifest files.
- **Invalid UTF-8.** Lean strings are Unicode and the protocol rejects invalid
  UTF-8, while Go strings carry arbitrary bytes. The Go oracle generates valid
  UTF-8 only; everything else (including non-UTF-8 in comments) is outside the
  compared domain.
- **`bufio.Scanner`'s 64 KiB token limit.** Go's `splitFields` ignores the
  scanner's `ErrTooLong`, so a line with a field of 64 KiB or more loses that
  field and everything after it; the port parses the whole line. The harness's
  generated lines stay far below the limit, so this is not compared.

## Build

Requires Lean 4.35+ and `leanc` on `PATH`, or `LEAN=/path/to/lean`; the Go
differential test also needs `go`. The flake's `gala-lean` dev shell provides
the nixpkgs Lean that `flake.lock` pins (currently 4.35.0) and Go: from
the repository root, run
`nix develop .#gala-lean` and then the commands below.

```bash
./build.sh
```

This runs Lean's C backend, then links the generated C with the Lean runtime:

```text
lean --c=build/c/Manifest.c    Manifest.lean
lean --c=build/c/ManifestCli.c ManifestCli.lean
lean --c=build/c/Main.c        Main.lean
leanc -O3 -flto -DNDEBUG -o build/manifest build/c/Main.c build/c/ManifestCli.c build/c/Manifest.c
```

`build.sh` uses these flags by default; set `LEANC_FLAGS` to override them.

`ManifestCli.lean` also holds the command logic `formal/gala-lean` imports for
its `mod` subcommand; `Main.lean` is the standalone executable and only calls
`ManifestCli.main`.

### Nix

From the repository root, the same executable builds without a local Lean
installation:

```bash
nix build .#gala-lean.manifest
```

The flake also checks the batch protocol below (`checks.<system>.gala-lean-manifest`).
The `gala-lean` group marks the staged Lean port; the plain `gala-lean` package
name stays reserved for the complete Lean implementation. The derivation is a
development tool and is not part of a GALA release; the port is not part of the
Lake project, so `formal/lean-toolchain` does not apply to it.

## Protocol

`--batch` reads one case per line:

```text
c|<content-hex>|<operation>|<operation>|...
```

`<content-hex>` is the manifest text, hex-encoded UTF-8. Operations are applied
to the parsed file in order; their string parameters are hex too:

| Operation | Go call | Prints |
| --- | --- | --- |
| `a:<path>:<version>:<0\|1>` | `AddRequire` | nothing |
| `d:<path>` | `RemoveRequire` | `remove <0\|1>` |
| `g:<path>` | `GetRequire` | `get 0`, or `get 1 <version> <indirect> <go>` |
| `p:<old path>:<old version>:<new path>:<new version>` | `AddReplace` | nothing |
| `e:<path>:<version>` | `AddExclude` | nothing |
| `direct`, `indirect`, `gala`, `go` | the filtered views | `<name> <count> [<hex path>...]` |
| `format` | `Format` | `format <hex>` |

A successful case prints:

```text
P
module <hex path>
gala <hex version>
require <hex path> <hex version> <0|1 indirect> <0|1 go>
replace <hex old path> <hex old version> <hex new path> <hex new version>
exclude <hex path> <hex version>
O
<one line per operation that returns something>
F <hex of Format>
--
```

The `P` dump comes after the operations, so it shows their effect; an operation
that only returns a value prints in the `O` section. A parse error prints
`E <hex of the Go error string>` and `--` instead; operations are not applied
to a file that did not parse.

## Verify

```bash
./test.sh
```

This builds everything, runs the native curated tests, then differentially
compares the Lean binary against `internal/depman/mod` on curated manifests and
operations, every sequence of up to three lines from a fixed pool, every rune
Go's whitespace predicates accept, and random manifests/operations. The default
run uses 20,000 random cases; set `FUZZ_CASES` or `FUZZ_SEED` to change that.
