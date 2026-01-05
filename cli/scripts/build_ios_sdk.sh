#!/usr/bin/env bash
set -euo pipefail

# Build iOS XCFramework for the Delight Go SDK using gomobile.
# Prereqs:
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   gomobile init

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT_DIR}/build"
OUT_PATH="${OUT_DIR}/DelightSDK.xcframework"

mkdir -p "${OUT_DIR}"

# Xcode run script phases execute with a very minimal PATH, so `go` may not be
# discoverable even when it is installed (e.g. via Homebrew). Expand PATH with
# common Go install locations before we proceed.
if ! command -v go >/dev/null 2>&1; then
  for candidate in \
    "/opt/homebrew/bin" \
    "/usr/local/bin" \
    "/opt/homebrew/opt/go/libexec/bin" \
    "$HOME/go/bin" \
    "$HOME/.local/bin" \
    "$HOME/bin"; do
    if [ -x "${candidate}/go" ]; then
      export PATH="${candidate}:${PATH}"
      break
    fi
  done
fi

if ! command -v go >/dev/null 2>&1; then
  echo "Error: go executable not found in PATH." >&2
  echo "Install Go and/or ensure it is visible to Xcode build scripts." >&2
  echo "Tried common locations like /opt/homebrew/bin and /usr/local/bin." >&2
  exit 127
fi

export GOPATH="${GOPATH:-$HOME/go}"
export PATH="${GOPATH}/bin:${PATH}"

# IMPORTANT:
#   We build in module mode so that Go's toolchain directive (go.mod `toolchain go1.24.5`)
#   is honored. Running `gomobile bind` with `GO111MODULE=off` forces GOPATH mode and
#   bypasses toolchain selection, which can produce hard-to-debug runtime crashes in
#   the gomobile-generated framework.
export GO111MODULE=on
export GOTOOLCHAIN="${DELIGHT_IOS_GOTOOLCHAIN:-go1.24.5}"
export GOWORK=off

(
  cd "${ROOT_DIR}"

  # Ensure module deps are present (and toolchain downloaded) before invoking gomobile.
  go mod download

  # IMPORTANT:
  #   We always set the `gomobile` build tag so the SDK can exclude desktop-only
  #   APIs that are unsafe across the gomobile/cgo boundary (e.g. string/[]byte
  #   return values).
  #
  # NOTE:
  #   We intentionally do not pass the `lldb` build tag. Recent Go toolchains can
  #   fail iOS builds under cgo with:
  #     -Werror,-Wdeclaration-after-statement
  #   in runtime/cgo sources, causing `make ios-run` to fail and retry. Keep the
  #   build tag surface stable by always building without `lldb`.
  #
  # `gomobile bind` fails if the output already exists, and Xcode builds can
  # re-run this script frequently. Remove the previous XCFramework to keep
  # builds deterministic.
  rm -rf "${OUT_PATH}"
  gomobile bind -tags gomobile -target=ios -o "${OUT_PATH}" ./sdk
)

echo "Built ${OUT_PATH}"
