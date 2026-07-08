#!/usr/bin/env bash
#
# cross-build-ffmpeg.sh — 使用 zig cc 交叉编译 FFmpeg 动态库
#
# 用法:
#   bash scripts/cross-build-ffmpeg.sh x86_64-linux-gnu
#   bash scripts/cross-build-ffmpeg.sh aarch64-linux-gnu
#   bash scripts/cross-build-ffmpeg.sh x86_64-windows-gnu
#   bash scripts/cross-build-ffmpeg.sh x86_64-darwin
#
# 产物:
#   ./cross-ffmpeg/<target>/lib/libavcodec.so / .dylib
#   ./cross-ffmpeg/<target>/include/
#   ./cross-ffmpeg/<target>/lib/pkgconfig/

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR="$ROOT_DIR/cross-ffmpeg"

# ── 目标 → zig target triple ──

target_to_zig() {
    case "$1" in
        x86_64-linux-gnu)    echo "x86_64-linux-gnu" ;;
        aarch64-linux-gnu)   echo "aarch64-linux-gnu" ;;
        x86_64-windows-gnu)  echo "x86_64-windows-gnu" ;;
        x86_64-darwin)       echo "x86_64-macos-none" ;;
        aarch64-darwin)      echo "aarch64-macos-none" ;;
        *)                   echo "unknown" ;;
    esac
}

FFMPEG_VERSION="7.1"
# GitHub mirror（比 ffmpeg.org 直连快）
FFMPEG_URL="https://github.com/FFmpeg/FFmpeg/archive/refs/tags/n${FFMPEG_VERSION}.tar.gz"

main() {
    if [ $# -lt 1 ]; then
        echo "用法: $0 <target-triple>"
        exit 1
    fi

    local target="$1"
    local zig_target
    zig_target=$(target_to_zig "$target")
    if [ "$zig_target" = "unknown" ]; then
        echo "不支持的目标: $target"
        exit 1
    fi

    local install_dir="$OUTPUT_DIR/$target"
    local src_dir="$OUTPUT_DIR/src"

    echo "[+] 目标:    $target"
    echo "[+] zig:     $zig_target"
    echo "[+] 安装到:  $install_dir"
    echo ""

    command -v zig >/dev/null 2>&1 || { echo "需要安装 zig: brew install zig"; exit 1; }

    # ── 下载 FFmpeg 源码 ──
    mkdir -p "$src_dir"
    local archive_name="FFmpeg-n${FFMPEG_VERSION}.tar.gz"

    if [ ! -f "$src_dir/$archive_name" ]; then
        echo "[+] 下载 FFmpeg ${FFMPEG_VERSION} ..."
        curl -L "$FFMPEG_URL" -o "$src_dir/$archive_name"
    fi

    local src_subdir="FFmpeg-n${FFMPEG_VERSION}"
    if [ ! -d "$src_dir/$src_subdir" ]; then
        echo "[+] 解压 ..."
        tar xf "$src_dir/$archive_name" -C "$src_dir"
    fi

    local build_dir="$src_dir/build-${target}"
    rm -rf "$build_dir"
    mkdir -p "$build_dir"

    # ── 配置 FFmpeg ──
    echo "[+] 配置 FFmpeg (--target=$target) ..."
    cd "$build_dir"

    local extra_flags=""
    case "$target" in
        *-linux-*)
            extra_flags="--target-os=linux --enable-pic --disable-asm"
            ;;
        *-windows-*)
            extra_flags="--target-os=mingw32 --enable-w32threads"
            ;;
        *-darwin)
            extra_flags="--target-os=darwin"
            ;;
    esac

    CC="zig cc -target $zig_target" \
    CXX="zig c++ -target $zig_target" \
    AR="zig ar" \
    RANLIB="zig ranlib" \
    "$src_dir/$src_subdir/configure" \
        --prefix="$install_dir" \
        --cc="zig cc -target $zig_target" \
        --cxx="zig c++ -target $zig_target" \
        --ar="zig ar" \
        --ranlib="zig ranlib" \
        --enable-cross-compile \
        --disable-programs \
        --disable-doc \
        --disable-debug \
        --disable-everything \
        --enable-decoder=h264 \
        --enable-parser=h264 \
        --enable-demuxer=h264 \
        --enable-protocol=file \
        $extra_flags \
        --extra-cflags="-fPIC" \
        --pkg-config=/usr/bin/false

    # ── 编译 ──
    echo "[+] 编译 FFmpeg ..."
    make -j"$(nproc 2>/dev/null || sysctl -n hw.logicalcpu 2>/dev/null || echo 4)" 2>&1 | tail -5

    # ── 安装 ──
    echo "[+] 安装到 $install_dir ..."
    make install

    echo ""
    echo "[+] 编译完成:"
    ls -lh "$install_dir/lib/libavcodec"* 2>/dev/null || true
    ls -lh "$install_dir/lib/libswscale"* 2>/dev/null || true

    echo ""
    echo "使用方式:"
    echo "  export PKG_CONFIG_PATH=\"$install_dir/lib/pkgconfig\""
    echo "  export CC=\"zig cc -target $zig_target\""
    echo "  CGO_ENABLED=1 go build ./cmd/phonefast/"
}

main "$@"
