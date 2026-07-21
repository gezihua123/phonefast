#!/usr/bin/env bash
# Thin wrapper — the real logic is in Python (scripts/build.py build --all --ensure-ffmpeg).
# Kept for backward compat: docs/dev call `bash scripts/build_local.sh`.
exec python3 "$(dirname "$0")/build.py" --all --ensure-ffmpeg "$@"
