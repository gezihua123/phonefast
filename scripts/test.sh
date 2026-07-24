#!/usr/bin/env bash
# scripts/test.sh — run `go test` against the same FFmpeg headers `build.sh` uses.
#
# Why this exists: bare `go test ./...` defaults to the system FFmpeg
# (homebrew 8.0 on macOS), which removed AVFMT_FLAG_SHORTEST — a macro
# go-astiav v0.35.0 still references. build.sh avoids this by compiling its own
# FFmpeg 7.x under build/cross-ffmpeg and pointing PKG_CONFIG_PATH at it. This
# wrapper does the same, so `go test` matches the production build environment.
#
# Usage:
#   bash scripts/test.sh                      # go test ./...
#   bash scripts/test.sh ./pkg/h264/ -race    # extra args forwarded
#   bash scripts/test.sh --no-cgo             # force CGO_ENABLED=0 (skips
#                                             # avcodec/session/daemon/mcp CGO tests)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Map host GOOS/GOARCH → self-compiled FFmpeg target dir name (mirrors
# pfbuild/platform.py TARGETS[*].ffmpeg_target). bash, not python, so the
# mapping is duplicated here — keep in sync if platform.py changes.
goos="$(go env GOOS 2>/dev/null || uname -s | tr '[:upper:]' '[:lower:]')"
goarch="$(go env GOARCH 2>/dev/null || { m="$(uname -m)"; if [ "$m" = "arm64" ] || [ "$m" = "aarch64" ]; then echo arm64; else echo amd64; fi; })"
case "$goos/$goarch" in
  darwin/arm64)  ffmpeg_target="aarch64-darwin" ;;
  darwin/amd64)  ffmpeg_target="x86_64-darwin" ;;
  linux/amd64)   ffmpeg_target="x86_64-linux-gnu" ;;
  linux/arm64)   ffmpeg_target="aarch64-linux-gnu" ;;
  windows/amd64) ffmpeg_target="x86_64-windows-gnu" ;;
  *)             ffmpeg_target="" ;;
esac

force_nocgo=0
args=()
for a in "$@"; do
  case "$a" in
    --no-cgo) force_nocgo=1 ;;
    *) args+=("$a") ;;
  esac
done

pkgdir="$ROOT/build/cross-ffmpeg/$ffmpeg_target/lib/pkgconfig"

# Fall back to pure-Go (CGO off) when self-compiled FFmpeg is unavailable, so a
# fresh checkout can still run the non-CGO tests. avcodec's TestStaticDecode
# needs the real CGO decoder, so set AVCODEC_SKIP_TEST (the test already honors
# it) to skip it instead of failing. Run `bash scripts/build.sh` once to
# populate build/cross-ffmpeg and get the full CGO test suite.
if [ "$force_nocgo" -eq 1 ] || [ -z "$ffmpeg_target" ] || [ ! -d "$pkgdir" ]; then
  echo "[test] self-compiled FFmpeg not available — running CGO_ENABLED=0 (avcodec CGO test skipped)"
  if [ ${#args[@]} -gt 0 ]; then
    exec env CGO_ENABLED=0 AVCODEC_SKIP_TEST=1 go test "${args[@]}"
  fi
  exec env CGO_ENABLED=0 AVCODEC_SKIP_TEST=1 go test ./...
fi

echo "[test] using self-compiled FFmpeg: $pkgdir"
if [ ${#args[@]} -gt 0 ]; then
  exec env PKG_CONFIG_PATH="$pkgdir" CGO_ENABLED=1 go test "${args[@]}"
fi
exec env PKG_CONFIG_PATH="$pkgdir" CGO_ENABLED=1 go test ./...
