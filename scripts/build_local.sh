#!/usr/bin/env bash
#
# build_local.sh — 本地全平台构建 (方案2: Mac + zig 交叉编译)
#
# 在一台 macOS (arm64) 上, 用本机 clang + zig cc 编出全部 4 个平台二进制:
#   darwin-arm64  → 本机 clang (原生)
#   仅 darwin-arm64 (macOS Intel 不再支持)
#   linux-amd64   → zig cc 交叉
#   linux-arm64   → zig cc 交叉
#   windows-amd64 → zig cc 交叉
# FFmpeg 静态链接进每个二进制 (asm-on, 需 nasm), 单文件分发。
#
# 与 CI (方案3, .github/workflows/release.yml) 的区别:
#   - 本脚本: 本地 zig 交叉, 快, 用于开发期出包验证
#   - CI:     每平台原生 runner, 最稳, 用于正式 release
# 两者产物可交叉校验。
#
# 用法:
#   bash scripts/build_local.sh           # 全平台 (4 目标)
#   bash scripts/build_local.sh --macos   # 仅 darwin
#   bash scripts/build_local.sh --linux   # 仅 linux
#   bash scripts/build_local.sh --windows # 仅 windows
#   bash scripts/build_local.sh --clean   # 构建前清理 dist/
#
# 产物: dist/dev/phonefast-<os>-<arch>[.exe] + .tar.gz

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$ROOT_DIR"

echo "════════════════════════════════════════════════════"
echo "  phonefast 本地全平台构建 (方案2: zig 交叉)"
echo "════════════════════════════════════════════════════"
echo ""

# ── 1. 环境检查 ──
echo "[1/3] 检查构建环境..."
bash "$SCRIPT_DIR/build_env.sh" check
echo ""

# ── 2. 编静态 FFmpeg 库 (按目标, 缺则编) ──
echo "[2/3] 确保静态 FFmpeg 库就绪..."

# 解析要构建哪些目标
FILTER="all"
CLEAN=false
for arg in "$@"; do
    case "$arg" in
        --macos)   FILTER="macos" ;;
        --linux)   FILTER="linux" ;;
        --windows) FILTER="windows" ;;
        --clean)   CLEAN=true ;;
    esac
done

# 目标 → cross-build-ffmpeg.sh 的 target 名
targets_for_filter() {
    case "$1" in
        macos)   echo "aarch64-darwin" ;;
        linux)   echo "x86_64-linux-gnu aarch64-linux-gnu" ;;
        windows) echo "x86_64-windows-gnu" ;;
        all)     echo "aarch64-darwin x86_64-linux-gnu aarch64-linux-gnu x86_64-windows-gnu" ;;
    esac
}

for t in $(targets_for_filter "$FILTER"); do
    if [ ! -f "build/cross-ffmpeg/$t/lib/libavcodec.a" ]; then
        echo "  编译 FFmpeg: $t"
        bash "$SCRIPT_DIR/cross-build-ffmpeg.sh" "$t" >/dev/null 2>&1 || {
            echo "  ✗ FFmpeg 编译失败: $t"
            echo "    手动排查: bash scripts/cross-build-ffmpeg.sh $t"
            exit 1
        }
        echo "  ✓ $t"
    else
        echo "  ✓ $t (已存在, 跳过)"
    fi
done
echo ""

# ── 3. 构建 Go 二进制 ──
echo "[3/3] 构建 phonefast 二进制..."
BUILD_ARGS=""
$CLEAN && BUILD_ARGS="$BUILD_ARGS --clean"
case "$FILTER" in
    all)     BUILD_ARGS="$BUILD_ARGS --all" ;;
    macos)   BUILD_ARGS="$BUILD_ARGS --macos" ;;
    linux)   BUILD_ARGS="$BUILD_ARGS --linux" ;;
    windows) BUILD_ARGS="$BUILD_ARGS --windows" ;;
esac

bash "$SCRIPT_DIR/build.sh" $BUILD_ARGS
