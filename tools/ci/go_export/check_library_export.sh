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
. "$here/lib.sh"
: "${GALA:?set GALA to the gala binary under test}"
prepare_workdir "${1:?usage: check_library_export.sh <work-dir>}"
stdlib_module=go.gala.fyi/stdlib
stdlib_version=v0.0.0-ci.1
lib_module=example.com/shapes
lib_version=v0.1.0
export GALA_HOME="$work/gala-home"

echo "+ gala stdlib export"
"$GALA" stdlib export --go-module "$stdlib_module" --out "$work/stdlib" --proxy "$work/proxy" --version "$stdlib_version"

# The library is copied out of the repository, whose own gala.mod and Bazel
# workspace would otherwise surround it.
cp -R "$here/testdata/shapes" "$work/shapes"
echo "+ gala export (in a copy of testdata/shapes)"
(cd "$work/shapes" && "$GALA" export --out "$work/shapes-go" --stdlib-version "$stdlib_version" \
  --proxy "$work/proxy" --version "$lib_version")
use_proxy

echo "+ go build in the exported module"
(cd "$work/shapes-go" && go build ./...)

echo "+ plain Go consumer of $lib_module via go mod tidy"
check_consumer "$here/library_consumer/main.go.tmpl" "$here/library_consumer/main.out" \
  "$(go_version_of "$work/shapes-go/go.mod")" "$lib_module@$lib_version" "$stdlib_module@$stdlib_version"
echo "ok: $lib_module@$lib_version builds and runs from a plain Go module"
