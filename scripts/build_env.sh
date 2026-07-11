#!/usr/bin/env bash
#
# build_env.sh — phonefast 构建环境依赖的检测 / 安装 / 判定
#
# phonefast 静态链接 FFmpeg 到 Go CGO 二进制，需要一套交叉编译工具链。
# 本脚本是统一的"环境就绪"入口：检测工具是否齐备、缺什么装什么、
# 并以退出码 / 文本报告判定结果，供其它脚本和 CI 复用。
#
# 工具链分工:
#   nasm  — FFmpeg x86 asm 汇编器 (SSE/AVX/AVX2/AVX-512)。linux-x86_64 / windows 必需。
#            缺失则 zig 分支降级为 --disable-asm (纯 C 解码，慢 2-4×)。
#   zig   — Mac 上交叉编译 linux/windows 的 CC (自带 sysroot)。darwin 不需要。
#   clang — darwin 交叉编译的 CC (zig 找不到 macOS SDK)。Mac 自带。
#   go    — Go 工具链，所有构建必需。
#
# 用法:
#   bash scripts/build_env.sh            # 检测并报告 (缺则提示安装命令)
#   bash scripts/build_env.sh install    # 自动安装缺失依赖 (macOS 用 brew)
#   bash scripts/build_env.sh check      # 只检测，缺依赖时退出码 != 0
#
# 退出码:
#   0 — 所有必需依赖就绪
#   1 — 有必需依赖缺失

set -euo pipefail

# ── 颜色 ──
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
ok()   { echo -e "  ${GREEN}✓${NC} $*"; }
miss() { echo -e "  ${RED}✗${NC} $*"; }
info() { echo -e "${BLUE}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }

# ── 工具检测 ──
# 每个工具: 变量名, 显示名, 检测命令, 必需?, 安装方式(brew), 用途
have_nasm=0; have_zig=0; have_clang=0; have_go=0; have_brew=0

detect() {
    command -v nasm   >/dev/null 2>&1 && have_nasm=1
    command -v zig    >/dev/null 2>&1 && have_zig=1
    command -v clang  >/dev/null 2>&1 && have_clang=1
    command -v go     >/dev/null 2>&1 && have_go=1
    command -v brew   >/dev/null 2>&1 && have_brew=1
}

# ── 报告 ──
report() {
    echo ""
    echo "构建环境检测:"
    echo "────────────────────────────────────────────────────────────"
    [ "$have_go"     = 1 ] && ok "go     $(go env GOVERSION 2>/dev/null || echo '?')    [必需·所有构建]" || miss "go     [必需·缺失] → https://go.dev/dl/"
    [ "$have_nasm"   = 1 ] && ok "nasm   $(nasm --version 2>/dev/null | head -1)    [FFmpeg x86 asm·linux-x64/windows]" || miss "nasm   [缺失·asm 将降级关闭]"
    [ "$have_zig"    = 1 ] && ok "zig    $(zig version 2>/dev/null || echo '?')    [交叉编译 linux/windows·Mac]" || warn "zig    [缺失·Mac 交叉编译 linux/windows 需要] → brew install zig"
    [ "$have_clang"  = 1 ] && ok "clang  $(clang --version 2>/dev/null | head -1)    [darwin 交叉编译]" || miss "clang  [缺失·darwin 构建需要]"

    echo ""
    echo "asm 判定:"
    echo "────────────────────────────────────────────────────────────"
    if [ "$have_nasm" = 1 ]; then
        ok "nasm 就绪 → zig/gcc 分支将开启 x86 asm (SSE/AVX/AVX2/AVX-512)"
        ok "aarch64 NEON 走 zig 内置 assembler，无需 nasm"
    else
        warn "nasm 缺失 → FFmpeg 降级 --disable-asm (纯 C 解码，慢 2-4×)"
        warn "安装后重跑: bash scripts/build_env.sh install"
    fi
    echo ""
}

# ── 安装 ──
do_install() {
    info "安装缺失依赖..."
    if [ "$have_brew" != 1 ]; then
        warn "未检测到 brew，自动安装不可用。请手动安装:"
        echo "  macOS:  /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\""
        echo "  然后:   brew install nasm zig go"
        exit 1
    fi
    [ "$have_nasm"  != 1 ] && { info "brew install nasm";  brew install nasm  || warn "nasm 安装失败"; }
    [ "$have_zig"   != 1 ] && { info "brew install zig";   brew install zig   || warn "zig 安装失败"; }
    [ "$have_go"    != 1 ] && { info "brew install go";    brew install go    || warn "go 安装失败"; }
    # clang 在 macOS 随 Xcode CLT 自带，不通过 brew 装
    echo ""
    info "重新检测..."
    detect
    report
}

# ── 判定退出码 ──
# 必需: go。其余按管线需要，不阻断本脚本（具体构建脚本会自行判定降级）。
judge() {
    if [ "$have_go" != 1 ]; then
        echo ""
        miss "必需依赖 go 缺失，环境未就绪"
        exit 1
    fi
    if [ "$have_nasm" != 1 ]; then
        echo ""
        warn "环境可用，但 nasm 缺失 → FFmpeg 将 asm 降级 (建议安装以启用 SIMD)"
        exit 0
    fi
    echo ""
    ok "构建环境就绪"
    exit 0
}

# ── main ──
ACTION="${1:-report}"
detect
case "$ACTION" in
    check)  report; judge ;;
    install) do_install ;;
    report|*) report ;;
esac
