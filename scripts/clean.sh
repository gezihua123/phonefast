#!/usr/bin/env bash
# clean.sh — 清理构建产物与运行时残留
# Usage:
#   bash scripts/clean.sh              # 默认 (light) 清理
#   bash scripts/clean.sh --light      # 安全: 构建产物 + 缓存 + 运行时
#   bash scripts/clean.sh --deep       # + Go modcache + IDE 目录
#   bash scripts/clean.sh --purge      # + git clean -Xdf (所有 git-ignored 文件)
#   bash scripts/clean.sh -n           # dry-run, 只预览不删除
#   bash scripts/clean.sh -f           # 跳过交互确认
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

LEVEL="light"
DRY=false
FORCE=false

usage() {
    cat <<'EOF'
clean.sh — 清理 phonefast 构建产物与运行时残留

Usage:  bash scripts/clean.sh [OPTIONS]

Levels:
  --light     默认。安全: 构建产物、缓存、运行时文件
  --deep      追加: Go modcache、IDE 目录 (.idea, .vscode)
  --purge     追加: git clean -Xdf (所有 git-ignored 文件)

Options:
  -n, --dry    预览模式，只显示将要删除的内容
  -f, --force  跳过所有交互确认
  -h, --help   显示此帮助
EOF
    exit 0
}

# ── 参数解析 ──────────────────────────────────────────────────────────────────────
for arg in "$@"; do
    case "$arg" in
        --light) LEVEL="light" ;;
        --deep)  LEVEL="deep" ;;
        --purge) LEVEL="purge" ;;
        -n|--dry) DRY=true ;;
        -f|--force) FORCE=true ;;
        -h|--help) usage ;;
        *) echo "未知参数: $arg"; usage ;;
    esac
done

# ── banner ─────────────────────────────────────────────────────────────────────────
echo "========================================"
echo " phonefast clean ($LEVEL)"
if $DRY; then
    echo " DRY RUN — 仅预览，不执行删除"
fi
echo "========================================"

# ── helpers ────────────────────────────────────────────────────────────────────────
_sz() { du -sh "$1" 2>/dev/null | cut -f1; }

_find_rm() {
    local pattern="$1" dir="${2:-$ROOT}" name="${3:-$pattern}"
    local count=0
    while IFS= read -r -d '' f; do
        if $DRY; then
            echo "  would rm: $f"
            ((count++)) || true
        else
            rm -rf "$f" && ((count++)) || true
        fi
    done < <(find "$dir" -name "$pattern" -print0 2>/dev/null)
    if [[ "$count" -gt 0 ]]; then
        echo "  $name: $count file(s)"
    fi
}

_clean() {
    local desc="$1" path="$2"
    if [[ -e "$path" || -L "$path" ]]; then
        local sz; sz=$(_sz "$path")
        if $DRY; then
            echo "  would rm: $path  ($sz)"
        else
            printf "  rm %-45s " "$path"
            rm -rf "$path" && echo "ok  ($sz)" || echo "FAIL"
        fi
    fi
}

_clean_glob() {
    # Usage: _clean_glob "desc" "/path/prefix-*"
    local desc="$1" pat="$2"
    local found=0
    for f in $pat; do
        if [[ -e "$f" ]]; then
            if $DRY; then
                echo "  would rm: $f"
            else
                rm -rf "$f" && echo "  rm $f" || echo "  rm FAIL: $f"
            fi
            found=1
        fi
    done
    if [[ "$found" -eq 0 ]]; then
        : # nothing matched
    fi
}

# ── 1. 停止 daemon ────────────────────────────────────────────────────────────────
echo ""
echo "=== Daemon ==="
_UID="${UID:-$(id -u)}"
PIDFILE="/tmp/phonefast-${_UID}-*.pid"

# Find and kill running daemon
for pf in $PIDFILE; do
    if [[ -f "$pf" ]]; then
        pid=$(cat "$pf" 2>/dev/null || true)
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            if $DRY; then
                echo "  would kill: phonefast daemon pid=$pid"
            else
                printf "  stopping daemon pid=%s ... " "$pid"
                kill "$pid" 2>/dev/null || true
                sleep 0.3
                # Force kill if still alive
                if kill -0 "$pid" 2>/dev/null; then
                    kill -9 "$pid" 2>/dev/null || true
                    echo "force-killed"
                else
                    echo "stopped"
                fi
            fi
        fi
    fi
done

# ── 2. 构建产物 ───────────────────────────────────────────────────────────────────
echo ""
echo "=== Build artifacts ==="
_clean "dist/"                    "$ROOT/dist"
_clean "build/"                   "$ROOT/build"
_clean ".build-tmp/"              "$ROOT/.build-tmp"
_clean "android/build/"           "$ROOT/android/build"
_clean "android/.gradle/"         "$ROOT/android/.gradle"

# 散落二进制
_clean_glob "temp bin" "$ROOT/phonefast"
_clean_glob "temp bin" "$ROOT/phonefast.exe"
_clean_glob "temp bin" "$ROOT/dist/phonefast-*"

# 构建产物压缩包
_clean_glob "archives" "$ROOT/*.tar.gz"
_clean_glob "archives" "$ROOT/*.zip"
_clean_glob "archives" "$ROOT/dist/*.tar.gz"
_clean_glob "archives" "$ROOT/dist/*.zip"

