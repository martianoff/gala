# Lean 4 port: GALA interpolation splitter

This is a standalone Lean 4 rewrite of `internal/interpolation`, the pure
byte-level lexer that splits the body of a GALA interpolated (`s"..."`) or
format (`f"..."`) string into literal and embedded-expression parts.

The Go original is a good pilot for a Lean component because it is explicitly
independent: it carries no parser or transpiler state, both the transformer and
the concurrency capture analysis share it, and its output is deterministic down
to byte offsets.

## What is ported

`Interpolation.lean` implements the same API:

| Go | Lean |
| --- | --- |
| `Split(content, formatSpecs)` | `split content formatSpecs` |
| `Part.Offset`, `Part.Start`, `Part.End` | `byteOffset`, `start`, `endPos` |
| `Part.PrintedByString` | `printedByString` |
| `Position(content, offset, line, col)` | `position content offset line col` |
| `EndOfEmbeddedExpr` | `endOfEmbeddedExpr` |

and the same mini-syntax:

| Input | Result |
| --- | --- |
| `$name` | expression part `name` |
| `${ expr }` | expression part `expr` |
| `$$` | literal `$` |
| `\x` | literal escape pair (the `\` prevents syntax interpretation) |
| `\"` inside `${...}` | unescaped to `"` |
| `${x}%04d`, `$x%s` | optional Go printf spec on the expression part — only with `formatSpecs = true`, i.e. for an `f"..."` body; in an `s"..."` body a `%` is literal text |
| `${` / `${a` | unterminated expression: text to end of content |

Identifiers are ASCII (`_`, `a-z`, `A-Z`) followed by `_`, letters, or digits;
`$` followed by anything else is literal. All offsets are byte offsets into the
content passed to `split`, as are `start` and `endPos`; a format spec after an
expression is not included in its span. The accepted printf flags, widths,
precisions, and verb set match the Go implementation exactly.

`printedByString` reports whether `fmt` prints an embedded expression as a
`Println` operand (no spec, or `%v`/`%s` without `#`). `position` returns the
line and rune column of a byte offset; it counts an invalid UTF-8 byte as one
rune, as Go's `utf8.RuneCount` does.

The port uses `ByteArray` throughout, not `String`, so non-UTF-8 bytes and
offsets are handled exactly like the Go version. The small scanners carry fuel
so their termination is structural; the top-level byte loop is `partial`
because its bound is the input size, and skipping a per-byte fuel argument is
what keeps the native port competitive.

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
lean --c=build/c/Interpolation.c    Interpolation.lean
lean --c=build/c/InterpolationCli.c InterpolationCli.lean
lean --c=build/c/Main.c             Main.lean
leanc -O3 -flto -DNDEBUG -o build/interpolation build/c/Main.c build/c/InterpolationCli.c build/c/Interpolation.c
```

`build.sh` uses these flags by default; set `LEANC_FLAGS` to override them.

`build/interpolation` is a normal native Linux ELF executable (x86-64,
dynamically linked against the host glibc; the Lean runtime is linked in).

### Nix

From the repository root, the same executable builds without a local Lean
installation:

```bash
nix build .#gala-lean.interpolation
printf 'a $name b' | ./result/bin/interpolation
```

`nix run .#gala-lean.interpolation -- --batch < build/cases.txt` runs it without
the intermediate `result` symlink. The flake uses the Lean packaged by its
pinned nixpkgs and runs `Tests.lean` in the derivation's check phase. The
`gala-lean` group marks the staged Lean port; the plain `gala-lean` package
name stays reserved for the complete Lean implementation. The derivation is a
development tool and is not part of a GALA release;
the port is not part of the Lake project, so `formal/lean-toolchain` does not
apply to it.

The CLI reads the interpolation body from stdin in normal mode and prints:

```text
L <byte-offset> <start> <end> <literal-text-hex>
E <byte-offset> <start> <end> <format-spec-hex> <expression-text-hex> <printed>
```

`--format` reads the body as an `f"..."` body (format specs are captured); the
default is an `s"..."` body.

`--batch` reads one case per line and prints a `--` separator after each:

```text
s <hex content>                    split content false
f <hex content>                    split content true
p <offset> <line> <col> <hex>      position content offset line col
```

That is the format used by the differential harness.

## Verify

```bash
./test.sh
```

This builds everything, runs the native curated tests, then differentially
compares the Lean binary against the real Go `interpolation.Split` and
`interpolation.Position` on curated cases, exhaustive short token sequences,
every raw byte string up to length 5 over a syntax-heavy alphabet, random token
sequences, random raw bytes (including invalid UTF-8), and every position of
those bytes. The default run uses 20,000 random cases per family; set
`FUZZ_CASES` and `FUZZ_SEED` to change that.

At the time of writing:

```text
interpolation tests: ok
differential: 378978 cases, 804601 part lines: MATCH
build/interpolation: ELF 64-bit LSB pie executable, x86-64, ... dynamically linked ...
```

The Go oracle is `go_ref/main.go`; it imports the production package, so the
comparison is against the code the compiler actually uses.

## Layout

| Path | Purpose |
| --- | --- |
| `Interpolation.lean` | the port |
| `InterpolationCli.lean` | native CLI / batch protocol, shared with `formal/gala-lean` |
| `Main.lean` | the standalone executable; calls `InterpolationCli.main` |
| `Tests.lean` | curated tests compiled into a native test binary |
| `go_ref/main.go` | Go differential oracle and case generator |
| `build.sh` | Lean → C → native ELF |
| `test.sh` | native tests + differential verification |
| `build/` | generated `.olean`, C, binaries, and test outputs (gitignored) |

## Scope

This is an executable port, not yet wired into the compiler build, and not a
machine-checked equivalence proof: the evidence is differential testing against
the production Go code. The production compiler still calls the Go package. A
follow-up would expose a C ABI from Lean (or call the Lean binary) and add that
to the Bazel/transpiler build; the point of this spike is that the Lean
component's behavior is already pinned against the Go implementation.
