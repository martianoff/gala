#!/usr/bin/env bash
# Builds each program under testdata/ twice, once from its GALA source with
# `gala build` and once from the equivalent plain Go with `go build`, and
# reports the size of every binary.
#
#   GALA=/path/to/gala tools/ci/binary_size/measure.sh <work-dir>
#   GALA=... BASELINE=tools/ci/binary_size/baseline.tsv tools/ci/binary_size/measure.sh <work-dir>
#
# Every binary is linux/amd64 with cgo off, whatever the host, so the numbers
# do not depend on the machine they were measured on. Both sides build with
# the same `go` on PATH and the same flags: `gala build` always passes
# -trimpath, so the Go side passes it too. Each program is built twice per
# side, with the default flags and with -ldflags="-s -w" (no symbol table, no
# DWARF), the flags most people ship with.
#
# The table goes to stdout and, tab-separated, to <work-dir>/sizes.tsv:
#   program  flags  gala_bytes  go_bytes
#
# With BASELINE set, the GALA bytes are compared against that file (lines of
# "program flags bytes"; # starts a comment) and the script fails when any of
# them is more than MARGIN_PERCENT (default 10) larger than its baseline.
# Programs that are not in the baseline are measured but not checked.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${GALA:?set GALA to the gala binary under test}"
work=${1:?usage: measure.sh <work-dir>}
mkdir -p "$work"
work=$(cd "$work" && pwd)
programs=${PROGRAMS:-hello httpjson collections}
margin=${MARGIN_PERCENT:-10}

export GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local
# A private GALA_HOME keeps the stdlib snapshot and build workspaces of the
# binary under test apart from any other gala on the machine.
export GALA_HOME="$work/gala-home"
unset GALA_BUILD_DIR GALA_CACHE GOFLAGS

echo "$("$GALA" version | head -1); $(go version)"

# Copied out of the repository so `gala build` sees only each program's own
# gala.mod, the way it would in a user's project.
rm -rf "$work/src"
cp -R "$here/testdata" "$work/src"

# build <program> <flags-name> <goflags> — prints "<gala-bytes> <go-bytes>".
build() {
  local prog=$1 flags=$2 goflags=$3
  local src="$work/src/$prog" out="$work/bin/$prog-$flags"
  mkdir -p "$out"
  (cd "$src/gala" && GOFLAGS=$goflags "$GALA" build -o "$out/gala" . >/dev/null)
  (cd "$src/go" && GOFLAGS=$goflags go build -trimpath -o "$out/go" .)
  echo "$(wc -c <"$out/gala") $(wc -c <"$out/go")"
}

: >"$work/sizes.tsv"
printf '%-12s %-8s %12s %12s %8s\n' program flags gala_bytes go_bytes gala/go
for prog in $programs; do
  for flags in default stripped; do
    case $flags in
      default) goflags="" ;;
      stripped) goflags="'-ldflags=-s -w'" ;;
    esac
    read -r gala_bytes go_bytes < <(build "$prog" "$flags" "$goflags")
    printf '%s\t%s\t%s\t%s\n' "$prog" "$flags" "$gala_bytes" "$go_bytes" >>"$work/sizes.tsv"
    printf '%-12s %-8s %12s %12s %8s\n' "$prog" "$flags" "$gala_bytes" "$go_bytes" \
      "$(awk -v a="$gala_bytes" -v b="$go_bytes" 'BEGIN { printf "%.3f", a / b }')"
  done
done

[ -n "${BASELINE:-}" ] || exit 0

echo
echo "Checking GALA sizes against $BASELINE (margin ${margin}%)"
failed=0
while read -r prog flags base; do
  case $prog in '' | '#'*) continue ;; esac
  now=$(awk -v p="$prog" -v f="$flags" '$1 == p && $2 == f { print $3 }' "$work/sizes.tsv")
  if [ -z "$now" ]; then
    echo "::error::$prog/$flags is in the baseline but was not measured"
    failed=1
    continue
  fi
  limit=$((base + base * margin / 100))
  delta=$(awk -v a="$now" -v b="$base" 'BEGIN { printf "%+.1f%%", (a - b) * 100 / b }')
  if [ "$now" -gt "$limit" ]; then
    echo "::error::GALA $prog ($flags) is $now bytes, $delta against the baseline $base (limit $limit)"
    failed=1
  else
    echo "ok   $prog ($flags): $now bytes, $delta against the baseline $base (limit $limit)"
  fi
done <"$BASELINE"
exit "$failed"
