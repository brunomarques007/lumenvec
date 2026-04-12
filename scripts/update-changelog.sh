#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${1:?version is required}"
NOTES_FILE="${2:?notes file is required}"
CHANGELOG_FILE="${3:-CHANGELOG.md}"
RELEASE_DATE="${RELEASE_DATE:-$(date -u +%F)}"

if [[ ! -f "$NOTES_FILE" ]]; then
  echo "Notes file not found: $NOTES_FILE" >&2
  exit 1
fi

if [[ ! -f "$CHANGELOG_FILE" ]]; then
  cat > "$CHANGELOG_FILE" <<EOF
# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog.

## [Unreleased]

EOF
fi

if grep -q "^## \[${VERSION}\]" "$CHANGELOG_FILE"; then
  echo "Changelog already contains ${VERSION}"
  exit 0
fi

tmp_file="$(mktemp)"
entry_file="$(mktemp)"

{
  echo "## [${VERSION}] - ${RELEASE_DATE}"
  sed '1d' "$NOTES_FILE"
  echo
} > "$entry_file"

awk -v entry_file="$entry_file" '
  BEGIN {
    inserted = 0
    while ((getline line < entry_file) > 0) {
      entry = entry line "\n"
    }
    close(entry_file)
  }
  {
    print
    if (!inserted && $0 == "## [Unreleased]") {
      print ""
      printf "%s", entry
      inserted = 1
    }
  }
  END {
    if (!inserted) {
      print ""
      print "## [Unreleased]"
      print ""
      printf "%s", entry
    }
  }
' "$CHANGELOG_FILE" > "$tmp_file"

mv "$tmp_file" "$CHANGELOG_FILE"
rm -f "$entry_file"
