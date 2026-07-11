#!/usr/bin/env bash
#
# phonefast 发版脚本 (推送 tag 触发 CI 全平台构建)
#
# 本脚本不本地构建、不直接创建 Release。它只做:
#   版本号自增 → commit → 打 tag → push tag
# 推送 v* tag 后, GitHub Actions (.github/workflows/release.yml) 自动:
#   5 平台原生 runner 编译 → 聚合发布到 GitHub Release Assets
#
# 用法:
#   bash scripts/release.sh              # 自动版本自增 + 触发 CI
#   bash scripts/release.sh --dry-run    # 预览, 不打 tag 不触发 CI
#   bash scripts/release.sh 1.0.3        # 指定版本号
#   bash scripts/release.sh -y           # 跳过确认
#
# 前置依赖:
#   - git (必须, 推 tag 用)
#   - gh  (可选, 事后查看 CI/Release)
#
# 本地手动构建 (不发布) 用:
#   bash scripts/build_local.sh          # 本机 zig 全平台编译 (方案2)
#
# 流程:
#   ① 检查工作区干净
#   ② 版本号自增 + commit
#   ③ 创建 git tag v${VERSION}
#   ④ push tag → 触发 CI → CI 发布 Release

set -euo pipefail

# ── 颜色 ──────────────────────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${GREEN}[✓]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[✗]${NC} $*"; exit 1; }
step()  { echo -e "${CYAN}──${NC} $*"; }

# ── 配置 ──────────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# 从 install_pkg.sh 读取默认版本
DEFAULT_VERSION=$(grep -E 'VERSION:-' "$SCRIPT_DIR/install_pkg.sh" | sed 's/.*:-\([0-9.]*\).*/\1/' | head -1)
DEFAULT_VERSION="${DEFAULT_VERSION:-1.0.0}"

VERSION=""
DRY_RUN=false
FORCE=false

# ── 版本自增 ──────────────────────────────────────────────────────────────────────

bump_patch() {
    local ver="$1"
    echo "$ver" | awk -F. '{print $1"."$2"."$3+1}'
}

# 更新项目中所有版本号引用
update_version_files() {
    local old_ver="$1" new_ver="$2"

    step "版本号自增: ${old_ver} → ${new_ver}"

    # scripts/install_pkg.sh — 只替换版本号字段，不碰无关数字
    sed -i '' \
        -e "s/\(--version \)$old_ver/\1$new_ver/g" \
        -e "s/\(默认: \)${old_ver}/\1${new_ver}/g" \
        -e "s/\(VERSION:-\)${old_ver}/\1${new_ver}/g" \
        -e "s/\(VERSION=\)${old_ver}/\1${new_ver}/g" \
        -e "s/${old_ver} bash/${new_ver} bash/g" \
        "$SCRIPT_DIR/install_pkg.sh"
    info "  scripts/install_pkg.sh"

    # docs/CLI.md — 只替换 "版本: X.X.X" 头
    sed -i '' "s/\(版本: \)${old_ver}/\1${new_ver}/" "$ROOT_DIR/docs/CLI.md"
    info "  docs/CLI.md"

    # 提交版本升级
    git -C "$ROOT_DIR" add -A
    git -C "$ROOT_DIR" commit -m "chore: bump version to ${new_ver}

Auto-incremented by release script.

Co-Authored-By: Claude <noreply@anthropic.com>"
    info "版本升级已提交: ${new_ver}"
}

# ── 前置检查 ──────────────────────────────────────────────────────────────────────

check_prereqs() {
    step "检查前置依赖..."

    command -v git >/dev/null 2>&1 || error "需要 git"
    # 本脚本只推 tag 触发 CI，不本地构建，故不需要 go/gh。
    # gh 仅用于事后查看 CI/Release，可选。
    command -v gh >/dev/null 2>&1 && info "gh 已安装 (可选, 用于查看 CI/Release)" || warn "gh 未安装 (可选)"

    # 检查工作区是否干净 (版本自增会 commit，故需干净起点)
    if [ -n "$(git -C "$ROOT_DIR" status --porcelain)" ]; then
        warn "工作区有未提交的修改:"
        git -C "$ROOT_DIR" status --short
        echo ""
        if $DRY_RUN || $FORCE; then
            info "（自动继续）"
        else
            read -rp "是否继续？(y/N) " confirm || true
            if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
                error "已取消发布"
            fi
        fi
    else
        info "工作区干净"
    fi
}

