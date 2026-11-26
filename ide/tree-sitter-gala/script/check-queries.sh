#!/usr/bin/env bash
#
# Checks queries/highlights.scm against its sources of truth:
#
#   - every keyword of gala.g4 (a word in quotes, 'val', 'match', ...) has a
#     capture, as "val" or as a node such as (true);
#   - the @type.builtin names are exactly IsPrimitiveType's in
#     internal/transpiler/types.go.
#
# A keyword the grammar parses but the query never captures would go
# unhighlighted without any test failing, so this catches it.
#
# It also compiles queries/indents.scm, which `tree-sitter test` does not load
# (tree-sitter.json names no indents query), so a node it names that the
# grammar no longer has fails here.

set -euo pipefail

grammar_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "$grammar_dir/../.." && pwd)"
highlights="$grammar_dir/queries/highlights.scm"
status=0

# The query's patterns alone: no comments, and no predicates, whose strings
# (`#any-of? ... "int"`) are names to compare against, not captures. A
# comment's `;` starts a line or follows a space; the `";"` capture's does not.
patterns="$(perl -0pe 's/(^|[ \t]);[^\n]*/$1/mg; s/\(#[^()]*\)//g' "$highlights")"

while IFS= read -r keyword; do
  if ! grep -qE "(\"$keyword\"|\($keyword\))" <<< "$patterns"; then
    echo "queries/highlights.scm: gala.g4 keyword '$keyword' has no capture" >&2
    status=1
  fi
done < <(sed 's|//.*||' "$repo_root/internal/parser/grammar/gala.g4" | grep -oE "'[a-z]+'" | tr -d "'" | sort -u)

# The quoted names in IsPrimitiveType's switch, and in the query's any-of list.
want="$(sed -n '/^func IsPrimitiveType/,/^}/p' "$repo_root/internal/transpiler/types.go" \
  | grep -oE '"[a-z0-9]+"' | tr -d '"' | sort -u)"
have="$(sed -n '/#any-of? @type.builtin/,/))/p' "$highlights" \
  | grep -oE '"[a-z0-9]+"' | tr -d '"' | sort -u)"
if [[ "$want" != "$have" ]]; then
  echo "queries/highlights.scm: @type.builtin names differ from IsPrimitiveType (< types.go, > query):" >&2
  diff <(echo "$want") <(echo "$have") >&2 || true
  status=1
fi

if ! (cd "$grammar_dir" && tree-sitter query queries/indents.scm test/highlight/basics.gala > /dev/null); then
  echo "queries/indents.scm does not compile against the grammar" >&2
  status=1
fi

if [[ "$status" -eq 0 ]]; then
  echo "check-queries: highlights.scm covers every gala.g4 keyword and builtin type; indents.scm compiles"
fi
exit "$status"
