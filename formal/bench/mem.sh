#!/usr/bin/env bash
# Peak-RSS (memory) comparison of the staged Lean ports and their Go
# originals, over the same corpora and component calls as run.sh.
#
# usage: formal/bench/mem.sh [--runs N] [--reps N] [--cpu N] [--no-build]
#
# Each measurement is a fresh process. The drivers report VmHWM from procfs
# before and after the measured loop, so the table shows the startup/corpus
# load high-water mark separately from the port's additional footprint
# (`work` = peak - load). Lean and Go alternate, checksums are compared before
# anything is reported, and each cell is the median over --runs processes.
# This reads /proc/self/status, so it is Linux-only. --reps N runs N passes
# per process (default 1); N > 1 exposes retained memory between passes.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
build="${BUILD_DIR:-$here/build}"

runs=3
reps=1
cpu="${BENCH_CPU:-2}"
do_build=1

while (($#)); do
  case "$1" in
    --runs) runs="${2:?--runs needs a number}"; shift ;;
    --reps) reps="${2:?--reps needs a number}"; shift ;;
    --cpu) cpu="${2:?--cpu needs a number}"; shift ;;
    --no-build) do_build=0 ;;
    -h|--help)
      sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "usage: $0 [--runs N] [--reps N] [--cpu N] [--no-build]" >&2; exit 2 ;;
  esac
  shift
done
if ! [[ "$runs" =~ ^[1-9][0-9]*$ ]]; then
  echo "error: --runs must be a positive integer" >&2
  exit 2
fi
if ! [[ "$reps" =~ ^[1-9][0-9]*$ ]]; then
  echo "error: --reps must be a positive integer" >&2
  exit 2
fi

if [[ ! -r /proc/self/status ]]; then
  echo "error: mem.sh needs Linux procfs (/proc/self/status) for VmHWM" >&2
  exit 1
fi
if ((do_build)); then
  "$here/build.sh"
fi
for f in "$build/InterpBench" "$build/ManifestBench" "$build/gala-bench" \
    "$build/corpus/s-cases.txt" "$build/corpus/f-cases.txt" \
    "$build/corpus/manifest-40.mod" "$build/corpus/manifest-400.mod" \
    "$build/corpus/manifest-4000.mod"; do
  if [[ ! -e "$f" ]]; then
    echo "error: $f is missing; run formal/bench/build.sh first" >&2
    exit 1
  fi
done

pin=()
if command -v taskset >/dev/null 2>&1; then
  pin=(taskset -c "$cpu")
fi

out="$build/mem"
mkdir -p "$out"
summary="$out/summary.txt"
: > "$summary"

checksum() {
  awk '/^(parts=|require=)/{last=$0} END{sub(/ ns=.*/, "", last); print last}' "$1"
}

# median < lines of numbers
median() {
  sort -n | awk '{a[NR]=$1} END{
    if (NR == 0) exit 1
    if (NR % 2) print a[(NR+1)/2]
    else print (a[NR/2] + a[NR/2+1]) / 2
  }'
}

# field <file> <load_peak_kib|peak_kib>: one value per process run.
field() {
  awk -v key="$2" '
    /^mem / {
      for (i = 2; i <= NF; i++) {
        split($i, pair, "=")
        if (pair[1] == key) print pair[2]
      }
    }' "$1"
}

# nonempty <file> <key>: fail loudly when a driver did not report a reading.
nonempty() {
  local vals
  vals="$(field "$1" "$2")"
  if [[ -z "$vals" ]]; then
    echo "error: $1 has no mem $2 reading; is /proc/self/status readable?" >&2
    exit 1
  fi
  if awk '{if ($1 <= 0) bad=1} END{exit !bad}' <<< "$vals"; then
    echo "error: $1 reported mem $2=0; memory measurement unavailable" >&2
    exit 1
  fi
  if [[ "$(wc -l <<< "$vals")" -ne "$runs" ]]; then
    echo "error: $1 has $(wc -l <<< "$vals") mem $2 readings, expected $runs" >&2
    exit 1
  fi
}

