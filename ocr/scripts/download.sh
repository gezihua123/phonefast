#!/usr/bin/env bash
#
# download.sh — download OCR models and ONNX Runtime lib into ocr/assets/
#
# Usage:
#   bash ocr/scripts/download.sh models            # PP-OCRv6 ONNX models only
#   bash ocr/scripts/download.sh lib               # ONNX Runtime lib (current platform)
#   bash ocr/scripts/download.sh keys              # character set (ppocr_keys.txt)
#   bash ocr/scripts/download.sh all               # models + lib + keys
#
# Sources:
#   models: HuggingFace PaddlePaddle (primary)
#   keys:   extracted from v6 model inference.yml
#   lib:    system install (brew/apt) → GitHub release (cross-platform)
#
# Environment:
#   ORT_VERSION    ONNX Runtime version (default: 1.27.1)
#   OCR_MODEL      Model level: medium (default), small, tiny

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ASSETS_DIR="$(cd "$SCRIPT_DIR/../assets" && pwd)"
KEYS_DIR="$(cd "$SCRIPT_DIR/../common" && pwd)"
ORT_VERSION="${ORT_VERSION:-1.27.1}"
OCR_MODEL="${OCR_MODEL:-medium}"

# ── Helpers ──────────────────────────────────────────────────────

info()  { echo -e "\033[0;32m[+]\033[0m $*"; }
warn()  { echo -e "\033[1;33m[!]\033[0m $*"; }
err()   { echo -e "\033[0;31m[x]\033[0m $*" >&2; exit 1; }

download() {
    local url="$1" dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 3 --connect-timeout 10 "$url" -o "$dest"
    elif command -v wget >/dev/null 2>&1; then
        wget -q --retry-connrefused --timeout=10 "$url" -O "$dest"
    else
        err "Neither curl nor wget found."
    fi
}

# ── Models (PP-OCRv6) ────────────────────────────────────────────

download_models() {
    local det="$ASSETS_DIR/ppocr-det.onnx"
    local rec="$ASSETS_DIR/ppocr-rec.onnx"
    local got_det=true got_rec=true

    [ -s "$det" ] || got_det=false
    [ -s "$rec" ] || got_rec=false
    if $got_det && $got_rec; then
        info "ppocr-det.onnx + ppocr-rec.onnx already present"
        return
    fi

    # Map model level to HuggingFace repo suffix
    local level="$OCR_MODEL"
    case "$level" in
        medium) ;;
        small)  ;;
        tiny)   ;;
        *)      warn "Unknown OCR_MODEL=$level, falling back to medium"; level="medium" ;;
    esac

    local hf="https://huggingface.co/PaddlePaddle/PP-OCRv6_${level}_det_onnx/resolve/main/inference.onnx"
    local hf_rec="https://huggingface.co/PaddlePaddle/PP-OCRv6_${level}_rec_onnx/resolve/main/inference.onnx"

    info "Downloading PP-OCRv6 ${level} models..."
    info "  det: $hf"
    info "  rec: $hf_rec"

    if ! $got_det; then
        if download "$hf" "$det"; then
            info "det: $(du -sh "$det" | cut -f1)"
            got_det=true
        fi
    fi
    if ! $got_rec; then
        if download "$hf_rec" "$rec"; then
            info "rec: $(du -sh "$rec" | cut -f1)"
            got_rec=true
        fi
    fi

    $got_det && $got_rec || warn "Some models missing. OCR will return ErrNotAvailable."
}

# ── Keys (character set from v6 model config) ────────────────────

download_keys() {
    local keys_file="$KEYS_DIR/ppocr_keys.txt"
    local yml_url="https://huggingface.co/PaddlePaddle/PP-OCRv6_medium_rec_onnx/resolve/main/inference.yml"

    info "Downloading PP-OCRv6 character set from inference.yml..."

    local tmpdir
    tmpdir="$(mktemp -d)"
    trap "rm -rf $tmpdir" EXIT

    download "$yml_url" "$tmpdir/inference.yml" || err "Failed to download inference.yml"

    python3 -c "
import yaml, sys
with open('$tmpdir/inference.yml') as f:
    cfg = yaml.safe_load(f)
chars = cfg['PostProcess']['character_dict']
with open('$keys_file', 'w') as f:
    for c in chars:
        f.write(c + '\n')
print(f'Extracted {len(chars)} characters -> $keys_file')
" || err "Failed to extract character set"
}

# ── ORT lib ──────────────────────────────────────────────────────

download_lib() {
    local name
    case "$(uname -s)" in
        Darwin)  name="libonnxruntime.dylib" ;;
        Linux)   name="libonnxruntime.so" ;;
        MINGW*|MSYS*) name="onnxruntime.dll" ;;
        *)       err "Unsupported platform: $(uname -s)" ;;
    esac

    local dest="$ASSETS_DIR/libonnxruntime-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m).dylib"
    [ -s "$dest" ] && { info "$(basename "$dest") already present"; return; }

    # Source 1: system install
    case "$(uname -s)" in
        Darwin)
            for p in /opt/homebrew/lib/$name /usr/local/lib/$name; do
                if [ -f "$p" ]; then
                    info "Found system ORT: $p"
                    cp "$p" "$dest" && return
                fi
            done ;;
        Linux)
            for p in /usr/local/lib/$name /usr/lib/$name; do
                if [ -f "$p" ]; then
                    info "Found system ORT: $p"
                    cp "$p" "$dest" && return
                fi
            done ;;
    esac

    # Source 2: GitHub release
    local suffix
    case "$(uname -s)/$(uname -m)" in
        Darwin/arm64) suffix="osx-arm64" ;;
        Darwin/x86_64) suffix="osx-x86_64" ;;
        Linux/x86_64)  suffix="linux-x64" ;;
        Linux/aarch64) suffix="linux-aarch64" ;;
        *)             err "No ORT release for $(uname -s)/$(uname -m)" ;;
    esac

    local pkg="onnxruntime-${suffix}-${ORT_VERSION}"
    local url="https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${pkg}.tgz"
    local tmpdir
    tmpdir="$(mktemp -d)"
    trap "rm -rf $tmpdir" EXIT

    info "Downloading ONNX Runtime ${ORT_VERSION} ($suffix)..."
    download "$url" "$tmpdir/pkg.tgz"
    tar xzf "$tmpdir/pkg.tgz" -C "$tmpdir"
    cp "$tmpdir/$pkg/lib/$name" "$dest"
    info "$(basename "$dest"): $(du -sh "$dest" | cut -f1)"
}

# ── Main ─────────────────────────────────────────────────────────

case "${1:-}" in
    models)
        mkdir -p "$ASSETS_DIR"
        download_models
        ;;
    lib)
        mkdir -p "$ASSETS_DIR"
        download_lib
        ;;
    keys)
        download_keys
        ;;
    all)
        mkdir -p "$ASSETS_DIR"
        download_models
        download_lib
        download_keys
        ;;
    *)
        echo "Usage: $0 {models|lib|keys|all}"
        echo "  models   PP-OCRv6 ONNX models (det + rec) -> ocr/assets/"
        echo "  lib      ONNX Runtime native lib -> ocr/assets/"
        echo "  keys     PP-OCRv6 character set -> ocr/common/ppocr_keys.txt"
        echo "  all      All of the above"
        echo ""
        echo "Environment:"
        echo "  OCR_MODEL   Model level: medium (default), small, tiny"
        exit 1
        ;;
esac
