#!/usr/bin/env bash
# GoCore's fixtures (tests/gocore): runs `gocore run` on each <name>.json and
# checks that it prints <name>.out exactly, ends as <name>.outcome says (the
# value of its `gocore: outcome=` line), and for a panic prints <name>.panic
# after "gocore: panic: ". The expectations come from a real `go run` of
# tests/gocore/src/<name>.go (tests/gocore/regen.sh), except for the fixtures
# about GoCore's own outcomes (fuel, unsupported).
# Usage: gocore_tests.sh GOCORE FIXTURE.json...
set -euo pipefail
bin="$1"
shift
tmp="${TEST_TMPDIR:-$(mktemp -d)}"
fail=0
for json in "$@"; do
  base="${json%.json}"
  name="$(basename "$base")"
  set +e
  "$bin" run "$json" > "$tmp/out.txt" 2> "$tmp/err.txt"
  set -e
  got_outcome="$(grep '^gocore: outcome=' "$tmp/err.txt" | tail -n 1 | tr -d '\r' | sed 's/^gocore: outcome=//')"
  want_outcome="$(tr -d '\r\n' < "$base.outcome")"
  if [[ "$got_outcome" != "$want_outcome" ]]; then
    echo "$name: outcome $got_outcome, want $want_outcome"; cat "$tmp/err.txt"; fail=1; continue
  fi
  if ! diff <(tr -d '\r' < "$base.out") <(tr -d '\r' < "$tmp/out.txt") > "$tmp/diff.txt"; then
    echo "$name: stdout differs"; cat "$tmp/diff.txt"; fail=1; continue
  fi
  if [[ -f "$base.panic" ]]; then
    got_panic="$(grep '^gocore: panic: ' "$tmp/err.txt" | head -n 1 | tr -d '\r' | sed 's/^gocore: panic: //')"
    want_panic="$(tr -d '\r\n' < "$base.panic")"
    if [[ "$got_panic" != "$want_panic" ]]; then
      echo "$name: panic text '$got_panic', want '$want_panic'"; fail=1; continue
    fi
  fi
  echo "$name: ok"
done
exit "$fail"
