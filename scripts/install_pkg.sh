#!/usr/bin/env bash
# ===========================================================================
# phonefast Package Installer
# ===========================================================================
# 基于 install.md 中的发布链接，自动检测系统架构并下载安装 phonefast 预编译包。
#
# 用法:
#   bash scripts/install_pkg.sh                    # 安装到 ~/.local/bin
#   bash scripts/install_pkg.sh --version 1.0.10    # 指定版本
#   bash scripts/install_pkg.sh --dry-run          # 仅打印信息，不安装
#   bash scripts/install_pkg.sh --help             # 显示帮助
#
# 环境变量:
#   VERSION       - 版本号 (默认: 1.0.10)
#   INSTALL_DIR   - 安装目录 (默认: 根据平台自动选择)
#   GITHUB_MIRROR - GitHub 镜像地址 (默认: https://github.com)
# ============================================================================

set -euo pipefail

# ── 颜色 ────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${GREEN}[✓]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[✗]${NC} $*" >&2; exit 1; }
step()  { echo -e "${CYAN}::${NC} $*"; }

# ── 默认配置 ─────────────────────────────────────────────────────────────────
REPO="gezihua123/phonefast"
VERSION="${VERSION:-1.0.10}"
GITHUB_MIRROR="${GITHUB_MIRROR:-https://github.com}"
BASE_URL="${GITHUB_MIRROR}/${REPO}/releases/download/v${VERSION}"

# ── 平台默认安装目录 ─────────────────────────────────────────────────────────
default_install_dir() {
  local os="$1"
  case "$os" in
    linux|darwin) echo "$HOME/.local/bin" ;;
    windows)      echo "$HOME/bin"         ;;
    *)            echo "$HOME/.local/bin"  ;;
  esac
}

# ── 帮助 ─────────────────────────────────────────────────────────────────────
show_help() {
  cat <<EOF
phonefast Package Installer — 自动下载并安装 phonefast 预编译包

用法: bash scripts/install_pkg.sh [选项]

选项:
  --version V   指定版本号 (默认: 1.0.10)
  --dry-run     只检测系统信息，不执行安装
  --help        显示本帮助

环境变量:
  VERSION       版本号 (默认: 1.0.10)
  INSTALL_DIR   安装目录 (默认: ~/.local/bin)
  GITHUB_MIRROR GitHub 镜像地址

示例:
  bash scripts/install_pkg.sh
  VERSION=1.0.10 bash scripts/install_pkg.sh
EOF
  exit 0
}

# ── 系统检测 ─────────────────────────────────────────────────────────────────
detect_platform() {
  local os arch

  # 检测操作系统
  case "$(uname -s)" in
    Darwin)  os="darwin"  ;;
    Linux)   os="linux"   ;;
    CYGWIN*|MINGW*|MSYS*) os="windows" ;;
    *)       error "不支持的操作系统: $(uname -s)" ;;
  esac

  # 检测架构
  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) error "不支持的架构: $(uname -m)" ;;
  esac

  # macOS Intel (amd64) 不再支持
  if [ "$os" = "darwin" ] && [ "$arch" = "amd64" ]; then
    error "phonefast 不再支持 macOS Intel (amd64)。请使用 Apple Silicon (arm64) Mac。"
  fi

  # Windows 下检查是否 64 位
  if [ "$os" = "windows" ] && [ "$arch" != "amd64" ]; then
    error "Windows 仅支持 amd64 架构"
  fi

  echo "$os" "$arch"
}

# ── 下载文件 ─────────────────────────────────────────────────────────────────
download_file() {
  local url="$1"
  local output="$2"
  local desc="$3"

  step "下载 ${desc}..."

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 --connect-timeout 10 "$url" -o "$output"
  elif command -v wget >/dev/null 2>&1; then
    wget -q --retry-connrefused --timeout=10 "$url" -O "$output"
  else
    error "需要 curl 或 wget 来下载文件"
  fi

  if [ ! -f "$output" ] || [ ! -s "$output" ]; then
    error "下载失败: ${url}\n      请检查网络连接，或尝试设置 GITHUB_MIRROR 使用镜像"
  fi

  local size
  size=$(du -h "$output" | cut -f1)
  info "下载完成: ${size}"
}

