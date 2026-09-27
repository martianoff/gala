#!/usr/bin/env bash
# Drives the gala CLI (not Bazel) over the project layouts in testdata/.
#
#   GALA=/path/to/gala tools/ci/cli_path/run_fixtures.sh <work-dir> [fixture...]
#
# With no fixture names, every fixture runs. Fixtures: root_main, cmd_app,
# nested, lib_only, go_subpkg, gala_dep, sequence.
#
# Every other test lane builds GALA with Bazel from repo sources. Users build
# with the CLI, which transpiles against the stdlib snapshot embedded in the
# binary, extracts it into GALA_HOME, and keeps per-project build workspaces
# there. None of that is exercised by Bazel, so this script runs the user path
# end to end with a GALA_HOME that starts empty.
#
# Each fixture is copied out of the repository first: the repo root has its
# own gala.mod (martianoff/gala), and a fixture built in place would find
# that one instead of its own.
#
# Works on Linux and on Windows under Git Bash; Windows is the reason it
# exists as much as Linux is (the .exe suffix, drive-letter paths, CRLF
# checkouts and BOM-prefixed sources all only show up there).
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${GALA:?set GALA to the gala binary under test}"
work=${1:?usage: run_fixtures.sh <work-dir> [fixture...]}
shift
mkdir -p "$work"
work=$(cd "$work" && pwd)

# One GALA_HOME for the whole run, starting empty, plus a second one that the
# sequence fixture uses as its from-scratch reference.
export GALA_HOME="$work/gala-home"
fresh_home="$work/gala-home-fresh"
rm -rf "$GALA_HOME" "$fresh_home"

# Checks run inside subshells (one per fixture), so failures are counted in a
# file rather than a variable.
failures_file="$work/failures"
: >"$failures_file"
fail() {
  echo "::error::$*"
  echo "$*" >>"$failures_file"
}

# exe <path-without-suffix> — the path with .exe appended where the OS adds it.
exe() {
  if [ -f "$1.exe" ]; then echo "$1.exe"; else echo "$1"; fi
}

# normalize — program output with CR stripped, so a CRLF checkout of the
# expected file compares equal to LF output (and vice versa).
normalize() { tr -d '\r'; }

# gala_ok <log> <args...> — run the CLI, keep its output, fail loudly on error.
gala_ok() {
  local log=$1
  shift
  echo "+ gala $*"
  if ! "$GALA" "$@" >"$log" 2>&1; then
    cat "$log"
    fail "gala $* failed (in $(basename "$(pwd)"))"
    return 1
  fi
  cat "$log"
}

# expect_output <label> <expected-file> <actual-file>
expect_output() {
  if diff -u <(normalize <"$2") <(normalize <"$3"); then
    echo "  ok: $1"
  else
    fail "$1: output differs from $(basename "$2")"
  fi
}

# expect_line <label> <file> <regex> — the CLI printed a line matching regex.
expect_line() {
  if normalize <"$2" | grep -Eq -- "$3"; then
    echo "  ok: $1"
  else
    fail "$1: expected a line matching /$3/"
  fi
}

# stage <fixture> — fresh copy of a fixture under the work dir; prints its path.
stage() {
  local dest="$work/$1"
  rm -rf "$dest"
  cp -R "$here/testdata/$1" "$dest"
  echo "$dest"
}

# root_main: package main at the module root.
fixture_root_main() {
  local dir
  dir=$(stage root_main)
  cd "$dir"
  gala_ok build.log build || return 0
  expect_line "build reports a binary" build.log '^Built: .*root_main'
  "$(exe "$dir/root_main")" >out.txt
  expect_output "built binary" expected.out out.txt
  gala_ok run.log run || return 0
  expect_output "gala run" expected.out run.log
}

# cmd_app: a library (with a test) at the root, the executable under cmd/app.
fixture_cmd_app() {
  local dir
  dir=$(stage cmd_app)
  cd "$dir"
  gala_ok build.log build || return 0
  expect_line "root builds as a library" build.log '^ok \(library compiled successfully\)'
  gala_ok build-app.log build -o app ./cmd/app || return 0
  "$(exe "$dir/app")" >out.txt
  expect_output "cmd/app binary" expected.out out.txt
  gala_ok run.log run ./cmd/app || return 0
  expect_output "gala run ./cmd/app" expected.out run.log
  gala_ok test.log test || return 0
  expect_line "root library test ran" test.log '^ok '
}

