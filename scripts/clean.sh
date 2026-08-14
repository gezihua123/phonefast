#!/usr/bin/env bash
# clean.sh — 清理构建产物与运行时残留
# Usage:
#   bash scripts/clean.sh              # 默认 (light): 所有可重新下载/构建的产物
#   bash scripts/clean.sh --light      # 同上
#   bash scripts/clean.sh --deep       # + Go modcache + IDE 目录
#   bash scripts/clean.sh --purge      # + git clean -Xdf (所有 git-ignored 文件)
#   bash scripts/clean.sh --runtime    # 追加: 停止 daemon + 清理 /tmp 运行文件 (pid/sock/log/截图)
#   bash scripts/clean.sh -n           # dry-run, 只预览不删除
#   bash scripts/clean.sh -f           # 跳过交互确认
#
# 清理原则：凡是可以重新下载、重新编译、自动生成的文件，默认全部删除。
#   - 构建产物 → build.sh / build-server.sh 重建
#   - FFmpeg 库 → download-ffmpeg.sh 重下载编译
#   - OCR 模型/运行时 → ocr/scripts/download.sh 重下载
#   - Go 缓存 → go 自动重建
#   - Gradle 缓存 → Gradle 自动下载
#
# Daemon 停止 + /tmp 运行期文件 (pid/sock/log/截图) 需显式 --runtime，
# 不在 --light/--deep/--purge 中默认执行，避免误停正在运行的服务。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# macOS /tmp → /private/tmp symlink; resolve so find -path matches correctly.
TMPDIR="$(cd /tmp 2>/dev/null && pwd -P)"
cd "$ROOT"

LEVEL="light"
RUNTIME=false
DRY=false
FORCE=false
TOTAL_FREED=0  # KB

usage() {
    cat <<'EOF'
clean.sh — 清理 phonefast 构建产物与运行时残留

Usage:  bash scripts/clean.sh [OPTIONS]

Levels:
  --light     默认。所有可重新下载/构建的产物 + 缓存 + 测试产物
  --deep      追加: Go modcache、IDE 目录 (.idea, .vscode)、ocr/vendor
  --purge     追加: git clean -Xdf (所有 git-ignored 文件)
  --runtime   追加: 停止 daemon + 清理 /tmp 运行期文件 (pid/sock/log/截图)

Options:
  -n, --dry    预览模式，只显示将要删除的内容
  -f, --force  跳过所有交互确认
  -h, --help   显示此帮助
EOF
    exit 0
}

# ── 参数解析 ──────────────────────────────────────────────────────────────────
for arg in "$@"; do
    case "$arg" in
        --light) LEVEL="light" ;;
        --deep)  LEVEL="deep" ;;
        --purge) LEVEL="purge" ;;
        --runtime) RUNTIME=true ;;
        -n|--dry) DRY=true ;;
        -f|--force) FORCE=true ;;
        -h|--help) usage ;;
        *) echo "未知参数: $arg"; usage ;;
    esac
done

# ── banner ─────────────────────────────────────────────────────────────────────
echo "========================================"
echo " phonefast clean ($LEVEL)"
$DRY && echo " DRY RUN — 仅预览，不执行删除"
echo "========================================"

# ── helpers ────────────────────────────────────────────────────────────────────
_bytes() {
    if [[ -e "$1" || -L "$1" ]]; then
        du -sk "$1" 2>/dev/null | cut -f1
    else
        echo 0
    fi
}
_human() { du -sh "$1" 2>/dev/null | awk '{print $1}'; }

# _rmpath — 删除单个文件/目录，显示大小，累计释放量
_rmpath() {
    local path="$1"
    local label
    # 路径在项目内则显示相对路径，否则显示完整路径
    if [[ "$path" == "$ROOT"* ]]; then
        label="${2:-${path:$(( ${#ROOT} + 1 ))}}"
    else
        label="${2:-$path}"
    fi
    if [[ -e "$path" || -L "$path" ]]; then
        local kb; kb=$(_bytes "$path")
        TOTAL_FREED=$((TOTAL_FREED + kb))
        if $DRY; then
            echo "  would rm: $label  ($(_human "$path"))"
        else
            printf "  rm %-50s " "$label"
            rm -rf "$path" && echo "ok  ($(_human "$path"))" || echo "FAIL"
        fi
    fi
}

# _rmglob — 删除匹配 glob 的文件（支持任意 base 目录）
# Usage: _rmglob "label" "/base/dir" "*.pattern"
_rmglob() {
    local desc="$1" base="$2" pat="$3"
    local count=0
    local total_kb=0
    while IFS= read -r -d '' f; do
        if [[ -e "$f" ]]; then
            total_kb=$((total_kb + $(_bytes "$f")))
            if $DRY; then
                echo "  would rm: $f"
                ((count++)) || true
            else
                rm -rf "$f" && ((count++)) || true
            fi
        fi
    done < <(find "$base" -path "$pat" -print0 2>/dev/null)
    TOTAL_FREED=$((TOTAL_FREED + total_kb))
    if [[ "$count" -gt 0 ]]; then
        local mb; mb=$(echo "scale=1; $total_kb/1024" | bc 2>/dev/null || echo 0)
        if (( total_kb >= 1024 )); then
            echo "  → $desc: $count file(s), ${mb}MB"
        else
            echo "  → $desc: $count file(s), ${total_kb}KB"
        fi
    fi
}

