#!/usr/bin/env bash
# Wall-clock A/B benchmark with hyperfine, including one-time process start and
# corpus loading (unlike run.sh, which times the component loops in process).
#
# usage: formal/bench/hyperfine.sh [--runs N] [--cpu N] [--no-build]
#
# Each scenario is measured twice, once with Lean first and once with Go
# first, because hyperfine runs each command's repetitions as a block and the
# host's frequency governor drifts between blocks. Compare the two runs. JSON
# and Markdown results are written under build/hyperfine/.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
build="${BUILD_DIR:-$root/formal/bench/build}"
reps_runs="${HF_RUNS:-5}"
cpu="${BENCH_CPU:-2}"
do_build=1

while (($#)); do
  case "$1" in
    --runs) reps_runs="${2:?--runs needs a number}"; shift ;;
    --cpu) cpu="${2:?--cpu needs a number}"; shift ;;
    --no-build) do_build=0 ;;
    -h|--help)
      sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "usage: $0 [--runs N] [--cpu N] [--no-build]" >&2; exit 2 ;;
  esac
  shift
done

if ((do_build)); then
  "$root/formal/bench/build.sh"
fi
if ! command -v hyperfine >/dev/null 2>&1; then
  echo "error: hyperfine not found; run this in 'nix develop .#gala-lean'" >&2
  exit 1
fi
for f in "$build/InterpBench" "$build/ManifestBench" "$build/gala-bench"; do
  if [[ ! -x "$f" ]]; then
    echo "error: $f is missing; run formal/bench/build.sh first" >&2
    exit 1
  fi
done

out="$build/hyperfine"
mkdir -p "$out/corpus"
# Smaller corpora keep each hyperfine iteration short; the full set is used by
# run.sh. The prefixes are deterministic and cover every generated case shape.
head -n 20000 "$build/corpus/s-cases.txt" > "$out/corpus/s-cases.txt"
head -n 50000 "$build/corpus/f-cases.txt" > "$out/corpus/f-cases.txt"
cp "$build/corpus/manifest-4000.mod" "$out/corpus/"
cp "$build/corpus/manifest-400.mod" "$out/corpus/"

pin=""
if command -v taskset >/dev/null 2>&1; then
  pin="taskset -c $cpu "
fi

# hf <scenario> <lean-command> <go-command>
hf() {
  local scenario="$1" lean_cmd="$2" go_cmd="$3"
  echo
  echo "===== $scenario: Lean first ====="
  hyperfine --warmup 1 --runs "$reps_runs" --command-name lean --command-name go \
    --export-json "$out/$scenario.lean-first.json" \
    --export-markdown "$out/$scenario.lean-first.md" \
    "$pin$lean_cmd" "$pin$go_cmd"
  echo
  echo "===== $scenario: Go first ====="
  hyperfine --warmup 1 --runs "$reps_runs" --command-name go --command-name lean \
    --export-json "$out/$scenario.go-first.json" \
    --export-markdown "$out/$scenario.go-first.md" \
    "$pin$go_cmd" "$pin$lean_cmd"
}

hf interp-s \
  "\"$build/InterpBench\" s 20 \"$out/corpus/s-cases.txt\"" \
  "\"$build/gala-bench\" interp s 20 \"$out/corpus/s-cases.txt\""
hf interp-f \
  "\"$build/InterpBench\" f 20 \"$out/corpus/f-cases.txt\"" \
  "\"$build/gala-bench\" interp f 20 \"$out/corpus/f-cases.txt\""
hf mod-4000 \
  "\"$build/ManifestBench\" 30 \"$out/corpus/manifest-4000.mod\" both" \
  "\"$build/gala-bench\" mod 30 \"$out/corpus/manifest-4000.mod\" both"
hf mod-400 \
  "\"$build/ManifestBench\" 300 \"$out/corpus/manifest-400.mod\" both" \
  "\"$build/gala-bench\" mod 300 \"$out/corpus/manifest-400.mod\" both"

echo
echo "hyperfine results: $out/"
