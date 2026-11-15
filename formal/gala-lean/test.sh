#!/usr/bin/env bash
# End-to-end check of the combined `gala-lean` executable.
#
# It builds the Lake package, then runs every component through the one
# command line: the reference interpreter (`run`), the interpolation splitter
# (`interp`), the diagnostic renderers (`diag`) and the gala.mod reader
# (`mod`). The components' own ports have their differential tests against
# the Go code; this script checks the combination.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$here/../.." && pwd)"

if [[ -z "${LAKE:-}" ]]; then
  if command -v lake >/dev/null 2>&1; then
    LAKE="$(command -v lake)"
  else
    echo "error: lake not found; run this in 'nix develop .#gala-lean'" >&2
    echo "       from the repository root, or set LAKE=/path/to/lake" >&2
    exit 1
  fi
fi

cd "$here"
"$LAKE" build gala-lean

bin="$here/.lake/build/bin/gala-lean"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# `version` and `help` are part of the one command line.
"$bin" version | grep -q '^gala-lean '
"$bin" help | grep -q '^usage: gala-lean '

# `run` is galaspec: output on stdout, outcome on stderr, diagnostics' exit
# codes unchanged. Two fixtures give arithmetic and printing coverage.
"$bin" run ../tests/ex_hex_literals.json > "$tmp/run.out"
diff -u ../tests/ex_hex_literals.out "$tmp/run.out"
"$bin" run --fuel 1000000 ../tests/arith.json > "$tmp/arith.out"
diff -u ../tests/arith.out "$tmp/arith.out"
"$bin" run ../tests/panic_divide.json > "$tmp/panic.out" 2>"$tmp/panic.err" || true
grep -q 'outcome=panic:divide' "$tmp/panic.err"

# A lone .gala argument behaves like `gala prog.gala`: it explains that this
# draft needs the Core JSON export instead of pretending to parse the source.
if "$bin" ../../examples/hello.gala 2>"$tmp/gala.err"; then
  echo "gala-lean: a .gala argument unexpectedly succeeded" >&2
  exit 1
fi
grep -q 'cannot read GALA source yet' "$tmp/gala.err"

# `interp` is the `s"..."`/`f"..."` splitter.
printf '%s' 'a $name b' | "$bin" interp > "$tmp/interp.out"
diff -u <(printf '%s\n' 'L 0 0 2 6120' 'E 3 2 7  6e616d65 1' 'L 7 7 9 2062') "$tmp/interp.out"

# `diag` is the terminal and JSON renderer pair.
printf '%s\n' \
  'r|0|6e6f2d737563682d66696c652d376133662e67616c61|7061636b616765206d61696e0a||d:47414c412d4530303031:626164:646f20626574746572::1:0:0' \
  | "$bin" diag --batch > "$tmp/diag.out"
grep -q '^R ' "$tmp/diag.out"
grep -q '^J ' "$tmp/diag.out"

# `mod init`/`show`/`format` use the gala.mod port and the diagnostics
# renderer for parse errors.
(
  cd "$tmp"
  "$bin" mod init example.com/draft > init.out
  grep -q '^module example.com/draft$' gala.mod
  grep -q '^gala 0.101.0$' gala.mod
  "$bin" mod show > show.out
  diff -u <(printf '%s\n' 'module example.com/draft' 'gala 0.101.0') show.out
  "$bin" mod format > format.out
  diff -u gala.mod format.out
  if "$bin" mod init example.com/other; then
    echo "gala-lean: second mod init unexpectedly succeeded" >&2
    exit 1
  fi
)

echo "gala-lean tests: ok"