# ── 主流程 ───────────────────────────────────────────────────────────────────
main() {
  local dry_run=false

  # 解析参数
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --version) VERSION="$2";   shift 2 ;;
      --dry-run) dry_run=true;   shift ;;
      --help|-h) show_help ;;
      *)         warn "未知选项: $1"; shift ;;
    esac
  done
  BASE_URL="${GITHUB_MIRROR}/${REPO}/releases/download/v${VERSION}"

  # 检测平台
  read -r os arch <<< "$(detect_platform)"
  local filename="phonefast-${VERSION}-${os}-${arch}.tar.gz"
  local download_url="${BASE_URL}/${filename}"

  echo ""
  echo "╔══════════════════════════════════════════════════════════════╗"
  echo "║              phonefast ${VERSION}  安装程序              ║"
  echo "╚══════════════════════════════════════════════════════════════╝"
  echo ""
  info "系统:    ${os} / ${arch}"
  info "版本:    ${VERSION}"
  info "下载源:  ${download_url}"

  if [ "$dry_run" = true ]; then
    echo ""
    info "Dry-run 模式，未执行任何操作。"
    echo ""
    echo "  下载链接:   ${download_url}"
    echo "  包文件名:   ${filename}"
    return 0
  fi

  # 确定安装目录
  local install_dir="${INSTALL_DIR:-$(default_install_dir "$os")}"
  info "安装目录: ${install_dir}"

  # 创建临时目录
  local tmp_dir
  tmp_dir=$(mktemp -d)
  local pkg_path="${tmp_dir}/${filename}"

  # ── 下载 ─────────────────────────────────────────────────────────────
  download_file "$download_url" "$pkg_path" "phonefast ${VERSION} 发布包"

  # ── 解压 ─────────────────────────────────────────────────────────────
  step "解压发布包..."
  tar -xzf "$pkg_path" -C "$tmp_dir"
  info "解压完成"

  # ── 安装二进制 ───────────────────────────────────────────────────────
  local bin_src="${tmp_dir}/phonefast-${os}-${arch}"
  if [ "$os" = "windows" ]; then
    bin_src="${tmp_dir}/phonefast-${os}-${arch}.exe"
  fi

  if [ ! -f "$bin_src" ]; then
    # 尝试不带架构后缀的二进制名
    bin_src="${tmp_dir}/phonefast"
    if [ "$os" = "windows" ]; then
      bin_src="${tmp_dir}/phonefast.exe"
    fi
  fi

  if [ ! -f "$bin_src" ]; then
    error "解压后未找到 phonefast 二进制文件:\n      $(ls -la "${tmp_dir}" 2>/dev/null | head -20)"
  fi

  step "安装到 ${install_dir}..."
  mkdir -p "$install_dir"
  cp "$bin_src" "${install_dir}/phonefast"
  chmod +x "${install_dir}/phonefast"
  info "二进制已安装: ${install_dir}/phonefast"

  # 验证安装
  if command -v "${install_dir}/phonefast" >/dev/null 2>&1 || [ -x "${install_dir}/phonefast" ]; then
    info "安装成功!"
    echo ""
    "${install_dir}/phonefast" --help 2>/dev/null || "${install_dir}/phonefast" 2>&1 | head -5 || true
  fi

  # ── PATH 提示 ────────────────────────────────────────────────────────
  if ! command -v phonefast >/dev/null 2>&1; then
    local rc_file
    case "$os" in
      windows) rc_file="$HOME/.bash_profile" ;;
      *)       rc_file="$HOME/.zshrc"        ;;
    esac
    warn "phonefast 不在 PATH 中，请将以下目录加入 PATH:"
    warn "  export PATH=\"${install_dir}:\$PATH\""
    echo ""
    echo "  或运行:"
    echo "    echo 'export PATH=\"${install_dir}:\$PATH\"' >> ${rc_file}"
    echo "    source ${rc_file}"
  fi

  # ── 清理临时文件 ─────────────────────────────────────────────────────
  rm -rf "$tmp_dir"

  echo ""
  echo "╔══════════════════════════════════════════════════════════════╗"
  echo "║         🎉 phonefast ${VERSION} 安装完成！              ║"
  echo "╚══════════════════════════════════════════════════════════════╝"
  echo ""
  echo "  运行以下命令开始使用:"
  echo "    phonefast --help"
  echo ""
  echo "  连接设备:"
  echo "    phonefast daemon"
  echo ""
  echo "  MCP 模式:"
  echo "    phonefast serve"
  echo ""
}

main "$@"
