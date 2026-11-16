#!/usr/bin/env bash
# The galaspec command-line contract the differential harness relies on, for
# the Bazel-built binary (formal.yml checks the same for the Lake-built one):
# exit code, stdout and the last stderr line on a panic, and a million nested
# calls finishing given the fuel; and `galaspec alpha`'s verdicts and exit codes.
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

# `galaspec alpha A B`: related (0), unrelated (1), a bad command line (4).
alpha() {
  local want_out="$1" want_code="$2"; shift 2
  set +e
  got="$("$bin" alpha "$@" 2> "$tmp/alpha_err.txt" | tr -d '\r')"
  code=${PIPESTATUS[0]}
  set -e
  [[ "$got" == "$want_out" && "$code" == "$want_code" ]] ||
    { echo "galaspec alpha $*: printed '$got', exit $code; want '$want_out', exit $want_code" >&2; exit 1; }
}
alpha "galaspec: alpha=related" 0 "$2" "$2"
alpha "galaspec: alpha=unrelated" 1 "$2" "$3"
alpha "galaspec: alpha=malformed" 4 "$2"
echo "galaspec command line: ok"
