#!/usr/bin/env bash
# Deprecated thin wrapper — use setup-ncnn.sh instead.
# Kept so existing docs/commands still work. Builds the NCNN rec model only
# (the lib install is now part of setup-ncnn.sh --lib).
exec bash "$(dirname "$0")/setup-ncnn.sh" --model "$@"
