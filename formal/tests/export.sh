#!/usr/bin/env bash
# Re-exports the Core JSON fixtures of formal/tests from their GALA sources:
# tests/src/<name>.gala -> tests/<name>.json, and examples/<name>.gala ->
# tests/ex_<name>.json for the S1 examples listed below. Run from the
# repository root after changing the exporter or a source. The expected
# .out/.outcome files are written by hand: each .out is what the program
# prints when built and run with gala (for examples, the example's own .out).
set -euo pipefail

examples=(
  composite_lit_if_cond
  composite_lit_if_expr_cond
  hello
  hex_literals
  match_nested_subject_unread
  match_sealed_branch_infer
  match_stmt_true_dangle
  match_void_dispatch
  sealed_equal
  semicolon_statement_separator
  tco_deep_sum
  tco_factorial
  tco_match_self_call
  with_main
)

export MSYS_NO_PATHCONV=1
bazel build //internal/formal/coreexport/galacore
galacore="$(bazel cquery --output=files //internal/formal/coreexport/galacore 2>/dev/null)"

for src in formal/tests/src/*.gala; do
  name="$(basename "$src" .gala)"
  "$galacore" "$src" > "formal/tests/$name.json"
done
for name in "${examples[@]}"; do
  "$galacore" "examples/$name.gala" > "formal/tests/ex_$name.json"
done
