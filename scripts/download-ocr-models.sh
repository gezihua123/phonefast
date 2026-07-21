#!/usr/bin/env bash
# Thin wrapper — the real logic is in Python (scripts/download_models.py).
# Kept for backward compat: CI/workflows/docs call `bash scripts/download-ocr-models.sh`.
exec python3 "$(dirname "$0")/download_models.py" "$@"