stats() {
  local label="$1"
  local lf="$out/$label-lean.txt"
  local gf="$out/$label-go.txt"
  local lsum gsum
  lsum="$(checksum "$lf")"
  gsum="$(checksum "$gf")"
  if [[ "$lsum" != "$gsum" ]]; then
    echo "CHECKSUM MISMATCH for $label" >&2
    echo "  lean: $lsum" >&2
    echo "  go:   $gsum" >&2
    exit 1
  fi
  nonempty "$lf" load_peak_kib
  nonempty "$lf" peak_kib
  nonempty "$gf" load_peak_kib
  nonempty "$gf" peak_kib

  local lload lpeak gload gpeak lwork gwork
  # VmHWM is whole KiB per process; the median of an even number of runs can
  # land on .5, so round it back to a whole KiB.
  lload="$(field "$lf" load_peak_kib | median | awk '{printf "%.0f", $1}')"
  lpeak="$(field "$lf" peak_kib | median | awk '{printf "%.0f", $1}')"
  gload="$(field "$gf" load_peak_kib | median | awk '{printf "%.0f", $1}')"
  gpeak="$(field "$gf" peak_kib | median | awk '{printf "%.0f", $1}')"
  lwork="$(awk -v p="$lpeak" -v l="$lload" 'BEGIN{print p - l}')"
  gwork="$(awk -v p="$gpeak" -v l="$gload" 'BEGIN{print p - l}')"
  if awk -v l="$lload" -v p="$lpeak" 'BEGIN{exit !(p < l)}' ||
     awk -v l="$gload" -v p="$gpeak" 'BEGIN{exit !(p < l)}'; then
    echo "error: peak is below load for $label" >&2
    exit 1
  fi

  local peak_ratio
  peak_ratio="$(awk -v a="$lpeak" -v b="$gpeak" 'BEGIN{printf "%.2f", a/b}')x"

  printf '%-14s lean %7d/%7d/%6d KiB  go %7d/%7d/%6d KiB  peak %6s\n' \
    "${label//-/ }" "$lload" "$lpeak" "$lwork" "$gload" "$gpeak" "$gwork" "$peak_ratio" |
    tee -a "$summary"
}

# mem_pair <label> <lean command...> -- <go command...>
# The two command tails are split at the literal `--`.
mem_pair() {
  local label="$1"
  shift
  local lean_cmd=() go_cmd=()
  while [[ "$1" != "--" ]]; do lean_cmd+=("$1"); shift; done
  shift
  go_cmd=("$@")
  local lf="$out/$label-lean.txt" gf="$out/$label-go.txt"
  : > "$lf"; : > "$gf"
  local r
  for ((r = 1; r <= runs; r++)); do
    if ((r % 2)); then
      "${pin[@]}" "${lean_cmd[@]}" >> "$lf"
      "${pin[@]}" "${go_cmd[@]}" >> "$gf"
    else
      "${pin[@]}" "${go_cmd[@]}" >> "$gf"
      "${pin[@]}" "${lean_cmd[@]}" >> "$lf"
    fi
  done
}

echo "memory: peak RSS (VmHWM), $runs process runs per side, $reps pass(es) per run"
echo "startup is an empty-corpus run: process and runtime baseline"
if ((${#pin[@]})); then echo "pinned: CPU $cpu"; fi
echo

mem_pair startup \
  "$build/InterpBench" s "$reps" /dev/null -- \
  "$build/gala-bench" interp s "$reps" /dev/null
for mode in s f; do
  mem_pair "interp-$mode" \
    "$build/InterpBench" "$mode" "$reps" "$build/corpus/$mode-cases.txt" -- \
    "$build/gala-bench" interp "$mode" "$reps" "$build/corpus/$mode-cases.txt"
done
for n in 40 400 4000; do
  mem_pair "mod-$n" \
    "$build/ManifestBench" "$reps" "$build/corpus/manifest-$n.mod" both -- \
    "$build/gala-bench" mod "$reps" "$build/corpus/manifest-$n.mod" both
done

echo "scenario       lean load/peak/work KiB    go load/peak/work KiB    peak lean/go"
stats startup
stats interp-s
stats interp-f
stats mod-40
stats mod-400
stats mod-4000
echo
echo "load: high-water mark after startup and corpus load; peak: after the loop;"
echo "work: peak - load. Raw driver output: $out/"
