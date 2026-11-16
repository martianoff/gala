#!/usr/bin/env bash
# Build the Lean ports and the Go/Lean benchmark drivers used by formal/bench.
#
# Environment:
#   LEAN, LEANC   Lean 4 compiler and C toolchain (otherwise found on PATH)
#   LEANC_FLAGS   C flags for the generated code (default: -O3 -flto -DNDEBUG)
#   BUILD_DIR     output directory (default: formal/bench/build)
#
# The build is deliberately plain Lean + leanc, like the port build scripts:
# the benchmark does not need Lake or Bazel. It does need a writable GOCACHE
# for the Go driver (Go selects one automatically).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
build="${BUILD_DIR:-$here/build}"

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
fi

if [[ -z "${LEAN:-}" ]]; then
  LEAN="$(command -v lean || true)"
fi
if [[ -z "${LEAN:-}" ]]; then
  echo "error: Lean not found; run this in 'nix develop .#gala-lean'" >&2
  echo "       from the repository root, or set LEAN=/path/to/lean" >&2
  exit 1
fi
if [[ -z "${LEANC:-}" ]]; then
  if [[ -x "$(dirname "$LEAN")/leanc" ]]; then
    LEANC="$(dirname "$LEAN")/leanc"
  else
    LEANC="$(command -v leanc || true)"
  fi
fi
if [[ ! -x "$LEANC" ]]; then
  echo "error: leanc not found; set LEANC=/path/to/leanc" >&2
  exit 1
fi
export LEAN LEANC

# Keep the ports and the drivers on the same C flags. -DNDEBUG removes Lean's
# runtime assertions from the generated C; the benchmark's final numbers use
# this default too, so it is what build.sh, run.sh and hyperfine.sh report.
leanc_flags=(-O3 -flto -DNDEBUG)
if [[ -n "${LEANC_FLAGS:-}" ]]; then
  read -r -a leanc_flags <<<"$LEANC_FLAGS"
fi
export LEANC_FLAGS="${leanc_flags[*]}"

if ! command -v go >/dev/null 2>&1; then
  echo "error: go not found; run this in 'nix develop .#gala-lean'" >&2
  exit 1
fi

mkdir -p "$build"

echo "building Lean ports"
BUILD_DIR="$build/interpolation" "$repo/formal/interpolation/build.sh" >/dev/null
BUILD_DIR="$build/manifest" "$repo/formal/manifest/build.sh" >/dev/null

echo "building Lean benchmark drivers"
LEAN_PATH="$build/interpolation" "$LEAN" -R "$repo" \
  --c="$build/InterpBench.c" "$here/InterpBench.lean"
"$LEANC" "${leanc_flags[@]}" -o "$build/InterpBench" \
  "$build/InterpBench.c" "$build/interpolation/c/Interpolation.c"

LEAN_PATH="$build/manifest" "$LEAN" -R "$repo" \
  --c="$build/ManifestBench.c" "$here/ManifestBench.lean"
"$LEANC" "${leanc_flags[@]}" -o "$build/ManifestBench" \
  "$build/ManifestBench.c" "$build/manifest/c/Manifest.c"

echo "building Go benchmark driver"
(
  cd "$repo"
  go build -o "$build/gala-bench" ./formal/bench/driver
)

echo "generating corpora"
"$build/gala-bench" gen "$build/corpus"

echo "built:"
echo "  $build/InterpBench"
echo "  $build/ManifestBench"
echo "  $build/gala-bench"
echo "  $build/corpus/"
