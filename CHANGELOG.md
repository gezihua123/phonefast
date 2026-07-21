# Changelog

## v1.0.12 (2026-07-21)

### 🚀 Features
- **OCR engine**: PP-OCR v3 text recognition (detection + recognition), ONNX Runtime backend with Vision ANE fast path on macOS (Apple Neural Engine detection <1ms), optional NCNN backend (28% faster rec), OCR JSON-RPC endpoint and `phonefast ocr` CLI command
- **Unicode text input**: Custom PFIME (headless Android IME) for CJK/emoji text injection via Base64 broadcast, dual-path dispatch (ASCII scrcpy protocol for speed, PFIME for Unicode), auto IME switch/restore on session lifecycle
- **Build variants**: `-full` variant embeds ONNX Runtime shared library (macOS arm64 only) for zero-dependency deployment, plain variant loads system `libonnxruntime` at runtime
- **Python build system**: `scripts/build.py` with platform matrix (`scripts/pfbuild/`), cross-compilation support, plain + `-full` dual-variant builds

### 🛠️ CI / Build
- GitHub Actions: CI builds both plain and `-full` (ocr_embed tag) variants for darwin-arm64
- Linux CI: ONNX Runtime library installed from GitHub releases for OCR smoke test
- CI trigger branch corrected to `master`, OCR benchmark tests excluded from CI pipeline
- ONNX Runtime install guide added to README (macOS `brew`, Linux manual download)

### 📝 Docs
- README reposition: "Phone as a Native Device for AI Agents"
- README_zh.md: Chinese README updated with ONNX Runtime installation guide

---

## v1.0.11 (2026-07-14)

### 🚀 Performance
- **H.264 decoding**: ThreadCount 2→1, eliminated multi-thread slice sync overhead, DPB memory halved
- **Frame loop simplification**: Removed legacy AllocFrame probe loop, saves 2 CGO alloc/free cycles per decode
- **Memory optimization**: Frame allocation and GC pressure halved, real physical memory ~16MB (after single screenshot)
- **Screenshot speed**: Long-run P50=28ms (4.3× improvement over v1.0.0), hot screenshot RPC reaches 12ms

### 📝 Docs
- `docs/DEV.md`: Added H.264 screenshot decoding architecture doc (astiav CGO + ffmpeg CLI fallback dual-path)
- `docs/benchmark.md`: Updated benchmark timeline, marked v1.0.11 release
- Site `_tabs/phonefast.md`: Updated speed comparison data, memory row, architecture design, long-run stress test

### 🛠️ Build
- `scripts/install_pkg.sh`: Default install directory changed to `~/.local/bin`, removed `--local`/`--global` modes

---

## v1.0.10 (2026-07-11)

### 🛠️ Build
- Removed darwin-amd64 (macOS Intel) support, darwin-arm64 only
- GitHub Actions CI confirmed macOS runner is fully arm64

---

## v1.0.9 (2026-07-11)

### 🐛 Fixes
- CI release pipeline: Skip Windows cross-compilation tests (known CGO limitation)

---

## v1.0.8 (2026-07-11)

### 🛠️ Build
- GitHub Actions CI: Enabled 5-platform native runner auto-build + release
- Scheme 3 (CI native runners) as the sole release path

---

## v1.0.7 (2026-07-08)

### 🐛 Fixes
- **Android 14 LocalSocket 4-byte limit**: Rewrote UISocketHandler read logic — batch-read first 4 bytes to avoid `readByte()` > 4 calls triggering silent connection reset
- Download URL path prefixed with `v` to match tag format

### 🔧 Improvements
- `scripts/build-server.sh`: Auto-build scrcpy-server.jar
- `scripts/release.sh`: Clean `dist/` before build, build jar before Go binary

---

## v1.0.6 (2026-07-08)

### 🛠️ Build
- release.sh cleans dist/ before build

---

## v1.0.5 (2026-07-08)

### 🛠️ Build
- release.sh builds scrcpy-server.jar before Go binary

---

## v1.0.4 (2026-07-08)

### 🐛 Fixes
- **Android 14 compatibility**: UISocketHandler read limit fix, resolves get_ui_elements socket silently disconnecting on Android 14 devices
- Uses batch read instead of byte-by-byte readByte() to work around Android 14 LocalSocket underlying bug

---

## v1.0.3 (2026-07-08)

### 🔧 Improvements
- CLI help text completed, supports `--help`/`-h` flags
- SKILL.md command examples corrected

---

## v1.0.2 (2026-07-08)

### 🛠️ Build
- Automated version bump workflow

---

## v1.0.1 (2026-07-08)

### 🔧 Improvements
- `scripts/install_pkg.sh`: Auto-detect system architecture, download matching prebuilt package
- Install script supports `--local`/`--global` modes

---

## v1.0.0 (2026-07-08)

### 🎉 Initial Release

- **phonefast CLI**: Full command set — tap/swipe/type/screenshot/observe/launch and more
- **Daemon mode**: Unix Socket JSON-RPC persistent process, <1ms communication latency
- **MCP service**: STDIO/SSE dual transport, native ImageContent output
- **scrcpy integration**: H.264 keyframe screenshot + UISocketHandler UI parsing
- **Three-level keepalive**: TCP keepalive + healthLoop + write-failure auto recovery
- **Cross-platform static build**: macOS/Linux/Windows, FFmpeg statically linked into binary
