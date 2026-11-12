#!/usr/bin/env bash
# The galaspec command-line contract the differential harness relies on, for
# the Bazel-built binary (formal.yml checks the same for the Lake-built one):
# exit code, stdout and the last stderr line on a panic, and a million nested
# calls finishing given the fuel.
# Usage: galaspec_cli_test.sh GALASPEC PANIC_DIVIDE_JSON DEEP_SUM_JSON
set -euo pipefail
bin="$1"
tmp="${TEST_TMPDIR:-$(mktemp -d)}"

set +e
"$bin" run "$2" > "$tmp/out.txt" 2> "$tmp/err.txt"
code=$?
set -e
cat "$tmp/out.txt" "$tmp/err.txt"
[[ "$code" == 1 ]] || { echo "exit code $code, want 1" >&2; exit 1; }
last="$(tail -n 1 "$tmp/err.txt" | tr -d '\r')"
[[ "$last" == "galaspec: outcome=panic:divide" ]] || { echo "last stderr line: $last" >&2; exit 1; }
[[ "$(tr -d '\r' < "$tmp/out.txt")" == "before" ]] || { echo "unexpected stdout" >&2; exit 1; }

out="$("$bin" run --fuel 100000000 "$3" | tr -d '\r')"
[[ "$out" == "500000500000" ]] || { echo "deep sum printed $out, want 500000500000" >&2; exit 1; }
echo "galaspec command line: ok"
