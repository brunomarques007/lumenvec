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

VERSION="${VERSION:-$(git describe --tags --match 'v*' --abbrev=0 2>/dev/null || echo dev)}"
GOOS_TARGET="${GOOS:-$("$GO_BIN" env GOOS)}"
GOARCH_TARGET="${GOARCH:-$("$GO_BIN" env GOARCH)}"
DIST_DIR="${DIST_DIR:-dist/release}"

mkdir -p "$DIST_DIR"

build_bundle() {
  local transport="$1"
  local config_file="$2"
  local bin_name="lumenvec"
  local bundle_name="lumenvec-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}-${transport}"
  local bundle_dir="$DIST_DIR/$bundle_name"

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
  cp CHANGELOG.md "$bundle_dir/CHANGELOG.md"
  cat > "$bundle_dir/BUILD_INFO.txt" <<EOF
LumenVec release bundle
Version: $VERSION
Platform: $GOOS_TARGET/$GOARCH_TARGET
Transport: $transport
Binary: $bin_name
Config file: config.yaml
EOF

  if [[ "$GOOS_TARGET" == "windows" ]]; then
    if command -v zip >/dev/null 2>&1; then
      (cd "$DIST_DIR" && zip -qr "${bundle_name}.zip" "$bundle_name")
    elif command -v tar >/dev/null 2>&1; then
      (cd "$DIST_DIR" && tar -a -cf "${bundle_name}.zip" "$bundle_name")
    else
      echo "neither zip nor tar is available to package Windows assets" >&2
      exit 1
    fi
  else
    tar -C "$DIST_DIR" -czf "$DIST_DIR/${bundle_name}.tar.gz" "$bundle_name"
  fi
}

build_bundle "http" "configs/config.yaml"
build_bundle "grpc" "configs/config.grpc.yaml"

echo "Release artifacts written to $DIST_DIR"