# _findrm — 按文件名模式递归删除（仅 project root 下）
_findrm() {
    local name="$1"
    local desc="${2:-$name}"
    _rmglob "$desc" "$ROOT" "*/$name"
}

# ── 1. 停止 daemon (仅 --runtime) ─────────────────────────────────────────────
if $RUNTIME; then
echo ""
echo "=== Daemon ==="

my_uid=$(id -u)

shopt -s nullglob
for pf in "$TMPDIR"/phonefast-${my_uid}-*.pid; do
    [[ -f "$pf" ]] || continue
    pid=$(cat "$pf" 2>/dev/null || true)
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
        if $DRY; then
            echo "  would kill: daemon pid=$pid"
        else
            printf "  stopping daemon pid=%s ... " "$pid"
            kill "$pid" 2>/dev/null || true
            sleep 0.3
            kill -0 "$pid" 2>/dev/null && { kill -9 "$pid" 2>/dev/null; echo "force-killed"; } || echo "stopped"
        fi
    fi
done
shopt -u nullglob

# 兜底: kill 所有 daemon_worker orphan 进程
if ! $DRY; then
    orphan=$(pgrep -f "phonefast.*daemon_worker" 2>/dev/null || true)
    for p in $orphan; do
        printf "  killing orphan daemon pid=%s ... " "$p"
        kill "$p" 2>/dev/null && echo "ok" || echo "skip"
    done
fi
fi  # RUNTIME

# ── 2. 构建产物 (build.sh 可重建) ────────────────────────────────────────────
echo ""
echo "=== Build artifacts (rebuildable) ==="

_rmpath "$ROOT/dist"          "dist/"
_rmpath "$ROOT/build"         "build/ (FFmpeg libs → download-ffmpeg.sh 可重下载)"
_rmpath "$ROOT/.build-tmp"    ".build-tmp/"

