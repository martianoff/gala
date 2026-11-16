#!/usr/bin/env bash
# Regenerates the GoCore fixtures from src/*.go: each program's GoCore JSON
# (with //internal/formal/gocoreexport/gocorejson), and, from a real `go run`,
# what it prints (<name>.out), how it ends (<name>.outcome: ok or panic) and, for a
# panic, the text Go prints after "panic: " (<name>.panic).
#
# Two fixtures are about GoCore's own outcomes, which Go does not share, and are
# not regenerated from Go: fuel (Go aborts with a stack overflow, GoCore ends in
# fuel) and unsupported_map (GoCore stops at the map). Their files are written by
# hand.
#
# Run from formal/tests/gocore with GOCOREJSON naming the exporter binary.
set -euo pipefail
: "${GOCOREJSON:?set GOCOREJSON to the gocorejson binary}"
for f in src/*.go; do
  b="$(basename "$f" .go)"
  "$GOCOREJSON" "$f" > "$b.json"
  case "$b" in fuel|unsupported_map) continue ;; esac
  set +e
  go run "$f" > "$b.out" 2> "$b.goerr"
  code=$?
  set -e
  if [[ "$code" == 0 ]]; then
    echo ok > "$b.outcome"
    rm -f "$b.panic"
  else
    echo panic > "$b.outcome"
    grep -m1 '^panic: ' "$b.goerr" | sed 's/^panic: //' | tr -d '\r' > "$b.panic"
  fi
  rm -f "$b.goerr"
done
