#!/usr/bin/env bash
# Runs galaspec's fixture tests (tests/GalaTests.lean) from formal/ in the
# runfiles tree, where they find tests/*.json. Usage: galaspec_tests.sh BINARY
set -euo pipefail
bin="$PWD/$1"
cd formal
"$bin"
