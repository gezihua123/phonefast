#!/usr/bin/env bash
set -euo pipefail

# setup-ncnn.sh — One-shot setup for the NCNN OCR engine (macOS only).
#
# The NCNN engine is an opt-in, macOS-only recognition backend (build tag
# `darwin && cgo && ncnn`). It swaps in Tencent's NCNN for the rec step and
# reuses the macOS Vision ANE path for detection (same as the onnx engine).
# In practice ~22% faster end-to-end than onnx on Apple Silicon.
#
# This script fixes the engine's two external dependencies:
#   1. CAPABILITY  — the NCNN C library (libncnn.dylib + c_api.h), via `brew install ncnn`.
#   2. DATA        — the converted rec model (rec.ncnn.param + rec.ncnn.bin),
#                    generated from the PP-OCR v3 rec ONNX via pnnx.
#
# Both are pinned: brew ncnn version is whatever brew ships (currently 20260526);
# the model is shape-specialized to [1,3,48,320] (NCNN can't handle the rec
# model's dynamic width unspecialized — see docs/DEV.md).
#
# Usage:
#   bash scripts/setup-ncnn.sh            # install lib + (re)build model
#   bash scripts/setup-ncnn.sh --lib      # lib only (brew install ncnn)
#   bash scripts/setup-ncnn.sh --model    # model only (pnnx convert)
#
# After setup, build & run:
#   CGO_ENABLED=1 go build -tags ncnn ./cmd/phonefast/
#   PHONEFAST_OCR_ENGINE=ncnn \
#     PHONEFAST_NCNN_PARAM=<repo>/tests/ocr-models/ncnn/rec.ncnn.param \
#     PHONEFAST_NCNN_BIN=<repo>/tests/ocr-models/ncnn/rec.ncnn.bin \
#     phonefast daemon --foreground

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
MODEL_DIR="$PROJECT_ROOT/tests/ocr-models/ncnn"
SRC_ONNX="$PROJECT_ROOT/assets/ocr/ppocr-rec.onnx"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; NC='\033[0m'
log()  { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[x]${NC} $*" >&2; exit 1; }

# macOS-only (the engine's build tag is darwin && cgo && ncnn).
[ "$(uname -s)" = "Darwin" ] || err "NCNN engine is macOS-only (build tag: darwin && cgo && ncnn)."

do_lib=true
do_model=true
if [ $# -gt 0 ]; then
    do_lib=false; do_model=false
    for arg in "$@"; do
        case "$arg" in
            --lib)   do_lib=true ;;
            --model) do_model=true ;;
            *) err "Unknown option: $arg" ;;
        esac
    done
fi

# ── 1. CAPABILITY: NCNN C library via brew ──────────────────────
# brew installs libncnn.dylib + c_api.h into /opt/homebrew (arm64) or
# /usr/local (intel), and ships ncnn.pc for pkg-config. The engine's cgo
# uses pkg-config (no hardcoded paths), so any brew prefix works.
install_lib() {
    log "Setting up NCNN C library (brew)..."
    command -v brew >/dev/null 2>&1 || err "Homebrew not found. Install from https://brew.sh"

    if brew list ncnn >/dev/null 2>&1; then
        log "brew ncnn already installed: $(brew list --versions ncnn | head -1)"
    else
        log "Installing ncnn via brew (this also pulls libomp/molten-vk/glslang)..."
        brew install ncnn
    fi

    # Sanity: c_api.h + libncnn.dylib + ncnn.pc must all be present.
    local pc; pc=$(pkg-config --exists ncnn 2>/dev/null && echo ok || echo "")
    local dylib; dylib=$(brew --prefix ncnn)/lib/libncnn.dylib
    [ -n "$pc" ] || err "pkg-config cannot find ncnn.pc. Run: brew install ncnn"
    [ -f "$dylib" ] || err "libncnn.dylib missing at $dylib"
    log "  c_api.h:   $(brew --prefix ncnn)/include/ncnn/c_api.h"
    log "  dylib:     $dylib"
    log "  pkg-config: ncnn.pc found"
}

# ── 2. DATA: convert PP-OCR rec ONNX → NCNN model ───────────────
# pnnx converts ONNX → NCNN (.param + .bin), specializing the input shape to
# [1,3,48,320]. NCNN loads models from files at runtime (PHONEFAST_NCNN_PARAM/BIN
# env vars), so the model is NOT embedded — it lives in tests/ocr-models/ncnn/.
build_model() {
    log "Building NCNN rec model from PP-OCR v3 ONNX..."
    [ -s "$SRC_ONNX" ] || err "Source ONNX not found: $SRC_ONNX\nRun: bash scripts/download-ocr-models.sh --models"

    # pnnx ships in the pip `pnnx` package.
    export PATH="$HOME/Library/Python/3.13/bin:$PATH"
    command -v pnnx >/dev/null 2>&1 || err "pnnx not found. Install: pip3 install --user pnnx"

    mkdir -p "$MODEL_DIR"
    local tmp_onnx="$MODEL_DIR/rec.onnx"
    cp "$SRC_ONNX" "$tmp_onnx"

    # Shape-specialize to [1,3,48,320] (PP-OCR rec ships dynamic; NCNN needs static).
    ( cd "$MODEL_DIR" && pnnx rec.onnx inputshape="[1,3,48,320]f32" >/dev/null )

    # Keep only the NCNN model; drop pnnx intermediates + the ONNX copy.
    ( cd "$MODEL_DIR" && rm -f rec.onnx rec.pnnx.param rec.pnnx.bin rec.pnnx.py rec.pnnx.onnx rec.pnnxsim.onnx )

    [ -s "$MODEL_DIR/rec.ncnn.param" ] && [ -s "$MODEL_DIR/rec.ncnn.bin" ] \
        || err "pnnx conversion failed: rec.ncnn.param/bin missing."

    log "  param: $MODEL_DIR/rec.ncnn.param ($(du -h "$MODEL_DIR/rec.ncnn.param" | cut -f1))"
    log "  bin:   $MODEL_DIR/rec.ncnn.bin ($(du -h "$MODEL_DIR/rec.ncnn.bin" | cut -f1))"
}

# ── main ────────────────────────────────────────────────────────
$do_lib && install_lib
$do_model && build_model

log "NCNN engine ready (macOS). Build & run:"
echo "  CGO_ENABLED=1 go build -tags ncnn ./cmd/phonefast/"
echo "  PHONEFAST_OCR_ENGINE=ncnn \\"
echo "  PHONEFAST_NCNN_PARAM=$MODEL_DIR/rec.ncnn.param \\"
echo "  PHONEFAST_NCNN_BIN=$MODEL_DIR/rec.ncnn.bin \\"
echo "  phonefast daemon --foreground"
