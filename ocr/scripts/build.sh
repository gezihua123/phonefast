#!/usr/bin/env bash
set -euo pipefail

# phonefast OCR build script — thin wrapper over the unified scripts/build.py.
#
# Kept for backward compat: docs and muscle memory call `bash ocr/scripts/build.sh
# <variant>`. Each variant now forwards to `build.py --variant <name>` so there
# is one build code path (builder.py + variants.py) for all OCR configurations.
#
# Usage: bash ocr/scripts/build.sh [variant]
#
# Variants:
#   default     Plain build, CGO=0 (no Apple Vision) — matches the old default.
#               = CGO_ENABLED=0 build.py --variant plain
#   cgo1        CGO=1, Apple Vision active (macOS). = build.py --variant cgo1
#   apple       Apple Vision only (NO_OCR_MODELS).   = build.py --variant apple
#   full        Self-contained (embed ORT lib).      = build.py --variant full
#
# No `all` — build.py is single-variant-per-invocation by design. To build
# multiple variants, run this script once per variant, or loop in shell:
#   for v in plain cgo1 apple full; do bash scripts/build.sh --variant $v; done
#
# Notes:
# - Output now lands in dist/dev/ (build.py's dir), not dist/. Bin names follow
#   build.py convention (phonefast-<os>-<arch> for plain/full, phonefast-cgo1 /
#   phonefast-apple for the macos_only variants).
# - TAGS / BIN_NAME / DIST_DIR env vars are no longer read (variant is the
#   single source of truth). CGO_ENABLED is still honored by build.py.
# - To build plain WITH CGO=1 (Apple Vision) instead of this script's CGO=0
#   default, run `bash scripts/build.sh --variant plain` directly.

# Locate the unified build.py (repo root / scripts / build.py).
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_PY="$SCRIPT_DIR/../../scripts/build.py"

if [ ! -f "$BUILD_PY" ]; then
    echo "error: build.py not found at $BUILD_PY" >&2
    exit 1
fi

run() {  # run <variant-name> [NAME=VALUE env assignments...]
    local variant="$1"; shift
    env "$@" python3 "$BUILD_PY" --variant "$variant"
}

case "${1:-default}" in
    default)
        # Preserve the old default = pure-Go (CGO=0, no Apple Vision) behavior.
        run plain CGO_ENABLED=0
        ;;
    cgo1)
        run cgo1
        ;;
    apple)
        run apple
        ;;
    full)
        run full
        ;;
    *)
        echo "Usage: $0 [default|cgo1|apple|full]"
        echo ""
        echo "Each variant forwards to: build.py --variant <name>"
        echo "  default = CGO_ENABLED=0 build.py --variant plain (pure-Go, no Apple Vision)"
        echo "  cgo1    = build.py --variant cgo1 (CGO=1, Apple Vision on macOS)"
        echo "  apple   = build.py --variant apple (Apple Vision only)"
        echo "  full    = build.py --variant full (embed ORT lib)"
        echo ""
        echo "No 'all' — build.py is single-variant-per-invocation. To build several:"
        echo "  for v in plain cgo1 apple full; do bash scripts/build.sh --variant \$v; done"
        exit 1
        ;;
esac
