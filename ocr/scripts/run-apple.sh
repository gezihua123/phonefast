#!/usr/bin/env bash
set -euo pipefail

# Run phonefast with Apple Vision OCR only (no models, no ONNX runtime)
# Usage: bash ocr/scripts/run-apple.sh [command] [args...]

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DIST_DIR="${SCRIPT_DIR}/../dist"
mkdir -p "$DIST_DIR"

# Build if not exists
if [ ! -f "$DIST_DIR/phonefast-apple" ]; then
    echo "Building Apple-only binary..."
    CGO_ENABLED=1 go build -tags NO_OCR_MODELS -o "$DIST_DIR/phonefast-apple" "${SCRIPT_DIR}/../../cmd/phonefast/"
fi

# Run with Apple engine
PHONEFAST_OCR_ENGINE=apple exec "$DIST_DIR/phonefast-apple" "$@"
