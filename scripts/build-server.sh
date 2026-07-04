#!/usr/bin/env bash
# =============================================================================
# Build scrcpy-server.jar with phonefast UISocketHandler patch
# =============================================================================
# What this does:
#   1. Clone scrcpy v3.3.4 (or use existing SCRCYP_DIR)
#   2. Apply phonefast patches from android/patches/
#   3. Build server with Gradle
#   4. Copy the resulting JAR to android/scrcpy-server.jar
#
# Prerequisites:
#   - ANDROID_HOME pointing to Android SDK (compileSdk 36 required)
#   - Java 17+
#   - Git
#
# Usage:
#   bash scripts/build-server.sh                           # fresh clone
#   bash scripts/build-server.sh /path/to/scrcpy           # use existing clone
#   bash scripts/build-server.sh --skip-build              # only apply patches
# =============================================================================

set -euo pipefail

SCRCPY_TAG="v3.3.4"
SCRCPY_URL="https://github.com/Genymobile/scrcpy.git"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
PATCH_DIR="$PROJECT_ROOT/android/patches"
JAR_OUT="$PROJECT_ROOT/android/scrcpy-server.jar"

# ── Args ──────────────────────────────────────────────────────────────────────
SCRCYP_DIR="${1:-}"
SKIP_BUILD=false

if [ "$SCRCYP_DIR" = "--skip-build" ]; then
    SKIP_BUILD=true
    SCRCYP_DIR=""
fi

