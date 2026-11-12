#!/usr/bin/env bash
# Runs galaspec's fixture tests (tests/GalaTests.lean) from the binary's own
# package directory in the runfiles tree, where they find tests/*.json.
# Usage: galaspec_tests.sh BINARY (its rootpath)
set -euo pipefail
bin="$PWD/$1"
cd "$(dirname "$1")"
"$bin"
