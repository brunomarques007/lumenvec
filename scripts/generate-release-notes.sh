#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${1:?version is required}"
PREVIOUS_TAG="${2:-}"

if [[ -z "$PREVIOUS_TAG" ]]; then
  PREVIOUS_TAG="$(git tag --list 'v*' --sort=-version:refname | head -n 1)"
fi

if [[ -z "$PREVIOUS_TAG" ]]; then
  PREVIOUS_TAG="v0.0.0"
fi

echo "## ${VERSION}"
echo
echo "Automated release from \`${PREVIOUS_TAG}\` to \`${VERSION}\`."
echo
echo "### Commits"

notes="$(
  git log --no-merges --pretty='%s (%h)' "${PREVIOUS_TAG}..HEAD" \
    | grep -vE '^docs\(changelog\): update for v[0-9]+\.[0-9]+\.[0-9]+ \([0-9a-f]+\)$' \
    | sed 's/^/- /'
)"

if [[ -n "$notes" ]]; then
  printf '%s\n' "$notes"
else
  echo "- No user-facing commits since ${PREVIOUS_TAG}"
fi
