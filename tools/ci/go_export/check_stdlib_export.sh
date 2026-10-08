#!/usr/bin/env bash
# Exports the standard library with the gala binary under test and checks that
# a plain Go program can use it the way a published version is used: through
# go mod tidy against a module proxy, with no gala on PATH, no network and no
# replace directive.
#
#   GALA=/path/to/gala tools/ci/go_export/check_stdlib_export.sh <work-dir> <module-path> <version>
#
# The export is left in <work-dir>/out and the proxy in <work-dir>/proxy, so a
# release job can publish exactly what was checked.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${GALA:?set GALA to the gala binary under test}"
work=${1:?usage: check_stdlib_export.sh <work-dir> <module-path> <version>}
module=${2:?module path required}
version=${3:?version required}

rm -rf "$work"
mkdir -p "$work"
work=$(cd "$work" && pwd)

echo "+ gala stdlib export --go-module $module --version $version"
"$GALA" stdlib export --go-module "$module" --out "$work/out" --proxy "$work/proxy" --version "$version"

# The old import path must be gone from every Go file; only the .gala sources
# kept for provenance still name it.
if leftovers=$(grep -rl --include='*.go' 'martianoff/gala' "$work/out"); then
  echo "::error::exported Go still mentions martianoff/gala:"
  echo "$leftovers"
  exit 1
fi

# Run go with a clean module environment: only the local proxy, no checksum
# database, and a private module cache so nothing leaks between runs.
export GOPROXY="file://$work/proxy" GOSUMDB=off GOFLAGS=-mod=mod GOWORK=off
export GOMODCACHE="$work/modcache"
unset GOPRIVATE GONOPROXY GONOSUMDB

# go vet is not run: the generated code has known vet findings (unreachable
# code after exhaustive matches), and a consumer's go vet ./... does not
# analyze its dependencies.
echo "+ go build in the exported module"
(cd "$work/out" && go build ./...)

echo "+ plain Go consumer via go mod tidy"
consumer="$work/consumer"
mkdir -p "$consumer"
sed "s|@MODULE@|$module|g" "$here/consumer/main.go.tmpl" >"$consumer/main.go"
printf 'module example.com/consumer\n\ngo 1.24\n' >"$consumer/go.mod"
(
  cd "$consumer"
  go mod tidy
  if ! grep -q "^require $module $version\$" go.mod; then
    echo "::error::go mod tidy did not resolve $module $version:"
    cat go.mod
    exit 1
  fi
  if grep -q '^replace' go.mod; then
    echo "::error::consumer go.mod needs a replace directive"
    exit 1
  fi
  go run . >"$work/consumer.out"
)
diff -u "$here/consumer/main.out" <(tr -d '\r' <"$work/consumer.out")
echo "ok: $module@$version builds and runs from a plain Go module"
