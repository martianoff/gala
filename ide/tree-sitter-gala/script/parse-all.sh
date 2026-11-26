#!/usr/bin/env bash
#
# Parses every .gala file in the repository with the tree-sitter grammar and
# fails if any of them has an ERROR or MISSING node. Files that are meant not
# to parse are listed, with the reason, in test/parse-all.exclude.
#
#   ide/tree-sitter-gala/script/parse-all.sh
#
# Needs the tree-sitter CLI on PATH and a C compiler; run `tree-sitter
# generate` first if grammar.js changed.

set -euo pipefail

grammar_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "$grammar_dir/../.." && pwd)"
exclude_file="$grammar_dir/test/parse-all.exclude"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Repository-relative paths, one per line; `#` starts a comment.
sed -e 's/#.*//' -e 's/[[:space:]]*$//' -e '/^$/d' "$exclude_file" | sort -u > "$work/excluded"

# Skip hidden directories (.git, .claude/worktrees with whole copies of the
# repository, the .gala build cache with vendored stdlib snapshots),
# bazel-* and node_modules, as internal/parser/inputs_test.go does. Prune
# them rather than filter their files out, so find never walks them.
(cd "$repo_root" && find . \( -name '.?*' -o -name 'bazel-*' -o -name node_modules \) -prune \
    -o -type f -name '*.gala' -print \
  | sed 's|^\./||' | sort) > "$work/all"

# An exclusion that names no file is stale: the file moved or was deleted.
if stale="$(comm -23 "$work/excluded" "$work/all")" && [[ -n "$stale" ]]; then
  echo "test/parse-all.exclude lists files that do not exist:" >&2
  echo "$stale" >&2
  exit 1
fi

# Relative to $grammar_dir, where tree-sitter runs: Git Bash does not convert
# an absolute /c/... path inside the --paths file for a native tree-sitter.exe.
comm -23 "$work/all" "$work/excluded" | sed 's|^|../../|' > "$work/paths"

total="$(wc -l < "$work/all")"
excluded="$(wc -l < "$work/excluded")"
checked="$(wc -l < "$work/paths")"

cd "$grammar_dir"
# --quiet prints only the files that fail; tree-sitter exits non-zero if any do.
status=0
tree-sitter parse --quiet --stat --paths "$work/paths" || status=$?

# An excluded file that parses cleanly no longer needs its exclusion.
while IFS= read -r f; do
  if tree-sitter parse --quiet "../../$f" > /dev/null 2>&1; then
    echo "test/parse-all.exclude: $f parses cleanly now; remove it from the list" >&2
    status=1
  fi
done < "$work/excluded"

echo "parse-all: $checked of $total .gala files parsed, $excluded excluded"
exit "$status"
