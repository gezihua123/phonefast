#!/usr/bin/env bash
#
# phonefast 打包脚本
#
# 用法:
#   bash scripts/build.sh                    # 当前平台构建
#   bash scripts/build.sh --all              # 全平台构建 (macOS/Linux/Windows)
#   bash scripts/build.sh --linux            # Linux x86_64
#   bash scripts/build.sh --macos            # macOS arm64 + x86_64
#   bash scripts/build.sh --windows          # Windows x86_64
#   bash scripts/build.sh --version 1.0.0    # 指定版本号
#   bash scripts/build.sh --clean            # 构建前清理 dist/
#
# 产物目录: dist/dev/
#   ├── phonefast-<os>-<arch>[/.exe]  # CLI 二进制 (jar 已 embed 编译进二进制)
#   ├── README.md
#   └── docs/
#
# 单文件分发: jar 已通过 Go embed 编译进二进制，无需额外文件即可运行。

set -euo pipefail

# ── 配置 ──────────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ANDROID_DIR="$ROOT_DIR/android"
JAR_SRC="$ANDROID_DIR/scrcpy-server.jar"

# 版本号: git tag → VERSION 环境变量 → "dev"
VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
    VERSION="$(git -C "$ROOT_DIR" describe --tags --abbrev=0 2>/dev/null || true)"
fi
if [ -z "$VERSION" ]; then
    VERSION="dev"
fi

# Go 版本和构建信息
GO_VERSION="$(go version | awk '{print $3}' | sed 's/^go//')"
BUILD_TIME="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
GIT_COMMIT="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")"

# 产物输出目录
DIST_BASE="$ROOT_DIR/dist/dev"

# ── 颜色 ──────────────────────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}[+]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[x]${NC} $*"; exit 1; }

# ── 前置检查 ──────────────────────────────────────────────────────────────────────

check_prereqs() {
    command -v go >/dev/null 2>&1 || error "需要安装 Go 工具链"
}

# sync_assets ensures assets/scrcpy-server.jar exists for Go embed.
# Copies from android/ if available; errors if neither source exists.
sync_assets() {
    local assets_dir="$ROOT_DIR/assets"
    local assets_jar="$assets_dir/scrcpy-server.jar"
    local assets_ver="$assets_dir/scrcpy-server.version"

    mkdir -p "$assets_dir"

    if [ -f "$assets_jar" ]; then
        info "assets/scrcpy-server.jar 已就绪"
        return
    fi

    if [ -f "$JAR_SRC" ]; then
        info "同步 jar → assets/"
        cp "$JAR_SRC" "$assets_jar"
        cp "$ANDROID_DIR/scrcpy-server.version" "$assets_ver"
    else
        error "assets/scrcpy-server.jar 与 android/scrcpy-server.jar 均不存在\n请先运行: bash scripts/build-server.sh"
    fi
}

# ── 构建单个平台 ──────────────────────────────────────────────────────────────────

build_target() {
    local os="$1" arch="$2" ext="$3"
    local bin_name="phonefast-${os}-${arch}${ext}"
    local dist_dir="$DIST_BASE"

    info "构建 ${os}/${arch} ..."
    mkdir -p "$dist_dir"

    CGO_ENABLED=0 \
    GOOS="$os" \
    GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$dist_dir/$bin_name" "$ROOT_DIR/cmd/phonefast/"

    # 复制文档
    mkdir -p "$dist_dir/docs"
    cp "$ROOT_DIR/README.md" "$dist_dir/"

    # 文档文件不存在时跳过（不阻断构建）
    for doc in promotional-copy.md phonefast-vs-phonemcp.md screenshot-mcp-image-content.md; do
        [ -f "$ROOT_DIR/docs/$doc" ] && cp "$ROOT_DIR/docs/$doc" "$dist_dir/docs/"
    done

    # 压缩 (可选)
    if command -v upx >/dev/null 2>&1; then
        upx -q "$dist_dir/$bin_name" 2>/dev/null || true
    fi

    # 计算产物大小
    local bin_size
    bin_size=$(du -h "$dist_dir/$bin_name" | cut -f1)
    info "  ${os}/${arch} 完成: bin=${bin_size}  →  ${dist_dir}"
}

