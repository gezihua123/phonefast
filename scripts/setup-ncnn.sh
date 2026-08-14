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
#   2. DATA        — the PP-OCRv6 medium rec ncnn model (rec.ncnn.param + .bin),
#                    downloaded pre-converted from HuggingFace.
#
# Both are pinned: brew ncnn version is whatever brew ships (currently 20260526);
# the model is the PP-OCRv6_medium_rec ncnn conversion from
# https://huggingface.co/qefro/pp-OCRv5-6-ncnn — a faithful ONNX→ncnn conversion
# with native ncnn LayerNorm + MultiHeadAttention layers (pnnx's own ONNX import
# leaves v6's LayerNorm/Linear as unconverted F.* ops, which ncnn rejects; this
# pre-converted model avoids that). The model has a dynamic Input blob and is fed
# each crop's natural width (capped at 640) — see ocr/ncnn/ncnn.go.
#
# Usage:
#   bash scripts/setup-ncnn.sh            # install lib + download model
#   bash scripts/setup-ncnn.sh --lib      # lib only (brew install ncnn)
#   bash scripts/setup-ncnn.sh --model    # model only (download from HF)
#
# After setup, build & run:
#   CGO_ENABLED=1 go build -tags ncnn ./cmd/phonefast/
#   PHONEFAST_OCR_ENGINE=ncnn \
#     PHONEFAST_NCNN_PARAM=<repo>/ocr/models/ncnn/rec.ncnn.param \
#     PHONEFAST_NCNN_BIN=<repo>/ocr/models/ncnn/rec.ncnn.bin \
#     phonefast daemon --foreground

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
MODEL_DIR="$PROJECT_ROOT/ocr/models/ncnn"
HF_BASE="https://huggingface.co/qefro/pp-OCRv5-6-ncnn/resolve/main"

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

# ── 2. DATA: download the PP-OCRv6 medium rec ncnn model ────────
# The model is a faithful ONNX→ncnn conversion of PP-OCRv6_medium_rec, hosted
# on HuggingFace. NCNN loads models from files at runtime
# (PHONEFAST_NCNN_PARAM/BIN env vars), so the model is NOT embedded — it lives
# in ocr/models/ncnn/. We download rather than convert locally because pnnx's
# ONNX importer leaves v6's LayerNorm/Linear as unconverted F.* ops (ncnn
# rejects them); the HF model was converted with a pnnx build that lowers them
# to native ncnn LayerNorm + MultiHeadAttention layers.
download_model() {
    log "Downloading PP-OCRv6 medium rec ncnn model from HuggingFace..."
    command -v curl >/dev/null 2>&1 || err "curl not found"

    mkdir -p "$MODEL_DIR"
    local param="$MODEL_DIR/rec.ncnn.param"
    local bin="$MODEL_DIR/rec.ncnn.bin"

    curl -fL -o "$param" "$HF_BASE/PP_OCRv6_medium_rec.param"
    curl -fL -o "$bin"   "$HF_BASE/PP_OCRv6_medium_rec.bin"

    [ -s "$param" ] && [ -s "$bin" ] \
        || err "download failed: rec.ncnn.param/bin missing."

    # Sanity: the .param must have zero unconverted F.*/aten:: ops (the pnnx
    # failure mode) and 18710 output classes (v6 medium: 18708 dict + blank + space).
    if grep -qE '^(F\.|aten::|prim::)' "$param"; then
        err "downloaded .param contains unconverted ops (F.*/aten::) — wrong model file."
    fi
    if ! grep -q '8=18710' "$param"; then
        err "downloaded .param missing Gemm 8=18710 (v6 medium output classes)."
    fi

    log "  param: $param ($(du -h "$param" | cut -f1))"
    log "  bin:   $bin ($(du -h "$bin" | cut -f1))"
    log "  dict:  reuses ocr/common/ppocr_keys.txt (18708 v6 chars; identical to HF)"
}

# ── main ────────────────────────────────────────────────────────
$do_lib && install_lib
$do_model && download_model

log "NCNN engine ready (macOS). Build & run:"
echo "  CGO_ENABLED=1 go build -tags ncnn ./cmd/phonefast/"
echo "  PHONEFAST_OCR_ENGINE=ncnn \\"
echo "  PHONEFAST_NCNN_PARAM=$MODEL_DIR/rec.ncnn.param \\"
echo "  PHONEFAST_NCNN_BIN=$MODEL_DIR/rec.ncnn.bin \\"
echo "  phonefast daemon --foreground"
