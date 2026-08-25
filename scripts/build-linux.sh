#!/usr/bin/env bash
# Build Matea for Linux on Windows/macOS/Linux hosts.
# Usage:
#   scripts/build-linux.sh           # linux/amd64
#   scripts/build-linux.sh amd64     # explicit arch
#   scripts/build-linux.sh arm64     # ARM64 servers
#
# The binary is written to dist/matea-linux-<arch>.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

ARCH="${1:-amd64}"
case "$ARCH" in
  amd64|x86_64)
    ARCH=amd64
    ;;
  arm64|aarch64)
    ARCH=arm64
    ;;
  arm|armv7)
    ARCH=arm
    export GOARM=7
    ;;
  *)
    echo "Unsupported architecture: $ARCH"
    echo "Supported: amd64, arm64, arm"
    exit 1
    ;;
esac

OUTPUT="dist/matea-linux-${ARCH}"
mkdir -p dist

echo "Building Matea for linux/$ARCH ..."
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -ldflags='-s -w' -o "$OUTPUT" .

echo "Built: $OUTPUT"
echo "File info:"
file "$OUTPUT" || true
ls -lh "$OUTPUT"
