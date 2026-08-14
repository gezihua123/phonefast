#!/usr/bin/env bash
# Thin wrapper -> ocr/scripts/download.sh (the canonical OCR asset downloader).
# Kept for backward compat: CI/workflows/docs call `bash scripts/download-ocr-models.sh`.
#
# The old scripts/download_models.py was tied to the pre-extract layout
# (assets/ocr/) and has been retired; ocr/scripts/download.sh downloads into
# ocr/assets/ which is where the //go:embed directives now read from.
#
# CI builds run on native runners (matrix goos/goarch == host), so --target is
# accepted for compat but ignored: the host lib is staged, matching the host build.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DL="$SCRIPT_DIR/../ocr/scripts/download.sh"

if [ ! -f "$DL" ]; then
  echo "error: ocr/scripts/download.sh not found at $DL" >&2
  exit 1
fi

models=false; lib=false
while [ $# -gt 0 ]; do
  case "$1" in
    --models) models=true; shift ;;
    --lib)    lib=true; shift ;;
    --all)    models=true; lib=true; shift ;;
    --target) shift 2 ;;        # value (e.g. darwin/arm64) - ignored, host only
    --force)  shift ;;           # download.sh re-checks; CI runs on fresh clone
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

if $models && $lib; then what=all
elif $models; then what=models
elif $lib; then what=lib
else what=all; fi   # no flags = both (preserves old default)

exec bash "$DL" "$what"
