#!/usr/bin/env bash
#
# phonefast GitHub Release 发布脚本
#
# 用法:
#   bash scripts/release.sh              # 自动版本自增 + 发布
#   bash scripts/release.sh --dry-run    # 试运行（只构建不发布）
#   bash scripts/release.sh 1.0.3        # 指定版本号发布
#   bash scripts/release.sh -y           # 跳过所有确认
#
# 前置依赖:
#   - gh      (GitHub CLI, 已登录)
#   - go      (Go 工具链)
#   - git
#
# 流程:
#   ① 检查工作区是否干净
#   ② 全平台构建
#   ③ 创建 git tag
#   ④ 创建 GitHub Release
#   ⑤ 上传所有构建产物

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

    command -v gh >/dev/null 2>&1 || error "需要安装 GitHub CLI (gh)"
    command -v go >/dev/null 2>&1 || error "需要安装 Go 工具链"

    # 检查 gh 登录状态
    if ! gh auth status 2>&1 | grep -qi "logged in"; then
        if $DRY_RUN; then
            warn "gh 未登录（dry-run 模式跳过，实际发布前需登录）"
        else
            error "gh 未登录，请先运行: gh auth login"
        fi
    else
        info "gh 已登录"
    fi

    # 检查工作区是否干净
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

# ── 构建 ──────────────────────────────────────────────────────────────────────────

do_build() {
    step "全平台构建 v${VERSION} ..."
    bash "$SCRIPT_DIR/build.sh" --all --version "$VERSION"
    info "构建完成"
}

# ── 发布 ──────────────────────────────────────────────────────────────────────────

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

    # 推送 tag
    step "推送 tag 到 GitHub ..."
    git -C "$ROOT_DIR" push origin "$tag"
    info "Tag 已推送"

    # 从 CHANGELOG 或 git log 生成发布说明
    step "生成发布说明 ..."
    local prev_tag
    prev_tag="$(git -C "$ROOT_DIR" describe --tags --abbrev=0 "$tag" 2>/dev/null || git -C "$ROOT_DIR" rev-list --max-parents=0 HEAD)"
    if [ "$prev_tag" = "$tag" ]; then
        prev_tag=""
    fi

    local release_notes
    if [ -n "$prev_tag" ]; then
        release_notes=$(git -C "$ROOT_DIR" log --oneline --no-decorate "${prev_tag}..${tag}" 2>/dev/null | sed 's/^/- /' || echo "")
    else
        release_notes=$(git -C "$ROOT_DIR" log --oneline --no-decorate -20 2>/dev/null | sed 's/^/- /' || echo "")
    fi
    release_notes="## 更新内容

${release_notes:-_(无)_}

---

> 全平台预编译二进制，内含 scrcpy-server.jar，单文件部署即用。
"

    # 收集构建产物
    step "收集发布产物 ..."
    local dist_dir="$ROOT_DIR/dist/$VERSION"
    local archives=()

    # 如果 dist 目录不在标准位置，从 dist/dev 收集
    if [ -d "$ROOT_DIR/dist/dev" ]; then
        # 查找所有 tar.gz 文件
        while IFS= read -r -d '' f; do
            archives+=("$f")
        done < <(find "$ROOT_DIR/dist/dev" -name "*.tar.gz" -type f -print0 2>/dev/null || true)
    fi

    if [ ${#archives[@]} -eq 0 ]; then
        warn "未找到发布包，尝试从构建产物中查找..."
        # 尝试在 dev 目录中直接查找
        if [ -d "$ROOT_DIR/dist/dev" ]; then
            while IFS= read -r -d '' f; do
                archives+=("$f")
            done < <(find "$ROOT_DIR/dist/dev" -maxdepth 1 -type f -name "*.tar.gz" -print0 2>/dev/null || true)
        fi
    fi

    if [ ${#archives[@]} -eq 0 ]; then
        warn "未找到 tar.gz 发布包，将仅发布源码 Release（无二进制附件）"
    else
        info "找到 ${#archives[@]} 个发布包"
    fi

    # 创建 GitHub Release
    step "创建 GitHub Release: ${tag} ..."
    if [ ${#archives[@]} -gt 0 ]; then
        gh release create "$tag" \
            --repo "gezihua123/phonefast" \
            --title "phonefast v${VERSION}" \
            --notes "$release_notes" \
            "${archives[@]}"
    else
        gh release create "$tag" \
            --repo "gezihua123/phonefast" \
            --title "phonefast v${VERSION}" \
            --notes "$release_notes"
    fi
    info "Release ${tag} 已发布！"
    echo ""
    echo "  → https://github.com/gezihua123/phonefast/releases/tag/${tag}"
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

    # 自动自增时先升级版本号并提交
    if [ -z "$version_arg" ] && [ "$VERSION" != "$DEFAULT_VERSION" ]; then
        update_version_files "$DEFAULT_VERSION" "$VERSION"
    fi

    do_build

    if $DRY_RUN; then
        echo ""
        warn "Dry-run 模式，跳过 GitHub Release 发布"
        echo "  Tag:       v${VERSION}"
        echo "  产物:"
        find "$ROOT_DIR/dist/dev" -name "*.tar.gz" -type f -exec echo "    {}" \;
        echo ""
        info "试运行完成。移除 --dry-run 以实际发布。"
        exit 0
    fi

    echo ""
    warn "即将发布 phonefast v${VERSION} 到 GitHub Releases"
    echo "  Tag:      v${VERSION}"
    echo "  构建产物: dist/dev/*.tar.gz"
    echo ""
    if ! $FORCE; then
        read -rp "确认发布？(y/N) " confirm || true
        if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
            error "已取消发布"
        fi
    fi

    do_release

    echo ""
    info "🎉 phonefast v${VERSION} 发布完成！"
}

main "$@"
