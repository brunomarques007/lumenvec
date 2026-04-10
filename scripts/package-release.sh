#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

GO_BIN="${GO_BIN:-go}"
if ! command -v "$GO_BIN" >/dev/null 2>&1; then
  if command -v go.exe >/dev/null 2>&1; then
    GO_BIN="go.exe"
  else
    echo "go executable not found in PATH" >&2
    exit 1
  fi
fi

VERSION="${VERSION:-$(tr -d '\r\n' < VERSION)}"
GOOS_TARGET="${GOOS:-$("$GO_BIN" env GOOS)}"
GOARCH_TARGET="${GOARCH:-$("$GO_BIN" env GOARCH)}"
DIST_DIR="${DIST_DIR:-dist/release}"

mkdir -p "$DIST_DIR"

build_bundle() {
  local transport="$1"
  local config_file="$2"
  local bin_name="lumenvec"
  local bundle_dir="$DIST_DIR/lumenvec_${VERSION#v}_${GOOS_TARGET}_${GOARCH_TARGET}_${transport}"

  if [[ "$GOOS_TARGET" == "windows" ]]; then
    bin_name="lumenvec.exe"
  fi

  rm -rf "$bundle_dir"
  mkdir -p "$bundle_dir"

  echo "Building ${transport} bundle for ${GOOS_TARGET}/${GOARCH_TARGET}..."
  CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" "$GO_BIN" build -o "$bundle_dir/$bin_name" ./cmd/server

  cp "$config_file" "$bundle_dir/config.yaml"
  cp README.md "$bundle_dir/README.md"
  cp LICENSE "$bundle_dir/LICENSE"
  cp RELEASE.md "$bundle_dir/RELEASE.md"

  if [[ "$GOOS_TARGET" == "windows" ]]; then
    if command -v zip >/dev/null 2>&1; then
      (cd "$DIST_DIR" && zip -qr "$(basename "$bundle_dir").zip" "$(basename "$bundle_dir")")
    elif command -v tar >/dev/null 2>&1; then
      (cd "$DIST_DIR" && tar -a -cf "$(basename "$bundle_dir").zip" "$(basename "$bundle_dir")")
    else
      echo "neither zip nor tar is available to package Windows assets" >&2
      exit 1
    fi
  else
    tar -C "$DIST_DIR" -czf "${bundle_dir}.tar.gz" "$(basename "$bundle_dir")"
  fi
}

build_bundle "http" "configs/config.yaml"
build_bundle "grpc" "configs/config.grpc.yaml"

echo "Release artifacts written to $DIST_DIR"