# 散落二进制 & 归档包（仅项目根一层）
shopt -s nullglob
for f in "$ROOT"/phonefast "$ROOT"/phonefast.exe "$ROOT"/phonefast-* \
         "$ROOT"/*.tar.gz "$ROOT"/*.zip; do
    _rmpath "$f"
done
shopt -u nullglob

# ── 3. Android JAR (build-server.sh 可重建) ──────────────────────────────────
echo ""
echo "=== Android JAR (rebuildable: scripts/build-server.sh) ==="

_rmpath "$ROOT/android/scrcpy-server.jar"
_rmpath "$ROOT/android/scrcpy-server.version"
_rmpath "$ROOT/android/build"        "android/build/ (Gradle build cache)"
_rmpath "$ROOT/android/.gradle"      "android/.gradle/ (Gradle dependency cache)"
_rmpath "$ROOT/assets/scrcpy-server.jar"
_rmpath "$ROOT/assets/scrcpy-server.version"

# ── 4. OCR 模型与运行时 (ocr/scripts/download.sh 可重下载) ──────────────────
echo ""
echo "=== OCR models & runtime (re-downloadable: ocr/scripts/download.sh) ==="

# ONNX Runtime 动态库 (*.dylib / *.so / *.dll)
_rmglob "ORT lib" "$ROOT/ocr/assets" "$ROOT/ocr/assets/libonnxruntime-*"

# PP-OCR 模型文件
_rmpath "$ROOT/ocr/assets/ppocr-det.onnx"
_rmpath "$ROOT/ocr/assets/ppocr-rec.onnx"

# OCR 模块构建产物
_rmpath "$ROOT/ocr/dist"      "ocr/dist/"

# 旧 OCR 模型位置（已迁至 ocr/assets/）
_rmpath "$ROOT/tests/ocr-models"

# ── 5. 测试产物 ───────────────────────────────────────────────────────────────
echo ""
echo "=== Test artifacts ==="

_rmpath "$ROOT/test_runs"
_rmpath "$ROOT/capture_output"

_findrm "*.test"     "*.test binaries"
_findrm "*.prof"     "*.prof files"
_findrm "*.out"      "coverage *.out"
_findrm "__pycache__" "__pycache__"
_findrm "*.pyc"      "*.pyc files"
_findrm "*.egg-info" "*.egg-info"

# ── 6. Go 缓存 ────────────────────────────────────────────────────────────────
echo ""
echo "=== Go cache ==="

_go_clean() {
    local dir="$1" label="$2"
    if $DRY; then
        echo "  would run: (cd $label && go clean -cache -testcache)"
    else
        (cd "$dir" && go clean -cache -testcache 2>/dev/null) && \
            echo "  $label: go clean ok" || echo "  $label: go clean skipped"
    fi
}

_go_clean "$ROOT"     "root module"
_go_clean "$ROOT/ocr" "ocr module"

# ── 7. 运行时残留 (/tmp, 仅 --runtime) ─────────────────────────────────────────
if $RUNTIME; then
echo ""
echo "=== Runtime files ==="

# PID / socket / log
_rmglob "pid/sock/log" "$TMPDIR" "$TMPDIR/phonefast-${my_uid}-*.pid"
_rmglob "pid/sock/log" "$TMPDIR" "$TMPDIR/phonefast-${my_uid}-*.sock"
_rmglob "pid/sock/log" "$TMPDIR" "$TMPDIR/phonefast-${my_uid}*.log"
_rmglob "pid/sock/log" "$TMPDIR" "$TMPDIR/phonefast.log"

# 截图（/tmp 零散截图）
_rmglob "screenshots"  "$TMPDIR" "$TMPDIR/phonefast-screenshot-*.png"
_rmglob "screenshots"  "$TMPDIR" "$TMPDIR/phonefast-screenshot-*.jpg"

# 临时构建二进制（/tmp 下各种 test build）
_rmglob "tmp binaries" "$TMPDIR" "$TMPDIR/phonefast-*"
_rmglob "tmp binaries" "$TMPDIR" "$TMPDIR/pf-*"

# 打包测试目录
_rmpath "$TMPDIR/phonefast-dist"

# Tesseract OCR 引擎临时目录
_rmglob "tesseract tmp" "$TMPDIR" "$TMPDIR/phonefast-tesseract-*"

# Skill / verify 临时目录
_rmpath "$TMPDIR/phonefast-skill"
_rmpath "$TMPDIR/phonefast-verify"

# FFmpeg 调试临时目录
_rmpath "$TMPDIR/ffcheck"

# 项目根零散的截图文件（由工具漏生成）
shopt -s nullglob
for f in "$ROOT"/screenshot_*.jpg "$ROOT"/screenshot_*.png; do
    _rmpath "$f"
done
shopt -u nullglob

fi  # RUNTIME

# ── 8. 编辑器/操作系统垃圾 ────────────────────────────────────────────────────
echo ""
echo "=== OS/Editor cruft ==="

_findrm ".DS_Store"
_findrm ".AppleDouble"
_findrm ".LSOverride"
_findrm "*.swp"   "vim swap"
_findrm "*.swo"   "vim swap"
_findrm "*~"      "backup files"
_findrm "Thumbs.db"
_findrm "ehthumbs.db"

# ── 9. Deep: modcache + IDE ───────────────────────────────────────────────────
if [[ "$LEVEL" == "deep" || "$LEVEL" == "purge" ]]; then
    echo ""
    echo "=== Deep clean ==="

    # Go modcache (go mod download 可恢复)
    if $DRY; then
        echo "  would run: go clean -modcache"
    else
        go clean -modcache 2>/dev/null || true
        (cd "$ROOT/ocr" && go clean -modcache 2>/dev/null) || true
        echo "  go clean -modcache: ok"
    fi

    # OCR vendor (go mod vendor 可恢复)
    _rmpath "$ROOT/ocr/vendor"

    # IDE 配置目录
    _rmpath "$ROOT/.idea"
    _rmpath "$ROOT/.vscode"
fi

# ── 10. Purge: git clean ──────────────────────────────────────────────────────
if [[ "$LEVEL" == "purge" ]]; then
    echo ""
    echo "=== git clean -X (purge) ==="

    _git_clean() {
        local dir="$1" label="$2"
        if $DRY; then
            echo "  $label:"
            (cd "$dir" && git clean -Xdn 2>/dev/null) || true
        else
            echo "  $label — files to be removed:"
            (cd "$dir" && git clean -Xdn 2>/dev/null) || true
            echo ""
            if $FORCE; then
                confirm="y"
            else
                read -r -p "  Proceed with 'git clean -Xdf'? [y/N] " confirm
            fi
            if [[ "$confirm" == "y" || "$confirm" == "Y" ]]; then
                (cd "$dir" && git clean -Xdf 2>/dev/null) && echo "  done" || echo "  skipped"
            else
                echo "  skipped"
            fi
        fi
    }

    _git_clean "$ROOT"     "root"
    if [[ -f "$ROOT/ocr/go.mod" ]]; then
        echo ""
        _git_clean "$ROOT/ocr" "ocr module"
    fi
fi

# ── 总结 ─────────────────────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " Clean complete ($LEVEL)"
echo "========================================"

if $DRY; then
    echo "  (dry run — nothing was deleted)"
elif [[ "$TOTAL_FREED" -gt 0 ]]; then
    freed_mb=$(echo "scale=1; $TOTAL_FREED / 1024" | bc 2>/dev/null || echo 0)
    echo "  Total freed: ~${freed_mb}MB"
else
    echo "  Nothing to clean."
fi

if ! $DRY && command -v go &>/dev/null; then
    echo "  Go caches remaining:"
    for c in GOCACHE GOMODCACHE; do
        dir=$(go env $c 2>/dev/null || true)
        if [[ -d "$dir" ]]; then
            echo "    $c: $(_human "$dir")"
        fi
    done
fi
