#!/bin/bash

set -euo pipefail

bump="${1:-patch}"

latest_tag="$(git tag --list 'v*' --sort=-version:refname | head -n 1)"
if [[ -z "$latest_tag" ]]; then
  latest_tag="v0.0.0"
fi

if [[ ! "$latest_tag" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "Latest tag $latest_tag does not match vMAJOR.MINOR.PATCH" >&2
  exit 1
fi

major="${BASH_REMATCH[1]}"
minor="${BASH_REMATCH[2]}"
patch="${BASH_REMATCH[3]}"

case "$bump" in
  major)
    major=$((major + 1))
    minor=0
    patch=0
    ;;
  minor)
    minor=$((minor + 1))
    patch=0
    ;;
  patch)
    patch=$((patch + 1))
    ;;
  *)
    echo "Unsupported bump type: $bump" >&2
    exit 1
    ;;
esac

echo "version=v${major}.${minor}.${patch}"
echo "previous_tag=${latest_tag}"
echo "bump=${bump}"