# ── 构建 server jar ───────────────────────────────────────────────────────────────

# ── 发布 (推送 tag 触发 CI) ─────────────────────────────────────────────────────────
# 本脚本不本地构建、不直接创建 Release，仅: 版本自增 → commit → tag → push tag。
# 推送 v* tag 后 GitHub Actions (release.yml) 自动全平台原生编译并发布 Release。
# 产物最终在 GitHub Release 的 Assets: /releases/tag/vX.Y.Z

do_release() {
    local tag="v${VERSION}"

    # 检查 tag 是否已存在
    if git -C "$ROOT_DIR" rev-parse "$tag" >/dev/null 2>&1; then
        warn "Tag $tag 已存在 (commit: $(git -C "$ROOT_DIR" rev-parse --short "$tag"))"
        read -rp "是否覆盖？(y/N) " confirm
        if [[ "$confirm" =~ ^[Yy]$ ]]; then
            git -C "$ROOT_DIR" tag -d "$tag"
            git -C "$ROOT_DIR" push origin ":refs/tags/$tag" 2>/dev/null || true
        else
            error "已取消发布"
        fi
    fi

    # 创建 tag
    step "创建 tag: ${tag} ..."
    git -C "$ROOT_DIR" tag -a "$tag" -m "phonefast v${VERSION}"
    info "Tag $tag 已创建"

    # 推送 tag — 这一步触发 GitHub Actions (release.yml) 全平台编译 + 发版
    step "推送 tag 到 GitHub (触发 CI 全平台构建) ..."
    git -C "$ROOT_DIR" push origin "$tag"
    info "Tag 已推送，CI 已触发"

    echo ""
    echo "  → CI 进度:   https://github.com/gezihua123/phonefast/actions"
    echo "  → Release:   https://github.com/gezihua123/phonefast/releases/tag/${tag}"
    echo ""
    info "CI 将在 ~10 分钟内完成 5 平台原生编译并发布到 Release。"
    warn "如需立即查看产物，打开上面的 Actions 链接。"
}

# ── Main ──────────────────────────────────────────────────────────────────────────

main() {
    local version_arg=""

    # 先解析参数：分离版本号和选项
    for arg in "$@"; do
        case "$arg" in
            --dry-run) DRY_RUN=true ;;
            --yes|-y)  FORCE=true ;;
            --*) ;;
            *) version_arg="$arg" ;;
        esac
    done
    if [ -n "$version_arg" ]; then
        # 手动指定版本，直接使用
        VERSION="$version_arg"
        info "使用指定版本: ${VERSION}"
    else
        # 未指定版本 → 自增
        VERSION="$(bump_patch "$DEFAULT_VERSION")"
        info "自动版本自增: ${DEFAULT_VERSION} → ${VERSION}"
    fi

    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║           phonefast Release Script  v${VERSION}           ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo ""

    info "版本:      ${VERSION}"
    info "仓库:      gezihua123/phonefast"
    info "Dry-run:   ${DRY_RUN}"
    echo ""

    check_prereqs

    # dry-run: 只预览, 不改版本文件、不 commit、不打 tag、不触发 CI
    if $DRY_RUN; then
        echo ""
        warn "Dry-run 模式，仅预览，不改文件、不创建 tag、不触发 CI"
        echo "  Tag:       v${VERSION}"
        echo "  触发方式:  git push origin v${VERSION} → CI release.yml"
        echo "  产物位置:  GitHub Release Assets (CI 编译后上传)"
        echo ""
        info "试运行完成。移除 --dry-run 以实际触发 CI 发版。"
        exit 0
    fi

    # 自动自增时先升级版本号并提交 (仅非 dry-run)
    if [ -z "$version_arg" ] && [ "$VERSION" != "$DEFAULT_VERSION" ]; then
        update_version_files "$DEFAULT_VERSION" "$VERSION"
    fi

    echo ""
    warn "即将发布 phonefast v${VERSION}"
    echo "  Tag:      v${VERSION}"
    echo "  流程:     推送 tag → 触发 CI → 5 平台原生编译 → 发布 Release"
    echo ""
    if ! $FORCE; then
        read -rp "确认发布？(y/N) " confirm || true
        if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
            error "已取消发布"
        fi
    fi

    do_release

    echo ""
    info "🎉 phonefast v${VERSION} 已触发 CI 发版！"
}

main "$@"
