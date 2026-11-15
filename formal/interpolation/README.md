# Lean 4 port: GALA interpolation splitter

This is a standalone Lean 4 rewrite of `internal/interpolation`, the pure
byte-level lexer that splits the body of a GALA interpolated (`s"..."`) or
format (`f"..."`) string into literal and embedded-expression parts.

The Go original is a good pilot for a Lean component because it is explicitly
independent: it carries no parser or transpiler state, both the transformer and
the concurrency capture analysis share it, and its output is deterministic down
to byte offsets.

## What is ported

`Interpolation.lean` implements the same mini-syntax:

| Input | Result |
| --- | --- |
| `$name` | expression part `name` |
| `${ expr }` | expression part `expr` |
| `$$` | literal `$` |
| `\x` | literal escape pair (the `\` prevents syntax interpretation) |
| `\"` inside `${...}` | unescaped to `"` |
| `${x}%04d`, `$x%s` | optional Go printf spec on the expression part |
| `${` / `${a` | unterminated expression: text to end of content |

Identifiers are ASCII (`_`, `a-z`, `A-Z`) followed by `_`, letters, or digits;
`$` followed by anything else is literal. All offsets are byte offsets into the
content passed to `split`. The accepted printf flags, widths, precisions, and
verb set match the Go implementation exactly.

The port uses `ByteArray` throughout, not `String`, so non-UTF-8 bytes and
offsets are handled exactly like the Go version. Recursive scanners carry fuel,
which keeps the generated C non-dependent on termination proofs.

## Build

Requires Lean 4 and `leanc` on `PATH`, or `LEAN=/path/to/lean`. The repo's
local toolchain at `tmp/lean-4.34.1` is found automatically.

```bash
./build.sh
```

This runs Lean's C backend, then links the generated C with the Lean runtime:

```text
lean --c=build/c/Interpolation.c Interpolation.lean
lean --c=build/c/Main.c         Main.lean
leanc -O2 -o build/interpolation build/c/Main.c build/c/Interpolation.c
```

`build/interpolation` is a normal native Linux ELF executable (x86-64,
dynamically linked against the host glibc; the Lean runtime is linked in).

### Nix

From the repository root, the same executable builds without a local Lean
installation:

```bash
nix build .#gala-lean-interpolation
printf 'a $name b' | ./result/bin/interpolation
```

`nix run .#gala-lean-interpolation -- --batch < build/cases.txt` runs it without
the intermediate `result` symlink. The flake uses the Lean packaged by its
pinned nixpkgs and runs `Tests.lean` in the derivation's check phase. The
`gala-lean-` prefix marks a piece of the staged Lean port; bare `gala-lean` is
reserved for the complete Lean implementation. The derivation is a development
tool and is not part of a GALA release;
the port is not part of the Lake project, so `formal/lean-toolchain` does not
apply to it.

The CLI reads the interpolation body from stdin in normal mode and prints:

```text
L <byte-offset> <literal-text-hex>
E <byte-offset> <format-spec-hex> <expression-text-hex>
```

`--batch` reads `c <hex>` lines and prints a `--` separator after each case;
that is the format used by the differential harness.

## Verify

```bash
./test.sh
```

This builds everything, runs the native curated tests, then differentially
compares the Lean binary against the real Go `interpolation.Split` on
curated cases, exhaustive short token sequences, every raw byte string up to
length 5 over a syntax-heavy alphabet, random token sequences, and random raw
bytes (including invalid UTF-8). The default run uses 20,000 random cases per
family; set `FUZZ_CASES` and `FUZZ_SEED` to change that.

At the time of writing:

```text
interpolation tests: ok
differential: 167261 cases, 357788 part lines: MATCH
build/interpolation: ELF 64-bit LSB pie executable, x86-64, ... dynamically linked ...
```

The Go oracle is `go_ref/main.go`; it imports the production package, so the
comparison is against the code the compiler actually uses.

## Layout

| Path | Purpose |
| --- | --- |
| `Interpolation.lean` | the port |
| `Main.lean` | native CLI / batch protocol |
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
