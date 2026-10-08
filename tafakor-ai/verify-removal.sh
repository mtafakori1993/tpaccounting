#!/bin/sh
# Verifies the removal procedure on a throwaway git worktree of the current commit:
#  1. remove touchpoints (+ tagging edits) and the tafakor-ai folder,
#  2. the tree must be identical to the base branch (default: main), except the product
#     features listed in product-changes.txt (they belong to the app and stay),
#  3. the backend builds and its server tests pass, the frontend builds.
# Nothing in the working copy is modified.
set -eu
BASE="${1:-main}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WT="$(mktemp -d "${TMPDIR:-/tmp}/tpai-removal-XXXXXX")"
cleanup() { git -C "$ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || rm -rf "$WT"; }
trap cleanup EXIT
git -C "$ROOT" worktree add --detach "$WT" HEAD >/dev/null
cd "$WT"
sh tafakor-ai/remove-touchpoints.sh --tags
KEEP="$(grep -v '^#' tafakor-ai/product-changes.txt | sed '/^$/d' | sed 's/^/:!/')"
rm -rf tafakor-ai
git add -A >/dev/null
# shellcheck disable=SC2086
if ! git diff --cached --quiet "$BASE" -- . $KEEP ; then
  echo "FAIL: tree differs from $BASE after removal:"
  git diff --cached --stat "$BASE" -- . $KEEP
  exit 1
fi
echo "OK: after removal the tree is identical to $BASE (product features kept: $(echo "$KEEP" | wc -l) files)"
(cd backend && go build ./... && go test ./internal/server/ ./internal/auth/ >/dev/null) && echo "OK: backend builds, server tests pass"
if [ -d "$ROOT/frontend/node_modules" ]; then
  ln -s "$ROOT/frontend/node_modules" frontend/node_modules
  (cd frontend && npx vite build >/dev/null 2>&1) && echo "OK: frontend builds"
  rm -f frontend/node_modules
fi
echo "REMOVAL VERIFIED"
