#!/usr/bin/env bash
set -euo pipefail

# Build iOS XCFramework for the Delight Go SDK using gomobile.
# Prereqs:
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   gomobile init

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SDK_PKG="github.com/bhandras/delight/cli/sdk"
OUT_DIR="${ROOT_DIR}/build"

mkdir -p "${OUT_DIR}"

gomobile bind -target=ios -o "${OUT_DIR}/DelightSDK.xcframework" "${SDK_PKG}"

echo "Built ${OUT_DIR}/DelightSDK.xcframework"