# ── 生成发布包 ────────────────────────────────────────────────────────────────────

make_archive() {
    local os="$1" arch="$2"
    local archive_name="phonefast-${VERSION}-${os}-${arch}"
    local bin_name="phonefast-${os}-${arch}"

    info "打包 ${os}/${arch} ..."
    cd "$DIST_BASE"

    case "$os" in
        darwin|linux)
            tar -czf "${archive_name}.tar.gz" "$bin_name" README.md docs
            info "  → ${archive_name}.tar.gz  (单文件部署: 解压后直接运行 phonefast-*)"
            ;;
        windows)
            tar -czf "${archive_name}.tar.gz" "${bin_name}.exe" README.md docs
            info "  → ${archive_name}.tar.gz  (单文件部署: 解压后直接运行 phonefast-*)"
            ;;
    esac
    cd "$ROOT_DIR"
}

# ── 所有平台定义 ──────────────────────────────────────────────────────────────────

PLATFORMS=(
    "darwin  amd64"
    "darwin  arm64"
    "linux   amd64"
    "linux   arm64"
    "windows amd64"
)

build_platforms() {
    local filter="${1:-}"
    local cur_os cur_arch
    cur_os="$(go env GOOS)"
    cur_arch="$(go env GOARCH)"

    for plat in "${PLATFORMS[@]}"; do
        read -r os arch <<< "$plat"
        local ext=""
        if [ "$os" = "windows" ]; then ext=".exe"; fi

        local should_build=false
        case "$filter" in
            all)     should_build=true ;;
            macos)   [ "$os" = "darwin" ]  && should_build=true ;;
            linux)   [ "$os" = "linux" ]   && should_build=true ;;
            windows) [ "$os" = "windows" ] && should_build=true ;;
            "")      [ "$os" = "$cur_os" ] && [ "$arch" = "$cur_arch" ] && should_build=true ;;
        esac

        if $should_build; then
            build_target "$os" "$arch" "$ext"
            if [ "$filter" = "all" ] || [ "$filter" = "macos" ] || [ "$filter" = "linux" ] || [ "$filter" = "windows" ]; then
                make_archive "$os" "$arch"
            fi
        fi
    done
}

# ── 产物清单 ──────────────────────────────────────────────────────────────────────

print_summary() {
    echo ""
    echo "══════════════════════════════════════════════════════════════════════════"
    echo "  phonefast ${VERSION}  构建完成"
    echo "══════════════════════════════════════════════════════════════════════════"
    echo "  Go 版本:    ${GO_VERSION}"
    echo "  Git commit: ${GIT_COMMIT}"
    echo "  构建时间:   ${BUILD_TIME}"
    echo "  产物目录:   ${DIST_BASE}/"
    echo ""
    find "$DIST_BASE" -type f \
        -exec ls -lh {} \; 2>/dev/null || true
    echo ""
    echo "  部署: 单文件 — 直接运行 phonefast-* 即可（jar 已内嵌）"
    echo "══════════════════════════════════════════════════════════════════════════"
}

# ── Main ───────────────────────────────────────────────────────────────────────────

main() {
    check_prereqs
    sync_assets

    local filter=""
    local clean_first=false
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --all)     filter="all"; shift ;;
            --macos)   filter="macos"; shift ;;
            --linux)   filter="linux"; shift ;;
            --windows) filter="windows"; shift ;;
            --version) VERSION="$2"; shift 2 ;;
            --clean)   clean_first=true; shift ;;
            *)         shift ;;
        esac
    done

    if $clean_first; then
        info "清理构建目录: ${DIST_BASE}"
        rm -rf "$DIST_BASE"
    fi

    info "phonefast ${VERSION}  构建开始"
    info "目标: ${filter:-当前平台 ($(go env GOOS)/$(go env GOARCH))}"
    echo ""

    # LDFLAGS 在此处构造，确保 --version 覆盖的 VERSION 生效。
    # 单行格式，避免多行字符串导致 -X 参数解析错位。
    LDFLAGS="-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}"

    build_platforms "$filter"
    print_summary
}

main "$@"
