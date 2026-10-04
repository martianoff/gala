#!/usr/bin/env bash
# Packs a Nix-transpiled stdlib with cmd/stdlib_gen and diffs the result
# against Bazel's generate_embedded output, byte for byte.
#
# Usage: tools/ci/compare_stdlib.sh <nix-output-dir> <label>
#   <nix-output-dir>  a Nix result holding the staged inputs under files/
#   <label>           names the Nix side in messages ("Nix", "local-bootstrap")
#
# Needs `bazel build //internal/stdlib:generate_embedded //cmd/stdlib_gen`
# first. Shared by stdlib-parity.yml and local-bootstrap.yml.
set -euo pipefail

nix_out=$1
label=$2
tmp=${RUNNER_TEMP:-$(mktemp -d)}
bazel_gen=bazel-bin/internal/stdlib/embedded_gen.go

# stdlib_gen groups its inputs into maps keyed by package and sorts both
# levels before writing, so the order of the file list cannot change the
# output. `sort` is still applied so the command line in a log is reproducible.
mapfile -t files < <(find "$nix_out/files" -type f | sort)
echo "comparing ${#files[@]} $label inputs against Bazel's generate_embedded srcs"

./bazel-bin/cmd/stdlib_gen/stdlib_gen_/stdlib_gen -output "$tmp/nix_embedded_gen.go" "${files[@]}"

if diff -u "$tmp/nix_embedded_gen.go" "$bazel_gen" > "$tmp/parity.diff"; then
  echo "stdlib parity holds: $label and Bazel produced identical embedded_gen.go"
  exit 0
fi

echo "::error::$label and Bazel disagree on internal/stdlib/embedded_gen.go"
echo "diff lines: $(wc -l < "$tmp/parity.diff")"
# Entry counts on both sides. A mismatch means an input was dropped on one
# side only -- Bazel's genrule takes every src, the Nix awk scrape keeps only
# labels containing a colon -- so this separates "an input went missing" from
# "an input was transpiled differently" without reading the diff. Equal counts
# with a non-empty diff is a codegen divergence.
echo "packed entries: $label=$(grep -c $'^\t\t"' "$tmp/nix_embedded_gen.go")" \
     "bazel=$(grep -c $'^\t\t"' "$bazel_gen")"
# A whole added or removed package block is the signature of a dropped input;
# a changed line inside a package is the signature of a codegen divergence.
# Print the first few hunks either way, because a generated file diffs into
# thousands of lines otherwise.
head -60 "$tmp/parity.diff"
exit 1
