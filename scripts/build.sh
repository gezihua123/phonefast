#!/usr/bin/env bash
# Thin wrapper — the real logic is in Python (scripts/build.py).
# Kept for backward compat: local dev and docs call `bash scripts/build.sh`.
# (CI/release workflows inline `go build` directly — they don't use this wrapper.)
exec python3 "$(dirname "$0")/build.py" "$@"
