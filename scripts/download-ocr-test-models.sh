#!/usr/bin/env bash
# Thin wrapper -> ocr/scripts/download_test_models.py (the canonical test-model
# downloader). Kept for backward compat: docs/dev call `bash scripts/download-ocr-test-models.sh`.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
exec python3 "$SCRIPT_DIR/../ocr/scripts/download_test_models.py" "$@"
