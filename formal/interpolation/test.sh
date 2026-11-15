#!/usr/bin/env bash
# Verify the Lean port against the real Go implementation.
#
#  1. build the native Lean binary (Lean -> C -> ELF)
#  2. compile and run the Lean curated tests in native code
#  3. generate curated + exhaustive + fuzz cases
#  4. compare Lean output with internal/interpolation.Split byte-for-byte
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$here/../.." && pwd)"
build_dir="${BUILD_DIR:-$here/build}"

"$here/build.sh"

if [[ -z "${LEAN:-}" ]]; then
  if command -v lean >/dev/null 2>&1; then
    LEAN="$(command -v lean)"
  elif [[ -x "$repo_root/tmp/lean-4.34.1/bin/lean" ]]; then
    LEAN="$repo_root/tmp/lean-4.34.1/bin/lean"
  else
    echo "error: lean not found; set LEAN=/path/to/lean" >&2
    exit 1
  fi
fi
if [[ -z "${LEANC:-}" ]]; then
  LEANC="$(dirname "$LEAN")/leanc"
  if [[ ! -x "$LEANC" ]]; then
    LEANC="$(command -v leanc || true)"
  fi
fi

export LEAN_PATH="$build_dir"

# Native curated tests.
"$LEAN" -R "$here" -o "$build_dir/Tests.olean" "$here/Tests.lean"
"$LEAN" -R "$here" --c="$build_dir/c/Tests.c" "$here/Tests.lean"
"$LEANC" -O2 -o "$build_dir/interpolation-tests" \
  "$build_dir/c/Tests.c" "$build_dir/c/Interpolation.c"
"$build_dir/interpolation-tests"

# Go differential oracle.
mkdir -p "$build_dir/go-cache"
(
  cd "$here"
  GOCACHE="$build_dir/go-cache" go build -o "$build_dir/go_ref" ./go_ref
)

fuzz_cases="${FUZZ_CASES:-20000}"
fuzz_seed="${FUZZ_SEED:-1}"
"$build_dir/go_ref" -gen "$fuzz_cases" -seed "$fuzz_seed" > "$build_dir/cases.txt"
"$build_dir/go_ref" -exhaustive >> "$build_dir/cases.txt"

"$build_dir/interpolation" --batch < "$build_dir/cases.txt" > "$build_dir/lean.out"
"$build_dir/go_ref" < "$build_dir/cases.txt" > "$build_dir/go.out"

if ! cmp -s "$build_dir/lean.out" "$build_dir/go.out"; then
  echo "differential mismatch; first differences:" >&2
  diff -u "$build_dir/go.out" "$build_dir/lean.out" | head -80 >&2 || true
  exit 1
fi

cases="$(wc -l < "$build_dir/cases.txt")"
lines="$(wc -l < "$build_dir/lean.out")"
echo "differential: $cases cases, $lines part lines: MATCH"
if command -v file >/dev/null 2>&1; then
  file "$build_dir/interpolation"
fi
