#!/usr/bin/env bash
# Steady-state throughput benchmark: the staged Lean ports vs their Go
# counterparts, timed inside one process per repetition so that process start
# and corpus loading are not counted.
#
# usage: formal/bench/run.sh [--quick|--full] [--runs N] [--cpu N] [--no-build]
#
# One iteration per repetition is discarded as warm-up. The reported ratio is
# median(Lean) / median(Go); below 1.0 means Lean is faster. Check that the two
# drivers printed the same checksum is done before any ratio is reported.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
build="${BUILD_DIR:-$here/build}"

mode=quick
runs=3
cpu="${BENCH_CPU:-2}"
do_build=1

while (($#)); do
  case "$1" in
    --quick) mode=quick ;;
    --full) mode=full ;;
    --runs) runs="${2:?--runs needs a number}"; shift ;;
    --cpu) cpu="${2:?--cpu needs a number}"; shift ;;
    --no-build) do_build=0 ;;
    --build-only) "$here/build.sh"; exit 0 ;;
    -h|--help)
      sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "usage: $0 [--quick|--full] [--runs N] [--cpu N] [--no-build]" >&2; exit 2 ;;
  esac
  shift
done

if ((do_build)); then
  "$here/build.sh"
fi
for f in "$build/InterpBench" "$build/ManifestBench" "$build/gala-bench" \
    "$build/corpus/s-cases.txt" "$build/corpus/f-cases.txt" "$build/corpus/manifest-4000.mod"; do
  if [[ ! -e "$f" ]]; then
    echo "error: $f is missing; run formal/bench/build.sh first" >&2
    exit 1
  fi
done

if [[ "$mode" == "quick" ]]; then
  s_reps=3; f_reps=5; mod40_reps=80; mod400_reps=12; mod4000_reps=3
else
  s_reps=6; f_reps=10; mod40_reps=200; mod400_reps=30; mod4000_reps=3
fi

pin=()
if command -v taskset >/dev/null 2>&1; then
  pin=(taskset -c "$cpu")
fi

mkdir -p "$build/results"
summary="$build/results/summary.txt"
: > "$summary"

checksum() {
  awk '/^(parts=|require=)/{last=$0} END{sub(/ ns=.*/, "", last); print last}' "$1"
}

iterations() {
  awk -v reps="$2" -F'ns=' '/^iter=/{i++; if ((i-1)%reps != 0) print $2}' "$1"
}

median() {
  sort -n | awk '{a[NR]=$1} END{
    if (NR == 0) exit 1
    if (NR % 2) print a[(NR+1)/2]
    else print (a[NR/2] + a[NR/2+1]) / 2
  }'
}

minv() { sort -n | awk 'NR == 1 { first = $1 } END { print first }'; }

ms() { awk -v ns="$1" 'BEGIN{printf "%.3f", ns/1e6}'; }

stats() {
  local reps="$1" lf="$2" gf="$3" label="$4"
  local lsum gsum
  lsum="$(checksum "$lf")"
  gsum="$(checksum "$gf")"
  if [[ "$lsum" != "$gsum" ]]; then
    echo "CHECKSUM MISMATCH for $label" >&2
    echo "  lean: $lsum" >&2
    echo "  go:   $gsum" >&2
    exit 1
  fi
  local lvals gvals lmed gmed lmin gmin
  lvals="$(iterations "$lf" "$reps")"
  gvals="$(iterations "$gf" "$reps")"
  if [[ -z "$lvals" || -z "$gvals" ]]; then
    echo "error: no steady-state iterations for $label" >&2
    exit 1
  fi
  lmed="$(median <<< "$lvals")"
  gmed="$(median <<< "$gvals")"
  lmin="$(minv <<< "$lvals")"
  gmin="$(minv <<< "$gvals")"
  local line
  line="$(printf '%-14s go=%9s ms lean=%9s ms ratio=%5sx min_ratio=%5sx (n=%d)' \
    "$label" "$(ms "$gmed")" "$(ms "$lmed")" \
    "$(awk -v a="$lmed" -v b="$gmed" 'BEGIN{printf "%.2f", a/b}')" \
    "$(awk -v a="$lmin" -v b="$gmin" 'BEGIN{printf "%.2f", a/b}')" \
    "$(wc -l <<< "$lvals")")"
  echo "$line" | tee -a "$summary"
}

run_interp() {
  local m="$1" reps="$2"
  local lf="$build/results/interp-$m-lean.txt"
  local gf="$build/results/interp-$m-go.txt"
  : > "$lf"; : > "$gf"
  for ((r = 1; r <= runs; r++)); do
    if ((r % 2)); then
      "${pin[@]}" "$build/InterpBench" "$m" "$reps" "$build/corpus/$m-cases.txt" >> "$lf"
      "${pin[@]}" "$build/gala-bench" interp "$m" "$reps" "$build/corpus/$m-cases.txt" >> "$gf"
    else
      "${pin[@]}" "$build/gala-bench" interp "$m" "$reps" "$build/corpus/$m-cases.txt" >> "$gf"
      "${pin[@]}" "$build/InterpBench" "$m" "$reps" "$build/corpus/$m-cases.txt" >> "$lf"
    fi
  done
}

run_mod() {
  local n="$1" reps="$2"
  local file="$build/corpus/manifest-$n.mod"
  local lf="$build/results/mod-$n-lean.txt"
  local gf="$build/results/mod-$n-go.txt"
  : > "$lf"; : > "$gf"
  for ((r = 1; r <= runs; r++)); do
    if ((r % 2)); then
      "${pin[@]}" "$build/ManifestBench" "$reps" "$file" both >> "$lf"
      "${pin[@]}" "$build/gala-bench" mod "$reps" "$file" both >> "$gf"
    else
      "${pin[@]}" "$build/gala-bench" mod "$reps" "$file" both >> "$gf"
      "${pin[@]}" "$build/ManifestBench" "$reps" "$file" both >> "$lf"
    fi
  done
}

lean_bin="${LEAN:-$(command -v lean || true)}"
echo "lean: $("$lean_bin" --version 2>/dev/null | head -n 1)"
echo "go:   $(go version)"
if ((${#pin[@]})); then echo "pinned: CPU $cpu"; fi
echo "mode: $mode, $runs runs; one warm-up iteration per run is dropped"
echo

run_interp s "$s_reps"
run_interp f "$f_reps"
run_mod 40 "$mod40_reps"
run_mod 400 "$mod400_reps"
run_mod 4000 "$mod4000_reps"

stats "$s_reps" "$build/results/interp-s-lean.txt" "$build/results/interp-s-go.txt" "interp s"
stats "$f_reps" "$build/results/interp-f-lean.txt" "$build/results/interp-f-go.txt" "interp f"
stats "$mod40_reps" "$build/results/mod-40-lean.txt" "$build/results/mod-40-go.txt" "mod 40"
stats "$mod400_reps" "$build/results/mod-400-lean.txt" "$build/results/mod-400-go.txt" "mod 400"
stats "$mod4000_reps" "$build/results/mod-4000-lean.txt" "$build/results/mod-4000-go.txt" "mod 4000"
echo
echo "raw iteration timings: $build/results/"
