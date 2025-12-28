#!/usr/bin/env bash
set -euo pipefail

# Build iOS XCFramework for the Delight Go SDK using gomobile.
# Prereqs:
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   gomobile init

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT_DIR}/build"

mkdir -p "${OUT_DIR}"

export GOPATH="${GOPATH:-$HOME/go}"
export PATH="${GOPATH}/bin:${PATH}"

TMP_GOPATH="$(mktemp -d)"
cleanup() {
  rm -rf "${TMP_GOPATH}"
}
trap cleanup EXIT

SRC_BASE="${TMP_GOPATH}/src/github.com/bhandras/delight"
mkdir -p "${SRC_BASE}"

if command -v rsync >/dev/null 2>&1; then
  rsync -a \
    --exclude "build" \
    --exclude "node_modules" \
    --exclude ".git" \
    "${ROOT_DIR}/" "${SRC_BASE}/cli/"
else
  cp -R "${ROOT_DIR}" "${SRC_BASE}/cli"
fi

if [[ -d "${GOPATH}/src/golang.org/x/mobile" ]]; then
  mkdir -p "${TMP_GOPATH}/src/golang.org/x"
  ln -s "${GOPATH}/src/golang.org/x/mobile" "${TMP_GOPATH}/src/golang.org/x/mobile"
else
  echo "gomobile requires golang.org/x/mobile in GOPATH/src. Run: go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init" >&2
  exit 1
fi

(
  cd "${SRC_BASE}/cli"
  GO111MODULE=on GOMOD="${SRC_BASE}/cli/go.mod" go mod vendor
  if ! GO111MODULE=off \
    GOMOD= \
    GOPATH="${TMP_GOPATH}" \
    CC="clang -std=gnu99 -Wno-error=declaration-after-statement -Wno-declaration-after-statement" \
    CGO_CFLAGS="-std=gnu99 -Wno-error=declaration-after-statement -Wno-declaration-after-statement" \
    GOGCCFLAGS="-std=gnu99 -Wno-error=declaration-after-statement -Wno-declaration-after-statement" \
    gomobile bind -tags lldb -target=ios -o "${OUT_DIR}/DelightSDK.xcframework" github.com/bhandras/delight/cli/sdk; then
    echo "lldb build tag failed; retrying without lldb." >&2
    GO111MODULE=off \
    GOMOD= \
    GOPATH="${TMP_GOPATH}" \
    gomobile bind -target=ios -o "${OUT_DIR}/DelightSDK.xcframework" github.com/bhandras/delight/cli/sdk
  fi
)

echo "Built ${OUT_DIR}/DelightSDK.xcframework"
