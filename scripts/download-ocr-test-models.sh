#!/usr/bin/env bash
# Thin wrapper — the real logic is in Python (scripts/download_test_models.py).
# Kept for backward compat: docs/dev call `bash scripts/download-ocr-test-models.sh`.
exec python3 "$(dirname "$0")/download_test_models.py" "$@"
