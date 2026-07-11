#!/usr/bin/env bash
#
# download-ffmpeg.sh — 下载 FFmpeg 源码并编译为静态库
#
# 使用 FFmpeg 官方开源地址下载源码，然后调用 cross-build-ffmpeg.sh 编译。
# 产物 (~15-25MB):
#   build/cross-ffmpeg/<target>/lib/libavcodec.a  (+ libavformat libswscale libavutil)
#   build/cross-ffmpeg/<target>/include/
#   build/cross-ffmpeg/<target>/lib/pkgconfig/
#
# 用法:
#   bash scripts/download-ffmpeg.sh                     # 当前平台 (aarch64-darwin)
#   bash scripts/download-ffmpeg.sh aarch64-darwin       # 指定目标
#   bash scripts/download-ffmpeg.sh x86_64-linux-gnu
#   bash scripts/download-ffmpeg.sh --all                 # 所有平台
#   bash scripts/download-ffmpeg.sh --force               # 强制重新编译
#
# 目标列表:
#   aarch64-darwin     x86_64-darwin
#   x86_64-linux-gnu   aarch64-linux-gnu
#   x86_64-windows-gnu

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR="$ROOT_DIR/build/cross-ffmpeg"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}[+]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[x]${NC} $*"; }

ALL_TARGETS=(
    "aarch64-darwin"
    "x86_64-darwin"
    "x86_64-linux-gnu"
    "aarch64-linux-gnu"
    "x86_64-windows-gnu"
)

# ── 工具检测 ─────────────

detect_native_target() {
    local os arch
    os="$(go env GOOS 2>/dev/null || uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(go env GOARCH 2>/dev/null || uname -m)"
    case "$arch" in
        arm64|aarch64) arch="aarch64" ;;
        x86_64|amd64)  arch="x86_64" ;;
    esac
    case "$os" in
        darwin)  echo "${arch}-darwin" ;;
        linux)   echo "${arch}-linux-gnu" ;;
        mingw*|msys*|cygwin*) echo "x86_64-windows-gnu" ;;
        *)       echo "${arch}-${os}" ;;
    esac
}

is_installed() {
    local target="$1"
    [ -f "$OUTPUT_DIR/$target/lib/libavcodec.a" ] && \
    [ -f "$OUTPUT_DIR/$target/lib/pkgconfig/libavcodec.pc" ]
}

# ── 使用开源 FFmpeg 源码编译 ──

build_from_source() {
    local target="$1" force="${2:-false}"

    if ! $force && is_installed "$target"; then
        info "✓ ${target} (已存在，跳过)"
        return 0
    fi

    info "从 FFmpeg 官方源码编译 (${target})..."
    info "  源码: https://github.com/FFmpeg/FFmpeg"
    if [ -f "$SCRIPT_DIR/cross-build-ffmpeg.sh" ]; then
        bash "$SCRIPT_DIR/cross-build-ffmpeg.sh" "$target"
    else
        error "未找到 scripts/cross-build-ffmpeg.sh"
        return 1
    fi
}

# ── Main ──

main() {
    local targets=() force=false

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --all)   targets=("${ALL_TARGETS[@]}") ;;
            --force) force=true ;;
            *)       targets+=("$1") ;;
        esac
        shift
    done

    if [ ${#targets[@]} -eq 0 ]; then
        targets=("$(detect_native_target)")
        info "检测到当前平台: ${targets[0]}"
    fi

    local failed=0
    for t in "${targets[@]}"; do
        echo ""
        if build_from_source "$t" "$force"; then
            : # ok
        else
            ((failed++))
        fi
    done

    echo ""
    if [ $failed -eq 0 ]; then
        info "全部就绪 — 现在可以构建 phonefast:"
        echo "   bash scripts/build.sh"
    else
        error "${failed} 个目标失败"
    fi
}

main "$@"
