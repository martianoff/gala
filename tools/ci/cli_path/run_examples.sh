#!/usr/bin/env bash
# Runs the single-file programs in examples/ through `gala run` and compares
# their stdout with the checked-in .out file.
#
#   GALA=/path/to/gala tools/ci/cli_path/run_examples.sh <work-dir>
#
# Bazel already runs these as gala_exec_test targets, but against the stdlib
# sources in the repository. `gala run` transpiles against the snapshot of the
# stdlib embedded in the binary instead, and that snapshot is assembled by its
# own genrule, so a stdlib file or symbol missing from it only fails here.
#
# Each example is copied into a directory of its own and run as an ephemeral
# module (no gala.mod), the way a user runs a one-file program; built in place
# it would pick up the repository's gala.mod instead. Examples that import a
# sibling package under martianoff/gala/examples/ need that module and are
# skipped, as are .gala files with no .out.
#
# JOBS (default: the number of CPUs) examples run concurrently. They share one
# GALA_HOME, which is part of what is being tested: the stdlib extraction and
# the Go build cache are shared across concurrent builds for real users too.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$here/../../.." && pwd)
: "${GALA:?set GALA to the gala binary under test}"

# run_one <work-dir> <example> — one example; prints "ok" or "FAIL" + details.
# Invoked through xargs as `run_examples.sh --one`, which works the same under
# Git Bash on Windows (where exported shell functions do not survive the trip
# through xargs).
run_one() {
  local work=$1 name=$2
  local dir="$work/ex/$name"
  rm -rf "$dir"
  mkdir -p "$dir"
  cp "$repo/examples/$name.gala" "$dir/main.gala"
  if ! (cd "$dir" && "$GALA" run main.gala >stdout.txt 2>stderr.txt); then
    echo "FAIL $name (gala run exited non-zero)"
    sed 's/^/    /' "$dir/stderr.txt" | head -40
    return 1
  fi
  if ! diff -u <(tr -d '\r' <"$repo/examples/$name.out") <(tr -d '\r' <"$dir/stdout.txt") >"$dir/diff.txt"; then
    echo "FAIL $name (stdout differs from $name.out)"
    sed 's/^/    /' "$dir/diff.txt" | head -40
    return 1
  fi
  echo "ok   $name"
}

if [ "${1:-}" = "--one" ]; then
  run_one "$2" "$3"
  exit
fi

work=${1:?usage: run_examples.sh <work-dir>}
mkdir -p "$work"
work=$(cd "$work" && pwd)

export GALA_HOME="${GALA_HOME:-$work/gala-home}"
jobs=${JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)}

# Built without spawning a process per file: that alone takes minutes on a
# Windows runner.
list="$work/examples.txt"
needs_module=" $(cd "$repo/examples" && grep -l '"martianoff/gala/examples/' -- *.gala | tr '\n' ' ') "
: >"$list"
for src in "$repo"/examples/*.gala; do
  file=${src##*/}
  name=${file%.gala}
  [ -f "$repo/examples/$name.out" ] || continue
  case "$needs_module" in *" $file "*) continue ;; esac
  echo "$name" >>"$list"
done
total=$(wc -l <"$list")
echo "Running $total examples with $jobs concurrent jobs; GALA_HOME=$GALA_HOME"

start=$(date +%s)
results="$work/results.txt"
# The first example runs alone, so the stdlib is extracted and compiled once
# rather than raced by every job at startup; everything after it still runs
# concurrently against the shared GALA_HOME.
run_one "$work" "$(head -1 "$list")" >"$results" 2>&1 || true
tail -n +2 "$list" |
  xargs -P "$jobs" -n 1 bash "${BASH_SOURCE[0]}" --one "$work" >>"$results" 2>&1 || true
cat "$results"
ran=$(grep -cE '^(ok  |FAIL )' "$results" || true)
fails=$(grep -c '^FAIL ' "$results" || true)
echo
echo "examples: $total listed, $ran run, $fails failed, $(( $(date +%s) - start ))s"
if [ "$fails" -gt 0 ] || [ "$ran" -ne "$total" ]; then
  grep '^FAIL ' "$results" | sed 's/^/::error::/' || true
  [ "$ran" -eq "$total" ] || echo "::error::only $ran of $total examples reported a result"
  exit 1
fi
