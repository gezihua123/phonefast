#!/usr/bin/env bash
#
# download.sh — download OCR models and ONNX Runtime lib into ocr/assets/
#
# Usage:
#   bash ocr/scripts/download.sh models            # PP-OCR v3 ONNX models only
#   bash ocr/scripts/download.sh lib               # ONNX Runtime lib (current platform)
#   bash ocr/scripts/download.sh all               # models + lib
#
# Sources:
#   models: HuggingFace RapidOCR (primary) → pip rapidocr_onnxruntime (fallback)
#   lib:    system install (brew/apt) → GitHub release (cross-platform)
#
# Environment:
#   ORT_VERSION    ONNX Runtime version (default: 1.27.1)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ASSETS_DIR="$(cd "$SCRIPT_DIR/../assets" && pwd)"
ORT_VERSION="${ORT_VERSION:-1.27.1}"

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

# ── Models ───────────────────────────────────────────────────────

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

    info "Downloading PP-OCR v3 models..."

    # Source 1: HuggingFace
    local hf="https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv3"
    if ! $got_det; then
        download "$hf/ch_PP-OCRv3_det_infer.onnx" "$det" && info "det: $(du -sh "$det" | cut -f1)" || got_det=false
    fi
    if ! $got_rec; then
        download "$hf/ch_PP-OCRv3_rec_infer.onnx" "$rec" && info "rec: $(du -sh "$rec" | cut -f1)" || got_rec=false
    fi

    # Source 2: pip rapidocr_onnxruntime (offline fallback)
    if ! $got_det; then
        local site
        site="$(python3 -c 'import site; print(site.getsitepackages()[0])' 2>/dev/null || true)"
        if [ -n "$site" ] && [ -d "$site/rapidocr_onnxruntime/models" ]; then
            local src="$site/rapidocr_onnxruntime/models"
            [ -f "$src/ch_PP-OCRv3_det_infer.onnx" ] && cp "$src/ch_PP-OCRv3_det_infer.onnx" "$det" && got_det=true
            [ -f "$src/ch_PP-OCRv3_rec_infer.onnx" ] && cp "$src/ch_PP-OCRv3_rec_infer.onnx" "$rec" && got_rec=true
        fi
    fi

    $got_det && $got_rec || warn "Some models missing. OCR will return ErrNotAvailable."
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
    all)
        mkdir -p "$ASSETS_DIR"
        download_models
        download_lib
        ;;
    *)
        echo "Usage: $0 {models|lib|all}"
        echo "  models   PP-OCR v3 ONNX models (det + rec) -> ocr/assets/"
        echo "  lib      ONNX Runtime native lib -> ocr/assets/"
        echo "  all      Both"
        exit 1
        ;;
esac
