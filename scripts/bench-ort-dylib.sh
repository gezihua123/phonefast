#!/usr/bin/env bash
set -euo pipefail

# bench-ort-dylib.sh — Compare brew vs release-static libonnxruntime performance
# with a 50-iteration benchmark each. Builds a test binary per dylib variant
# (the dylib is embedded at build time), runs BenchmarkOCRBench on the densest
# image (05_fixed.png, 34 boxes), and prints avg/median/min/max for each.
#
# Usage: bash scripts/bench-ort-dylib.sh
#
# Prereq: /tmp/ort-brew.dylib and /tmp/ort-release.dylib staged (this script
# stages them if missing). Requires the pinned static FFmpeg env for CGO build.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT"

FF_PREFIX="$ROOT/build/cross-ffmpeg/aarch64-darwin"
export PKG_CONFIG_PATH="$FF_PREFIX/lib/pkgconfig"
export CGO_CFLAGS="-I$FF_PREFIX/include"
export CGO_LDFLAGS="-L$FF_PREFIX/lib"
export CGO_ENABLED=1 GOOS=darwin GOARCH=arm64

ASSET="$ROOT/assets/ocr/libonnxruntime-darwin-arm64.dylib"
BREW="/tmp/ort-brew.dylib"
RELEASE="/tmp/ort-release.dylib"

# Stage dylibs if missing.
[ -f "$BREW" ] || cp /opt/homebrew/lib/libonnxruntime.dylib "$BREW"
if [ ! -f "$RELEASE" ]; then
  echo "Fetching release-static ORT 1.23.0..."
  curl -fsSL -o /tmp/ort-rel.tgz "https://github.com/microsoft/onnxruntime/releases/download/v1.23.0/onnxruntime-osx-arm64-1.23.0.tgz"
  mkdir -p /tmp/ort-rel && tar -xzf /tmp/ort-rel.tgz -C /tmp/ort-rel
  cp /tmp/ort-rel/onnxruntime-osx-arm64-1.23.0/lib/libonnxruntime.dylib "$RELEASE"
fi

ORIG=$(cat "$ASSET" | wc -c | tr -d ' ')
trap 'echo "restoring original dylib..."; cp "$BREW" "$ASSET"' EXIT

run_variant() {
  local label="$1" lib="$2"
  echo ""
  echo "════════════════════════════════════════════════════════════════"
  echo "  $label  ($(wc -c < "$lib" | tr -d ' ') bytes)"
  echo "════════════════════════════════════════════════════════════════"
  cp "$lib" "$ASSET"
  # -benchtime=50x = exactly 50 iterations. -run ^$ skips tests.
  go test -bench BenchmarkOCRBench -benchtime=50x -run '^$' -count 1 \
    ./tests/ocr-benchmark/ 2>&1 | grep -E "BenchmarkOCRBench|avg|median|ms/op|allocs"
}

run_variant "brew-1.27.1 (19MB, dynamic deps)" "$BREW"
run_variant "release-1.23.0 (35MB, static, self-contained)" "$RELEASE"

echo ""
echo "Done. Original dylib restored."
