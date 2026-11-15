#!/usr/bin/env bash
# Compile the Lean 4 gala.mod port to C, then link a native Linux binary.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$here/../.." && pwd)"
build_dir="${BUILD_DIR:-$here/build}"

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

if [[ ! -x "$LEAN" ]]; then
  echo "error: LEAN is not executable: $LEAN" >&2
  exit 1
fi
if [[ ! -x "$LEANC" ]]; then
  echo "error: leanc not found; set LEANC=/path/to/leanc" >&2
  exit 1
fi

mkdir -p "$build_dir/c"

# The .olean files live in build_dir, so imports resolve from there.
export LEAN_PATH="$build_dir"

"$LEAN" -R "$here" -o "$build_dir/Manifest.olean" "$here/Manifest.lean"
"$LEAN" -R "$here" -o "$build_dir/ManifestCli.olean" "$here/ManifestCli.lean"
"$LEAN" -R "$here" -o "$build_dir/Main.olean" "$here/Main.lean"

# Lean 4's C backend: one C translation unit per module.
"$LEAN" -R "$here" --c="$build_dir/c/Manifest.c" "$here/Manifest.lean"
"$LEAN" -R "$here" --c="$build_dir/c/ManifestCli.c" "$here/ManifestCli.lean"
"$LEAN" -R "$here" --c="$build_dir/c/Main.c" "$here/Main.lean"

# leanc links the generated C against the Lean runtime using the host C
# toolchain, producing a normal native executable.
"$LEANC" -O2 -o "$build_dir/manifest" \
  "$build_dir/c/Main.c" "$build_dir/c/ManifestCli.c" "$build_dir/c/Manifest.c"

echo "built $build_dir/manifest"
echo "generated C: $build_dir/c/Manifest.c $build_dir/c/ManifestCli.c $build_dir/c/Main.c"
