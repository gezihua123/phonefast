#!/usr/bin/env bash
#
# phonefast 源码打包脚本
#
# 将项目源码 (除 .git 和构建产物以外) 打包为 .zip 归档。
#
# 用法:
#   bash scripts/zip.sh              # 打包源码到当前目录
#   bash scripts/zip.sh -o /tmp      # 指定输出目录
#   bash scripts/zip.sh --all        # 包含构建产物 (dist/, build/)

set -euo pipefail

# ── 颜色 ──────────────────────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${GREEN}[✓]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[✗]${NC} $*"; exit 1; }
step()  { echo -e "${CYAN}──${NC} $*"; }

# ── 参数 ──────────────────────────────────────────────────────────────────────────

OUT_DIR="."
INCLUDE_ALL=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        -o) OUT_DIR="$2"; shift 2 ;;
        --all) INCLUDE_ALL=true; shift ;;
        -h) echo "用法: $0 [-o <输出目录>] [--all]"; exit 0 ;;
        *) error "未知参数: $1" ;;
    esac
done

# ── 路径 ──────────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# 项目名 + 分支名 + 日期构成文件名
PROJECT_NAME="phonefast"
BRANCH=$(git -C "$ROOT_DIR" rev-parse --abbrev-ref HEAD)
DATE=$(date +%Y%m%d-%H%M%S)
if $INCLUDE_ALL; then
    ZIP_NAME="${PROJECT_NAME}-${BRANCH}-${DATE}.zip"
else
    ZIP_NAME="${PROJECT_NAME}-src-${BRANCH}-${DATE}.zip"
fi
ZIP_PATH="$(cd "$OUT_DIR" 2>/dev/null && pwd || echo "$OUT_DIR")/${ZIP_NAME}"

# ── 检查 ──────────────────────────────────────────────────────────────────────────

command -v zip >/dev/null 2>&1 || error "需要 zip 命令"

mkdir -p "$OUT_DIR" 2>/dev/null || error "无法创建输出目录: $OUT_DIR"

# ── 打包 ──────────────────────────────────────────────────────────────────────────

if $INCLUDE_ALL; then
    step "打包项目 (排除 .git) ..."
    cd "$ROOT_DIR"
    zip -r "${ZIP_PATH}" . \
        -x '.git/*' \
        -x '.git' \
        -x '*.zip'
else
    step "打包源码 (排除 .git / 构建产物) ..."
    cd "$ROOT_DIR"
    zip -r "${ZIP_PATH}" . \
        -x '.git/*' \
        -x '.git' \
        -x '*.zip' \
        -x 'dist/*' \
        -x 'dist/' \
        -x 'bin/*' \
        -x 'bin/' \
        -x 'build/*' \
        -x 'build/' \
        -x 'out/*' \
        -x 'out/' \
        -x 'temp/*' \
        -x 'temp/' \
        -x 'tmp/*' \
        -x 'tmp/' \
        -x 'logs/*' \
        -x 'logs/' \
        -x 'vendor/*' \
        -x 'vendor/' \
        -x 'node_modules/*' \
        -x 'node_modules/' \
        -x '.build-tmp/*' \
        -x '.build-tmp' \
        -x '.DS_Store' \
        -x '*.exe' \
        -x '*.dll' \
        -x '*.so' \
        -x '*.dylib' \
        -x '*.test' \
        -x '*.out' \
        -x '*.log' \
        -x '*.pid' \
        -x '*.pid.lock' \
        -x '*.coverprofile'
fi

info "打包完成: ${ZIP_PATH}"

# 显示大小
if command -v du >/dev/null 2>&1; then
    SIZE=$(du -h "$ZIP_PATH" | cut -f1)
    info "文件大小: ${SIZE}"
fi

# 列出内容
echo ""
step "文件列表:"
unzip -l "$ZIP_PATH" | sed '1,3d;$d' || true
