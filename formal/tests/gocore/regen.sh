#!/usr/bin/env bash
# Regenerates the GoCore fixtures from src/*.go: each program's GoCore JSON
# (with //internal/formal/gocoreexport/gocorejson), and, from a real `go run`,
# what it prints (<name>.out), how it ends (<name>.outcome: ok or panic) and, for a
# panic, the text Go prints after "panic: " for the panic that ended the run
# (<name>.panic).
#
# Some fixtures are about GoCore's own outcomes, which Go does not share, and are
# not regenerated from Go: fuel (Go aborts with a stack overflow, GoCore ends in
# fuel) and the unsupported_* ones, where GoCore stops: at a map
# (unsupported_map), at a capacity that depends on gc's escape analysis
# (unsupported_escape), at constraint type inference (unsupported_inference),
# at panic(nil) (unsupported_panic_nil), at a struct that embeds an interface
# (unsupported_embedded) or a type with methods, directly, through another
# embedded type or of a package GoCore does not hold (unsupported_embedded_*),
# at what fmt prints as an error (unsupported_printf_*), and at a name a runnable
# standard-library file lacks (unsupported_stdlib).
# Their files are written by hand.
#
# Run from formal/tests/gocore with GOCOREJSON naming the exporter binary, and a
# `go` that is the release MODULE.bazel pins: the expected output comes from its
# `go run`, and the standard library's runnable files (gocoreexport.RunnableStdlib)
# from its GOROOT.
set -euo pipefail
: "${GOCOREJSON:?set GOCOREJSON to the gocorejson binary}"
pinned="go$(sed -nE 's/^go_sdk\.download\(version = "([^"]+)"\)/\1/p' ../../../MODULE.bazel)"
if [[ "$(go env GOVERSION)" != "$pinned" ]]; then
  echo "regen.sh: go is $(go env GOVERSION), MODULE.bazel pins $pinned" >&2
  exit 1
fi
goroot="$(go env GOROOT)"
for f in src/*.go; do
  b="$(basename "$f" .go)"
  "$GOCOREJSON" -goroot="$goroot" "$f" > "$b.json"
  case "$b" in fuel|unsupported_*) continue ;; esac
  set +e
  go run "$f" > "$b.out" 2> "$b.goerr"
  code=$?
  set -e
  if [[ "$code" == 0 ]]; then
    echo ok > "$b.outcome"
    rm -f "$b.panic"
  else
    echo panic > "$b.outcome"
    # A panic raised while another unwinds is printed after it, tab-indented: the
    # last is the one that ended the run, as the differential harness reads it
    # (goPanicText), without the " [recovered]" a re-panic adds.
    tr -d '\r' < "$b.goerr" | sed 's/^\t//' | grep '^panic: ' | tail -n 1 |
      sed 's/^panic: //; s/ \[recovered.*//' > "$b.panic"
  fi
  rm -f "$b.goerr"
done
