#!/usr/bin/env bash
# Exports the GALA library in testdata/shapes with the gala binary under test
# and checks that a plain Go program can import it the way a published version
# is imported: through go mod tidy against a module proxy, with no gala on
# PATH, no network and no replace directive. The standard library comes from
# the same proxy, exported by the same binary.
#
#   GALA=/path/to/gala tools/ci/go_export/check_library_export.sh <work-dir>
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${GALA:?set GALA to the gala binary under test}"
work=${1:?usage: check_library_export.sh <work-dir>}
stdlib_module=go.gala.fyi/stdlib
stdlib_version=v0.0.0-ci.1
lib_module=example.com/shapes
lib_version=v0.1.0

# The work dir is deleted on every run, so only a directory this script
# created (it carries the marker) or an empty one is accepted.
marker=.go-export-check
if [ -d "$work" ] && [ -n "$(ls -A "$work")" ] && [ ! -e "$work/$marker" ]; then
  echo "::error::$work is not empty and was not created by this script; pass a new directory"
  exit 2
fi
rm -rf "$work"
mkdir -p "$work"
work=$(cd "$work" && pwd)
touch "$work/$marker"
export GALA_HOME="$work/gala-home"

echo "+ gala stdlib export"
"$GALA" stdlib export --go-module "$stdlib_module" --out "$work/stdlib" --proxy "$work/proxy" --version "$stdlib_version"

# The library is copied out of the repository, whose own gala.mod and Bazel
# workspace would otherwise surround it.
cp -R "$here/testdata/shapes" "$work/shapes"
echo "+ gala export (in a copy of testdata/shapes)"
(cd "$work/shapes" && "$GALA" export --out "$work/shapes-go" --stdlib-version "$stdlib_version" \
  --proxy "$work/proxy" --version "$lib_version")

if ! grep -qxF "	$stdlib_module $stdlib_version" "$work/shapes-go/go.mod"; then
  echo "::error::the exported go.mod does not require $stdlib_module $stdlib_version:"
  cat "$work/shapes-go/go.mod"
  exit 1
fi

# -modcacherw keeps the module cache deletable, so a re-run can clear <work-dir>.
export GOPROXY="file://$work/proxy" GOSUMDB=off GOFLAGS="-mod=mod -modcacherw" GOWORK=off
export GOMODCACHE="$work/modcache"
unset GOPRIVATE GONOPROXY GONOSUMDB

echo "+ plain Go consumer of $lib_module via go mod tidy"
consumer="$work/consumer"
mkdir -p "$consumer"
cp "$here/library_consumer/main.go.tmpl" "$consumer/main.go"
go_line=$(grep '^go ' "$work/shapes-go/go.mod")
printf 'module example.com/consumer\n\n%s\n' "$go_line" >"$consumer/go.mod"
(
  cd "$consumer"
  go mod tidy
  for req in "$lib_module $lib_version" "$stdlib_module $stdlib_version"; do
    if ! grep -qF "	$req" go.mod; then
      echo "::error::go mod tidy did not resolve $req:"
      cat go.mod
      exit 1
    fi
  done
  if grep -q '^replace' go.mod; then
    echo "::error::consumer go.mod needs a replace directive"
    exit 1
  fi
  go run . >"$work/consumer.out"
)
diff -u "$here/library_consumer/main.out" <(tr -d '\r' <"$work/consumer.out")
echo "ok: $lib_module@$lib_version builds and runs from a plain Go module"