# ── Prereqs ───────────────────────────────────────────────────────────────────
check_prereqs() {
    local missing=()

    if [ -z "${ANDROID_HOME:-}" ]; then
        echo "ERROR: ANDROID_HOME is not set"
        echo "  export ANDROID_HOME=/path/to/Android/sdk"
        exit 1
    fi

    # Accept any installed SDK platform >= 30
    local sdk_found=""
    local dir ver
    for dir in "$ANDROID_HOME/platforms"/android-*; do
        [ -d "$dir" ] || continue
        ver="${dir##*/android-}"
        if [[ "$ver" =~ ^[0-9]+$ ]] && [ "$ver" -ge 30 ] 2>/dev/null; then
            sdk_found="$ver"
            break
        fi
    done

    if [ -z "$sdk_found" ]; then
        echo "WARNING: No Android SDK platform >= 30 found, trying to install android-36..."
        "$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" "platforms;android-36" 2>/dev/null || {
            echo "ERROR: Could not install android-36. Install it manually:"
            echo "  sdkmanager 'platforms;android-36'"
            exit 1
        }
    fi

    command -v java >/dev/null 2>&1 || missing+=("java")
    command -v git >/dev/null 2>&1 || missing+=("git")

    if [ ${#missing[@]} -gt 0 ]; then
        echo "ERROR: Missing tools: ${missing[*]}"
        exit 1
    fi

    echo "[OK] ANDROID_HOME=$ANDROID_HOME"
    echo "[OK] Java: $(java -version 2>&1 | head -1)"
}

# ── Clone ─────────────────────────────────────────────────────────────────────
prepare_scrcpy() {
    if [ -n "$SCRCYP_DIR" ]; then
        echo ""
        echo "=== Using existing scrcpy clone: $SCRCYP_DIR ==="
        if [ ! -d "$SCRCYP_DIR/.git" ]; then
            echo "ERROR: $SCRCYP_DIR is not a git repository"
            exit 1
        fi
        cd "$SCRCYP_DIR"
        # Reset to clean state (including untracked files the patch may have added)
        git checkout -- . 2>/dev/null || true
        git clean -fd server/src/ 2>/dev/null || true
        git checkout "$SCRCPY_TAG" 2>&1 || {
            git fetch origin "$SCRCPY_TAG"
            git checkout "$SCRCPY_TAG"
        }
    else
        SCRCYP_DIR="$PROJECT_ROOT/.build-tmp/scrcpy"
        echo ""
        echo "=== Cloning scrcpy $SCRCPY_TAG → $SCRCYP_DIR ==="
        rm -rf "$SCRCYP_DIR"
        mkdir -p "$(dirname "$SCRCYP_DIR")"
        git clone --depth 1 --branch "$SCRCPY_TAG" "$SCRCPY_URL" "$SCRCYP_DIR" 2>&1 | tail -1
    fi
    echo "[OK] scrcpy ready at $SCRCYP_DIR ($(git describe --tags 2>/dev/null || echo "$SCRCPY_TAG"))"
}

# ── Patch ─────────────────────────────────────────────────────────────────────
apply_patches() {
    echo ""
    echo "=== Applying phonefast patches ==="
    cd "$SCRCYP_DIR"

    for patch in "$PATCH_DIR"/*.patch; do
        local name=$(basename "$patch")
        echo "  $name"
        git apply --verbose "$patch" 2>&1 | sed 's/^/    /'
    done

    # Verify
    if ! grep -q "UISocketHandler" server/src/main/java/com/genymobile/scrcpy/Server.java; then
        echo "ERROR: Patch application failed — Server.java does not reference UISocketHandler"
        exit 1
    fi
    if [ ! -f server/src/main/java/com/genymobile/scrcpy/control/UISocketHandler.java ]; then
        echo "ERROR: Patch application failed — UISocketHandler.java not found"
        exit 1
    fi
    echo "[OK] All patches applied"

    # ── Overlay latest UISocketHandler ────────────────────────────────────
    # The patch provides a baseline; the canonical source lives in
    # android/phonefast-agent/UISocketHandler.java with protocol fixes
    # (e.g. bulk-read 4 bytes to work around a LocalSocket + readByte()
    # compatibility issue on certain Android builds).
    local latest_handler="$PROJECT_ROOT/android/phonefast-agent/UISocketHandler.java"
    if [ -f "$latest_handler" ]; then
        cp "$latest_handler" server/src/main/java/com/genymobile/scrcpy/control/UISocketHandler.java
        echo "[OK] Overlaid latest UISocketHandler.java from phonefast-agent/"
    fi
}

# ── Build ─────────────────────────────────────────────────────────────────────
build_server() {
    echo ""
    echo "=== Building scrcpy-server ==="
    cd "$SCRCYP_DIR"

    # scrcpy uses root gradlew to drive the :server subproject
    ./gradlew :server:assembleRelease 2>&1 | tail -30

    local built_apk="server/build/outputs/apk/release/server-release-unsigned.apk"
    if [ ! -f "$built_apk" ]; then
        echo "ERROR: Build failed — APK not found at $built_apk"
        exit 1
    fi

    cp "$built_apk" "$JAR_OUT"
    echo ""
    # Copy jar to assets/ for Go embed (single-binary distribution)
    mkdir -p "$PROJECT_ROOT/assets"
    cp "$JAR_OUT" "$PROJECT_ROOT/assets/scrcpy-server.jar"
    echo "=== Build complete ==="
    echo "  $(ls -lh "$JAR_OUT" | awk '{print $5}')  $JAR_OUT"
    echo "  $(ls -lh "$PROJECT_ROOT/assets/scrcpy-server.jar" | awk '{print $5}')  $PROJECT_ROOT/assets/scrcpy-server.jar"
}

# ── Write version sidecar ─────────────────────────────────────────────────────
write_version() {
    # Write to android/ (deploy.go reads sidecar next to jar)
    local ver_android="$PROJECT_ROOT/android/scrcpy-server.version"
    echo "$SCRCPY_TAG" | sed 's/^v//' > "$ver_android"
    echo "[OK] Version → android/scrcpy-server.version: $(cat "$ver_android")"

    # Sync to assets/ for Go embed (single-binary distribution)
    mkdir -p "$PROJECT_ROOT/assets"
    cp "$ver_android" "$PROJECT_ROOT/assets/scrcpy-server.version"
    echo "[OK] Version → assets/scrcpy-server.version"

    # Also write to dist/dev/ if it exists (legacy)
    local ver_dist="$PROJECT_ROOT/dist/dev/scrcpy-server.version"
    if [ -d "$(dirname "$ver_dist")" ]; then
        cp "$ver_android" "$ver_dist"
        echo "[OK] Version → dist/dev/scrcpy-server.version"
    fi
}

# ── Main ──────────────────────────────────────────────────────────────────────
check_prereqs
prepare_scrcpy
apply_patches

if [ "$SKIP_BUILD" = false ]; then
    build_server
    write_version
else
    echo ""
    echo "=== Patches applied. Skip build (--skip-build). ==="
    echo "  cd $SCRCYP_DIR/server && ./gradlew assembleRelease"
fi
