#!/usr/bin/env bash
# Instruction-count profiles with Valgrind/Callgrind, which are unaffected by
# CPU frequency scaling and therefore useful when the host is noisy.
#
# usage: formal/bench/callgrind.sh [--no-build] [--cpu N]
#
# Profiles the same component calls as run.sh. Go needs async preemption
# disabled under Valgrind (Valgrind's signal handling does not support Go's
# async preemption); GODEBUG=asyncpreemptoff=1 GOMAXPROCS=1 is applied.
# Outputs and annotated call graphs are written under build/callgrind/.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
build="${BUILD_DIR:-$root/formal/bench/build}"
cpu="${BENCH_CPU:-2}"
do_build=1

while (($#)); do
  case "$1" in
    --no-build) do_build=0 ;;
    --cpu) cpu="${2:?--cpu needs a number}"; shift ;;
    -h|--help)
      sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "usage: $0 [--no-build] [--cpu N]" >&2; exit 2 ;;
  esac
  shift
done

if ((do_build)); then
  "$root/formal/bench/build.sh"
fi
if ! command -v valgrind >/dev/null 2>&1; then
  echo "error: valgrind not found; run this in 'nix develop .#gala-lean'" >&2
  exit 1
fi
for f in "$build/InterpBench" "$build/ManifestBench" "$build/gala-bench"; do
  if [[ ! -x "$f" ]]; then
    echo "error: $f is missing; run formal/bench/build.sh first" >&2
    exit 1
  fi
done

out="$build/callgrind"
mkdir -p "$out/corpus"
head -n 20000 "$build/corpus/s-cases.txt" > "$out/corpus/s-cases.txt"
head -n 10000 "$build/corpus/f-cases.txt" > "$out/corpus/f-cases.txt"
cp "$build/corpus/manifest-1000.mod" "$out/corpus/"

pin=()
if command -v taskset >/dev/null 2>&1; then
  pin=(taskset -c "$cpu")
fi

# profile <name> <command...>
profile() {
  local name="$1"
  shift
  echo "profiling $name"
  "${pin[@]}" valgrind --quiet --tool=callgrind     --callgrind-out-file="$out/$name.out" "$@" >/dev/null
  local ir
  ir="$(awk '/^summary:/{print $2; exit}' "$out/$name.out")"
  if [[ -z "$ir" ]]; then
    echo "error: valgrind produced no summary for $name" >&2
    exit 1
  fi
  echo "$ir" > "$out/$name.ir"
  echo "  $name: $ir instructions"
}

profile lean-interp-s "$build/InterpBench" s 2 "$out/corpus/s-cases.txt"
export GODEBUG=asyncpreemptoff=1 GOMAXPROCS=1
profile go-interp-s "$build/gala-bench" interp s 2 "$out/corpus/s-cases.txt"
profile lean-interp-f "$build/InterpBench" f 2 "$out/corpus/f-cases.txt"
profile go-interp-f "$build/gala-bench" interp f 2 "$out/corpus/f-cases.txt"
profile lean-manifest "$build/ManifestBench" 10 "$out/corpus/manifest-1000.mod" both
profile go-manifest "$build/gala-bench" mod 10 "$out/corpus/manifest-1000.mod" both
unset GODEBUG GOMAXPROCS

echo
echo "instruction totals (lower is better):"
for scenario in interp-s interp-f manifest; do
  lean_ir="$(cat "$out/lean-$scenario.ir")"
  go_ir="$(cat "$out/go-$scenario.ir")"
  ratio="$(awk -v a="$lean_ir" -v b="$go_ir" 'BEGIN{printf "%.2f", a/b}')"
  printf '  %-10s lean=%12s  go=%12s  lean/go=%sx\n' "$scenario" "$lean_ir" "$go_ir" "$ratio"
done

echo
echo "Lean hotspots for interp-s:"
callgrind_annotate --auto=no --inclusive=no --threshold=90 "$out/lean-interp-s.out" |
  sed -n '/PROGRAM TOTALS/,$p' | head -n 25
echo
echo "Lean hotspots for the manifest (parse + format):"
callgrind_annotate --auto=no --inclusive=no --threshold=90 "$out/lean-manifest.out" |
  sed -n '/PROGRAM TOTALS/,$p' | head -n 25
echo
echo "full callgrind data: $out/"