# nested: main -> internal/a -> internal/a/b, a BOM-prefixed source with
# non-ASCII text, and a test two packages below a package-main root.
fixture_nested() {
  local dir
  dir=$(stage nested)
  cd "$dir"
  gala_ok build.log build || return 0
  "$(exe "$dir/nested")" >out.txt
  expect_output "built binary" expected.out out.txt
  gala_ok run.log run || return 0
  expect_output "gala run" expected.out run.log
  gala_ok test.log test || return 0
  expect_line "subpackage test ran" test.log '^(ok |PASS$)'
}

# lib_only: no package main at all.
fixture_lib_only() {
  local dir
  dir=$(stage lib_only)
  cd "$dir"
  gala_ok build.log build || return 0
  expect_line "library compile check" build.log '^ok \(library compiled successfully\)'
  gala_ok test.log test || return 0
  expect_line "tests ran" test.log '^ok '
}

# go_subpkg: a hand-written Go package inside the module, whose types reach
# GALA code through inference.
fixture_go_subpkg() {
  local dir
  dir=$(stage go_subpkg)
  cd "$dir"
  gala_ok build.log build || return 0
  "$(exe "$dir/go_subpkg")" >out.txt
  expect_output "built binary" expected.out out.txt
}

# gala_dep: a published GALA module in gala.mod, fetched from GitHub into the
# empty GALA_HOME and built against. Needs network access.
fixture_gala_dep() {
  local dir
  dir=$(stage gala_dep)
  cd "$dir"
  gala_ok build.log build || return 0
  "$(exe "$dir/gala_dep")" >out.txt
  expect_output "built binary" expected.out out.txt
  gala_ok run.log run || return 0
  expect_output "gala run" expected.out run.log
}

# sequence: the order a user actually types commands in, against one warm
# GALA_HOME, then an edit, then the same builds from an empty GALA_HOME. Every
# binary a warm build produced must behave exactly like the from-scratch one:
# anything else is a build workspace or cache handing back stale output.
fixture_sequence() {
  local dir
  dir=$(stage sequence)
  cd "$dir"
  gala_ok s1.log build -o root1 || return 0
  gala_ok s2.log build -o app1 ./cmd/app || return 0
  gala_ok s3.log test || return 0
  expect_line "test in the sequence" s3.log '^(ok |PASS$)'
  gala_ok s4.log build -o root2 || return 0
  "$(exe "$dir/root1")" >root1.txt
  "$(exe "$dir/app1")" >app1.txt
  "$(exe "$dir/root2")" >root2.txt
  printf 'root label-v1\n' >want-root.txt
  printf 'app label-v1\n' >want-app.txt
  expect_output "root, first build" want-root.txt root1.txt
  expect_output "cmd/app after a root build" want-app.txt app1.txt
  expect_output "root rebuilt after build ./cmd/app and test" want-root.txt root2.txt

  # Edit the package both executables share and rebuild both, still warm.
  sed 's/label-v1/label-v2/' internal/a/b/label.gala >label.tmp
  mv label.tmp internal/a/b/label.gala
  gala_ok s5.log build -o root3 || return 0
  gala_ok s6.log build -o app3 ./cmd/app || return 0
  "$(exe "$dir/root3")" >root3.txt
  "$(exe "$dir/app3")" >app3.txt
  printf 'root label-v2\n' >want-root.txt
  printf 'app label-v2\n' >want-app.txt
  expect_output "root after editing internal/a/b" want-root.txt root3.txt
  expect_output "cmd/app after editing internal/a/b" want-app.txt app3.txt

  # The same tree from an empty GALA_HOME is the reference.
  GALA_HOME="$fresh_home" gala_ok f1.log build -o rootf || return 0
  GALA_HOME="$fresh_home" gala_ok f2.log build -o appf ./cmd/app || return 0
  "$(exe "$dir/rootf")" >rootf.txt
  "$(exe "$dir/appf")" >appf.txt
  expect_output "warm root build matches a fresh one" rootf.txt root3.txt
  expect_output "warm cmd/app build matches a fresh one" appf.txt app3.txt
}

fixtures=("$@")
if [ ${#fixtures[@]} -eq 0 ]; then
  fixtures=(root_main cmd_app nested lib_only go_subpkg gala_dep sequence)
fi

for name in "${fixtures[@]}"; do
  if ! declare -F "fixture_$name" >/dev/null; then
    fail "unknown fixture: $name"
    continue
  fi
  echo
  echo "=== $name"
  ( "fixture_$name" ) || fail "$name: aborted (see the output above)"
done

echo
if [ -s "$failures_file" ]; then
  echo "CLI-path fixtures: $(wc -l <"$failures_file") failure(s):"
  sed 's/^/  - /' "$failures_file"
  exit 1
fi
echo "CLI-path fixtures: all passed"
