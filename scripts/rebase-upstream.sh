#!/usr/bin/env bash
# Rebase our mkwfc fork onto the latest upstream WiiLink24/wfc-server.
#
# Our fork diverges from upstream mainly in the `database/` package (Postgres -> SQLite)
# and in a few custom admin/bans/motd additions. Expect conflicts there; resolve by
# keeping our SQLite query logic while taking upstream's behavioural changes.
set -euo pipefail

cd "$(dirname "$0")/.."

BRANCH="mkwfc"
REMOTE="upstream"
UPSTREAM_BRANCH="${1:-main}"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "error: working tree is dirty; commit or stash before rebasing." >&2
  exit 1
fi

echo ">> fetching ${REMOTE}/${UPSTREAM_BRANCH}"
git fetch "$REMOTE" "$UPSTREAM_BRANCH"

echo ">> rebasing ${BRANCH} onto ${REMOTE}/${UPSTREAM_BRANCH}"
git checkout "$BRANCH"
git rebase "${REMOTE}/${UPSTREAM_BRANCH}"

echo ">> done. Review database/ and run: go build ./... && go test ./database/..."
