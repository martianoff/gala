#!/usr/bin/env bash
#
# Mechanical checks for the rules in AGENTS.md that a reviewer — human or
# agent — should never have to catch by eye. CI runs this on every pull
# request; run it locally before pushing.
#
#   tools/check_repo_policy.sh                 # check the working tree
#   tools/check_repo_policy.sh origin/master   # ... and commit messages in origin/master..HEAD
#
# Checks:
#   1. No generated parser sources are committed under internal/parser/grammar.
#      Bazel generates them from gala.g4; a checked-in copy silently diverges.
#   2. No internal ticket IDs (FIX-001, BUG-10, ...) in tracked files or, when
#      a base ref is given, in the messages of commits since that base.
#      Public GitHub references (#123) are fine.
#   3. The tool-specific agent files still import AGENTS.md, so every agent
#      reads the same rules.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

base="${1:-}"
failed=0

fail() {
  echo "::error::$*" >&2
  failed=1
}

# --- 1. generated parser ----------------------------------------------------
generated="$(git ls-files 'internal/parser/grammar/*.go')"
if [[ -n "$generated" ]]; then
  fail "generated parser sources are committed; edit internal/parser/grammar/gala.g4 instead and remove:"
  echo "$generated" >&2
fi

# --- 2. internal ticket IDs -------------------------------------------------
# Uppercase prefix, dash, number: the shape private trackers and ad-hoc fix
# batches use. Files that document the rule itself are excluded.
ticket_re='(^|[^A-Za-z0-9_])(FIX|BUG|INC|JIRA|TICKET|ISSUE)-[0-9]+'
ticket_excludes=(
  ':!AGENTS.md'
  ':!CONTRIBUTING.MD'
  ':!tools/check_repo_policy.sh'
)

if hits="$(git grep -nIE "$ticket_re" -- . "${ticket_excludes[@]}")"; then
  fail "internal ticket IDs found; describe the problem on its own terms (AGENTS.md rule 7):"
  echo "$hits" >&2
fi

if [[ -n "$base" ]]; then
  while read -r sha; do
    if msg_hits="$(git log -1 --format=%B "$sha" | grep -nE "$ticket_re")"; then
      fail "commit $(git log -1 --format='%h %s' "$sha") references an internal ticket ID:"
      echo "$msg_hits" >&2
    fi
  done < <(git rev-list --no-merges "$base..HEAD")
fi

# --- 3. agent files point at AGENTS.md --------------------------------------
[[ -f AGENTS.md ]] || fail "AGENTS.md is missing"
for f in CLAUDE.MD GEMINI.md; do
  if [[ ! -f "$f" ]]; then
    fail "$f is missing; it must contain '@AGENTS.md'"
  elif ! grep -qx '@AGENTS.md' "$f"; then
    fail "$f no longer imports AGENTS.md; keep project rules in AGENTS.md and '@AGENTS.md' in $f"
  fi
done
if ! grep -q 'AGENTS.md' .github/copilot-instructions.md 2>/dev/null; then
  fail ".github/copilot-instructions.md must point to AGENTS.md"
fi

if (( failed )); then
  echo "Repo policy check FAILED." >&2
  exit 1
fi
echo "Repo policy check passed."
