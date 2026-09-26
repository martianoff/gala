#!/usr/bin/env bash
#
# Mechanical checks for the rules in AGENTS.md that a reviewer — human or
# agent — should never have to catch by eye. CI runs this on every pull
# request; run it locally before pushing.
#
#   tools/check_repo_policy.sh                 # check the working tree
#   tools/check_repo_policy.sh origin/master   # ... and commit messages in origin/master..HEAD
#
# PR_TITLE and PR_BODY, when set, are checked too: master squash-merges, so the
# PR title and description become the commit message that lands.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

base="${1:-}"
failed=0

fail() {
  echo "::error::$*" >&2
  failed=1
}

# --- 1. generated parser (AGENTS.md rule 1) ---------------------------------
# .gitignore already ignores these; this catches a `git add -f`.
generated="$(git ls-files 'internal/parser/grammar/*.go' 'internal/parser/grammar/*.java')"
if [[ -n "$generated" ]]; then
  fail "generated parser sources are committed; edit internal/parser/grammar/gala.g4 instead and remove:"
  echo "$generated" >&2
fi

# --- 2. internal ticket IDs (AGENTS.md rule 7) ------------------------------
# An uppercase tracker key, a dash, a number. Docs that explain the rule write
# the shape as KEY-<n>, which this does not match, so nothing is excluded.
ticket_re='(^|[^A-Za-z0-9_])(FIX|BUG|INC|JIRA|TICKET|ISSUE)-[0-9]+'

if hits="$(git grep -nIE "$ticket_re")"; then
  fail "internal ticket IDs found in files; describe the problem on its own terms:"
  echo "$hits" >&2
fi

if [[ -n "$base" ]]; then
  # One pass over every message; each line is prefixed with its commit so a hit
  # names the commit to reword.
  if hits="$(git log --no-merges --format='@@commit %h%n%B' "$base..HEAD" \
      | awk '/^@@commit / { sha = $2; next } { print sha ": " $0 }' \
      | grep -E "$ticket_re")"; then
    fail "commit messages reference internal ticket IDs; reword them before merging:"
    echo "$hits" >&2
  fi
fi

if hits="$(printf '%s\n%s\n' "${PR_TITLE:-}" "${PR_BODY:-}" | grep -nE "$ticket_re")"; then
  fail "the PR title or description references internal ticket IDs; edit the PR:"
  echo "$hits" >&2
fi

# --- 3. agent files point at AGENTS.md --------------------------------------
# file -> line it must contain
declare -A pointers=(
  [CLAUDE.MD]='@AGENTS.md'
  [GEMINI.md]='@AGENTS.md'
  [.github/copilot-instructions.md]='All agent instructions for this repository live in [AGENTS.md](../AGENTS.md) at the'
)
for f in "${!pointers[@]}"; do
  tr -d '\r' < "$f" 2>/dev/null | grep -qxF "${pointers[$f]}" \
    || fail "$f must keep pointing at AGENTS.md (missing line: ${pointers[$f]})"
done

if (( failed )); then
  echo "Repo policy check FAILED." >&2
  exit 1
fi
echo "Repo policy check passed."