# assets.go (可能由 build 生成)
_clean "assets.go"                "$ROOT/assets.go"

# assets/ 构建产物（源文件在 android/）
_clean "assets/scrcpy-server.jar"      "$ROOT/assets/scrcpy-server.jar"
_clean "assets/scrcpy-server.version"  "$ROOT/assets/scrcpy-server.version"

# ── 3. 测试产物 ───────────────────────────────────────────────────────────────────
echo ""
echo "=== Test artifacts ==="
_clean "test_runs/"               "$ROOT/test_runs"
_clean "capture_output/"          "$ROOT/capture_output"

# Go test 缓存与 profile 文件
_find_rm "*.test"  "$ROOT" "*.test binaries"
_find_rm "*.prof"  "$ROOT" "*.prof files"
_find_rm "*.out"   "$ROOT" "coverage *.out"

# Python 缓存
_find_rm "__pycache__" "$ROOT"  "__pycache__"
_find_rm "*.pyc"       "$ROOT"  "*.pyc files"
_find_rm "*.egg-info"  "$ROOT"  "*.egg-info"

# ── 4. Go 缓存 ────────────────────────────────────────────────────────────────────
echo ""
echo "=== Go cache ==="
if $DRY; then
    echo "  would run: go clean -cache -testcache"
else
    printf "  go clean -cache ... "
    go clean -cache 2>/dev/null && echo "ok" || echo "skipped"
    printf "  go clean -testcache ... "
    go clean -testcache 2>/dev/null && echo "ok" || echo "skipped"
fi

if [[ "$LEVEL" == "deep" || "$LEVEL" == "purge" ]]; then
    if $DRY; then
        echo "  would run: go clean -modcache"
    else
        printf "  go clean -modcache ... "
        go clean -modcache 2>/dev/null && echo "ok" || echo "skipped"
    fi
fi

# ── 5. 运行时残留 ─────────────────────────────────────────────────────────────────
echo ""
echo "=== Runtime files ==="

# PID / socket / log (wildcard in case of variant suffixes)
_clean_glob "pid"     "/tmp/phonefast-${_UID}-*.pid"
_clean_glob "sock"    "/tmp/phonefast-${_UID}-*.sock"
_clean_glob "log"     "/tmp/phonefast-${_UID}*.log"
_clean_glob "log"     "/tmp/phonefast.log"

# Screenshots
_clean_glob "screenshots" "/tmp/phonefast-screenshot*.png"
_clean_glob "screenshots" "/tmp/phonefast-*-test.png"

# Test / verify binaries in /tmp (use prefix to avoid double match)
_clean_glob "tmp bin" "/tmp/phonefast-v[0-9]*"
_clean_glob "tmp bin" "/tmp/phonefast-ver-*"

# Skill / verify temp dirs
_clean "tmp skill dir"   "/tmp/phonefast-skill"
_clean "tmp verify dir"  "/tmp/phonefast-verify"

# ── 6. 编辑器/操作系统垃圾 ────────────────────────────────────────────────────────
echo ""
echo "=== OS/Editor cruft ==="

# macOS
_find_rm ".DS_Store"   "$ROOT" "DS_Store"
_find_rm ".AppleDouble" "$ROOT" "AppleDouble"
_find_rm ".LSOverride"  "$ROOT" "LSOverride"

# Editor swap/backup
_find_rm "*.swp"  "$ROOT" "vim swap"
_find_rm "*.swo"  "$ROOT" "vim swap"
_find_rm "*~"     "$ROOT" "backup files"

# Thumbs.db (Windows)
_find_rm "Thumbs.db"    "$ROOT" "Thumbs.db"
_find_rm "ehthumbs.db"  "$ROOT" "ehthumbs.db"

# ── 7. Deep: IDE 目录 ─────────────────────────────────────────────────────────────
if [[ "$LEVEL" == "deep" || "$LEVEL" == "purge" ]]; then
    echo ""
    echo "=== IDE config (deep) ==="
    _clean ".idea/"    "$ROOT/.idea"
    _clean ".vscode/"  "$ROOT/.vscode"
fi

# ── 8. Purge: git clean ───────────────────────────────────────────────────────────
if [[ "$LEVEL" == "purge" ]]; then
    echo ""
    echo "=== git clean -X (purge) ==="
    if $DRY; then
        echo "  would run: git clean -Xdn"
        git clean -Xdn 2>/dev/null || true
    else
        echo "  Files to be removed (preview):"
        git clean -Xdn 2>/dev/null || true
        echo ""

        if $FORCE; then
            confirm="y"
        else
            read -r -p "  Proceed with 'git clean -Xdf'? [y/N] " confirm
        fi

        if [[ "$confirm" == "y" || "$confirm" == "Y" ]]; then
            git clean -Xdf 2>/dev/null && echo "  git clean done" || echo "  git clean skipped"
        else
            echo "  skipped"
        fi
    fi
fi

# ── 总结 ─────────────────────────────────────────────────────────────────────────
echo ""
echo "========================================"
echo " Clean complete ($LEVEL)"
echo "========================================"
if ! $DRY && command -v go &>/dev/null; then
    echo "  Go cache remaining:"
    while IFS= read -r line; do
        dir="${line#*=}"
        [[ -d "$dir" ]] && echo "    $dir  ($(_sz "$dir"))" || true
    done < <(go env GOCACHE GOMODCACHE 2>/dev/null)
fi
