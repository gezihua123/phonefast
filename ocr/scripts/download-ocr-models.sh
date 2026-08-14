#!/usr/bin/env bash
# Thin wrapper -> ./download.sh (the canonical OCR asset downloader).
# Kept for backward compat: docs/dev call `bash ocr/scripts/download-ocr-models.sh`.
#
# Translates legacy --models/--lib flags to download.sh subcommands. --target is
# accepted for compat but ignored (CI builds native; host == target).
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DL="$SCRIPT_DIR/download.sh"

models=false; lib=false
while [ $# -gt 0 ]; do
  case "$1" in
    --models) models=true; shift ;;
    --lib)    lib=true; shift ;;
    --all)    models=true; lib=true; shift ;;
    --target) shift 2 ;;        # value ignored, host only
    --force)  shift ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

if $models && $lib; then what=all
elif $models; then what=models
elif $lib; then what=lib
else what=all; fi

exec bash "$DL" "$what"
