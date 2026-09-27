#!/usr/bin/env bash
# Runs the single-file programs in examples/ through `gala run` and compares
# their output with the checked-in expected file.
#
#   GALA=/path/to/gala tools/ci/cli_path/run_examples.sh <work-dir>
#
# Bazel already runs these as gala_exec_test targets, but against the stdlib
# sources in the repository. `gala run` transpiles against the snapshot of the
# stdlib embedded in the binary instead, and that snapshot is assembled by its
# own genrule, so a stdlib file or symbol missing from it only fails here.
#
# The set of examples is exactly the enabled single-source gala_exec_test
# targets in examples/BUILD.bazel, and the comparison is the one their test
# runner (cmd/gala_test_runner) makes: stdout and stderr together, CRLF
# folded to LF, surrounding whitespace trimmed. Targets with gala_deps, and
# sources that import a sibling package under martianoff/gala/examples/, need
# the repository's module and are skipped.
#
# Each example is copied into a directory of its own and run as an ephemeral
# module (no gala.mod), the way a user runs a one-file program; built in place
# it would pick up the repository's gala.mod instead.
#
# JOBS (default: the number of CPUs) examples run concurrently. They share one
# GALA_HOME, which is part of what is being tested: the stdlib extraction and
# the Go build cache are shared across concurrent builds for real users too.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$here/../../.." && pwd)
: "${GALA:?set GALA to the gala binary under test}"

# normalized <file> — the file as gala_test_runner compares it.
normalized() {
  local s
  s=$(tr -d '\r' <"$1")
  s="${s#"${s%%[![:space:]]*}"}"
  s="${s%"${s##*[![:space:]]}"}"
  printf '%s\n' "$s"
}

# run_one <work-dir> <source> <expected> — one example; the first line of its
# report is "ok" or "FAIL", details follow.
run_one() {
  local work=$1 src=$2 expected=$3
  local name=${src%.gala}
  local dir="$work/ex/${name//\//__}"
  rm -rf "$dir"
  mkdir -p "$dir"
  cp "$repo/examples/$src" "$dir/main.gala"
  if ! (cd "$dir" && "$GALA" run main.gala >output.txt 2>&1); then
    echo "FAIL $name (gala run exited non-zero)"
    sed 's/^/    /' "$dir/output.txt" | head -40
    return 1
  fi
  if ! diff -u <(normalized "$repo/examples/$expected") <(normalized "$dir/output.txt") >"$dir/diff.txt"; then
    echo "FAIL $name (output differs from $expected)"
    sed 's/^/    /' "$dir/diff.txt" | head -40
    return 1
  fi
  echo "ok   $name"
}

# Invoked through xargs as `run_examples.sh --one`: exported shell functions
# do not reliably survive xargs under Git Bash on Windows. Each example writes
# its report to a file of its own, because concurrent appends to one shared
# file interleave and leave gaps there.
if [ "${1:-}" = "--one" ]; then
  id=${3%.gala}
  report="$2/ex/${id//\//__}.result"
  mkdir -p "$2/ex"
  run_one "$2" "$3" "$4" >"$report" 2>&1 || true
  head -1 "$report"
  exit 0
fi

work=${1:?usage: run_examples.sh <work-dir>}
mkdir -p "$work"
work=$(cd "$work" && pwd)
# Under the repository, every example would find the repo's own gala.mod
# instead of running as a one-file program.
case "$work/" in
  "$repo/"*) echo "::error::work dir $work must be outside the repository"; exit 2 ;;
esac

# A GALA_HOME that starts empty; an inherited GALA_HOME, GALA_BUILD_DIR or
# GALA_CACHE could hand back a previously extracted stdlib or build workspace.
unset GALA_BUILD_DIR GALA_CACHE
export GALA_HOME="$work/gala-home"
rm -rf "$GALA_HOME"
jobs=${JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)}

# "<source> <expected>" for every enabled single-source gala_exec_test without
# gala_deps. Built without spawning a process per example: that alone takes
# minutes on a Windows runner.
list="$work/examples.txt"
needs_module=" $(cd "$repo/examples" && { grep -rl --include='*.gala' '"martianoff/gala/examples/' . || true; } | sed 's|^\./||' | tr '\n' ' ') "
: >"$list"
while read -r src expected; do
  case "$needs_module" in *" $src "*) continue ;; esac
  echo "$src $expected" >>"$list"
done < <(awk '
  /^gala_exec_test\(/ { inb = 1; src = ""; want = ""; deps = 0; next }
  inb && /^\)/ { if (src != "" && want != "" && !deps) print src, want; inb = 0; next }
  inb && /^[ \t]*src = "/ { split($0, q, "\""); src = q[2] }
  inb && /^[ \t]*expected = "/ { split($0, q, "\""); want = q[2] }
  inb && /gala_deps/ { deps = 1 }
' "$repo/examples/BUILD.bazel")
total=$(wc -l <"$list")
# An empty list means the BUILD.bazel parse above stopped matching, not that
# there is nothing to test.
if [ "$total" -eq 0 ]; then
  echo "::error::no gala_exec_test examples found in examples/BUILD.bazel; the parser in run_examples.sh needs updating"
  exit 1
fi
echo "Running $total examples with $jobs concurrent jobs; GALA_HOME=$GALA_HOME"

start=$(date +%s)
rm -rf "$work/ex"
# The first example runs alone, so the stdlib is extracted and compiled once
# rather than raced by every job at startup; everything after it still runs
# concurrently against the shared GALA_HOME. Progress lines stream as they
# finish; failures are reported again at the end, in list order.
head -1 "$list" | xargs -L 1 bash "${BASH_SOURCE[0]}" --one "$work"
tail -n +2 "$list" | xargs -P "$jobs" -L 1 bash "${BASH_SOURCE[0]}" --one "$work" || true

ran=0
fails=0
while read -r src _; do
  id=${src%.gala}
  report="$work/ex/${id//\//__}.result"
  [ -f "$report" ] || continue
  ran=$((ran + 1))
  if [ "$(head -c 4 "$report")" = "FAIL" ]; then
    fails=$((fails + 1))
    echo
    cat "$report"
    echo "::error::$(head -1 "$report")"
  fi
done <"$list"
echo
echo "examples: $total listed, $ran run, $fails failed, $(( $(date +%s) - start ))s"
if [ "$ran" -ne "$total" ]; then
  echo "::error::only $ran of $total examples produced a result"
  exit 1
fi
[ "$fails" -eq 0 ]
