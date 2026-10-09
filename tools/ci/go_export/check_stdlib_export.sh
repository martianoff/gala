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
. "$here/lib.sh"
: "${GALA:?set GALA to the gala binary under test}"
prepare_workdir "${1:?usage: check_stdlib_export.sh <work-dir> <module-path> <version>}"
module=${2:?module path required}
version=${3:?version required}

echo "+ gala stdlib export --go-module $module --version $version"
"$GALA" stdlib export --go-module "$module" --out "$work/out" --proxy "$work/proxy" --version "$version"
use_proxy

# Builds every package, not only the ones the consumer below imports. go vet
# is not run: the generated code has known vet findings (unreachable code
# after exhaustive matches), and a consumer's go vet ./... does not analyze
# its dependencies.
echo "+ go build in the exported module"
(cd "$work/out" && go build ./...)

echo "+ plain Go consumer via go mod tidy"
# The program imports go.gala.fyi/stdlib; another module path is substituted.
program="$work/program/main.go"
mkdir -p "$(dirname "$program")"
sed "s|go.gala.fyi/stdlib|$module|g" "$here/consumer/main.go.tmpl" >"$program"
check_consumer "$program" "$here/consumer/main.out" "$(go_version_of "$work/out/go.mod")" "$module@$version"
echo "ok: $module@$version builds and runs from a plain Go module"
