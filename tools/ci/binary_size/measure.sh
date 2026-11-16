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
# Programs that are not in the baseline are measured but not checked, and
# baseline lines for programs left out of PROGRAMS are skipped.
set -euo pipefail
shopt -s inherit_errexit # a failed build inside $(build ...) must stop the script

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${GALA:?set GALA to the gala binary under test}"
work=${1:?usage: measure.sh <work-dir>}
mkdir -p "$work"
work=$(cd "$work" && pwd)
programs=${PROGRAMS:-hello httpjson collections}
margin=${MARGIN_PERCENT:-10}
case $margin in
  '' | *[!0-9]*) echo "MARGIN_PERCENT must be a whole number, got '$margin'" >&2; exit 2 ;;
esac

# GOENV=off ignores settings saved with `go env -w` (GOFLAGS, GOAMD64, ...),
# which would otherwise make the sizes depend on the machine.
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local GOENV=off
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
  rm -f "$out/gala" "$out/go"
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
    # A command substitution, not `read < <(...)`, so a failed build stops
    # the script under set -e instead of leaving empty sizes behind.
    sizes=$(build "$prog" "$flags" "$goflags")
    read -r gala_bytes go_bytes <<<"$sizes"
    printf '%s\t%s\t%s\t%s\n' "$prog" "$flags" "$gala_bytes" "$go_bytes" >>"$work/sizes.tsv"
    printf '%-12s %-8s %12s %12s %8s\n' "$prog" "$flags" "$gala_bytes" "$go_bytes" \
      "$(awk -v a="$gala_bytes" -v b="$go_bytes" 'BEGIN { printf "%.3f", a / b }')"
  done
done

[ -n "${BASELINE:-}" ] || exit 0

echo
echo "Checking GALA sizes against $BASELINE (margin ${margin}%)"
failed=0
while read -r prog flags base || [ -n "$prog" ]; do
  base=${base%$'\r'} # a CRLF checkout on Windows
  case $prog in '' | '#'*) continue ;; esac
  case $base in
    '' | *[!0-9]*) echo "::error::baseline line '$prog $flags $base' is not 'program flags bytes'"; failed=1; continue ;;
  esac
  case " $programs " in *" $prog "*) ;; *) continue ;; esac
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
    # Unless the baseline comes down after a shrink, a later regression could
    # grow back by the shrink plus the margin before this check noticed.
    if [ "$now" -lt $((base - base / 100)) ]; then
      echo "::warning::GALA $prog ($flags) shrank by more than 1%; lower its line in $BASELINE to $now"
    fi
  fi
done <"$BASELINE"
exit "$failed"
