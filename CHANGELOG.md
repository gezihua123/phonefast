# Changelog

## v1.0.15 (2026-07-29)

### 🚀 Features
- **Hierarchical UI formats**: `observe` and `ui` commands now support `--format` flag with `flatref` (default), `jsonl`, `simplexml`, and `yml` output modes — all include parent-child relationships and depth metadata for LLM-friendly tree navigation
- **`full` UI socket mode**: Android `UISocketHandler` adds hierarchical dump (`dumpFullHierarchy`) that collects ALL nodes (no filtering) with `parent`/`depth` fields, enabling accurate tree reconstruction on the Go side
- **JPEG screenshot encoding** for MCP: `screenshot` and `observe` RPCs now return JPEG (quality 85) instead of PNG — ~10× smaller payload while preserving native resolution; `pngToJPEG` conversion with PNG fallback on failure

### ⚡ Performance
- **Concurrent screenshot + UI dump** in `observe`: `Screenshot()` and `GetUIFull()` now run in parallel goroutines with independent 30s timeouts, halving end-to-end observe latency
- **Daemon simplification**: removed unused `--socket`/`--serial` flags from daemon subcommand, fixed socket path (`/tmp/phonefast-{uid}.sock`)
- **MaxSize=0**: use native device resolution for video encoding (was hardcoded 1080)

### 🐛 Fixes
- **Android node recycling**: `UISocketHandler` now recycles all `AccessibilityNodeInfo` and `AccessibilityWindowInfo` objects in leaf→root order, preventing memory leaks during repeated UI dumps. Window recycling is paired with its root node in the same `try-finally` block — NOT extracted into a separate loop (would cause over-recycling and stale root data). `lastVisited` tracking ensures unvisited windows (early break on maxElements or exception) are still recycled.

### 🛠️ Refactor
- **Format extraction**: `formatElements` moved from `cmd/phonefast/main.go` to `internal/format.ElementsForLLM`, shared by CLI daemon mode, direct mode, and RPC handlers
- **Daemon config**: removed `SocketPath`/`PidFile` from `daemon.Config` — paths now resolved inside `New()` via `SocketName()`/`PidFileName()`

### 📝 Docs
- CLI docs (en/zh): updated daemon socket description, added `--format` flag documentation

---

## v1.0.14 (2026-07-27)

### ⚡ Performance
- **OCR memory & timing (CGO=1)**: per-op allocation 54.7 MB → 44.1 MB (−19%, under the 50 MB target); allocations/op 1.46 M → 2.1 K (−99.9%); median latency 173 ms → 162 ms with no new timing spikes
  - `ResizeImage`: rewrote bilinear path to convert source to a flat RGBA buffer once instead of `src.At()` per pixel (source of the 1.46 M allocations)
  - `CropBox`: copy cropped pixels so the full decoded screenshot (~4.5 MB) is collectible before recognition; `BaseEngine.Recognize` nils the image after cropping
  - Input-tensor scratch buffers (`Detector.detBuf`, `OnnxRecognizer.recBuf`): reuse one growable `[]float32` each across serialized calls — safe because ORT's `CreateTensorWithDataAsOrtValue` is zero-copy and the input `Value` is closed before reuse
  - `ExtractTextBoxes`: `sync.Pool` for the three `[]bool` masks; `dilateMask` writes into a caller buffer
- **Screenshot memory churn**: ~24 MB transient allocation per screenshot → ~13 MB (Go side −90%), cutting daemon RSS growth under load by ~30% (6-min Warmup-profile repro: +36.6 MB → +25.8 MB; pixel output byte-identical)
  - `pkg/avcodec`: PNG/JPEG encoding now wraps a per-decoder reused RGBA scratch (`ImageCopyToBuffer`, packed align=1) instead of allocating a fresh `image.NRGBA` per screenshot (~8.3 MB @1080p per call)
  - `internal/daemon`: image RPC responses (screenshot/observe) stream base64 directly to the socket (`base64.NewEncoder` over `bufio.Writer`) instead of materializing the base64 string plus two `json.Marshal` copies (~8 MB per call)
- **New regression guards** (`tests/ocr-benchmark/`): `TestOCRSpike` (spike-factor threshold) and `TestOCRDeterminism` (byte-identical output across iterations — catches tensor-pooling reuse races)

### 🛠️ Tooling
- **Daemon pprof endpoint**: `PHONEFAST_PPROF=localhost:6060` enables `/debug/pprof/*` (heap/goroutine/profile) on the daemon; unset = zero overhead
- **Memory profiling tools** (`tests/`): `mem_isolation.py` (per-op-class RSS phases + vmmap/footprint anatomy), `mem_repro_warmup.py` (Warmup-profile RSS replay), `pngdiff.go` (pixel-exact PNG comparison)

---

## v1.0.13 (2026-07-24)

### 🚀 Features
- **Multi-device daemon**: Removed `Config.Serial`, concurrent multi-device connections with per-serial connect serialization, RPC device_list + per-serial operations, backward-compatible Status()

### 🛠️ Refactor
- **MCP tools**: Simplified tool definitions (-319 lines), expanded test coverage (+250 lines), streamlined server transport and session lifecycle
- **CLI**: Restructured entry point for multi-device support
- **Docs**: Trimmed verbose content from CLI, DEV, phonefast, and README docs

### 🛠️ Build
- go-astiav v0.35.0 → v0.40.0 (FFmpeg n8.0 compatibility)
- FFmpeg 7.1 → 8.0 (cross-build-ffmpeg.sh)
- `scripts/test.sh`: Fixed case-in-`$()` syntax error on macOS
- `assets/ocr/lib_nolib.go`: Fixed build tag causing `RuntimeLib` redeclaration on linux/amd64 `-full` build

### 🐛 Fixes
- `pkg/h264`: Decoder improvements and new tests
- `tests/stress_test_rpc.py`: Stability improvements

---

## v1.0.11 (2026-07-14)

### 🚀 Performance
- **H.264 decoding**: ThreadCount 2→1, eliminated multi-thread slice sync overhead, DPB memory halved
- **Frame loop simplification**: Removed legacy AllocFrame probe loop, saves 2 CGO alloc/free cycles per decode
- **Memory optimization**: Frame allocation and GC pressure halved, real physical memory ~16MB (after single screenshot)
- **Screenshot speed**: Long-run P50=28ms (4.3× improvement over v1.0.0), hot screenshot RPC reaches 12ms

### 📝 Docs
- `docs/DEV.md`: Added H.264 screenshot decoding architecture doc (astiav CGO + ffmpeg CLI fallback dual-path)
- `docs/BENCHMARK.md`: Updated benchmark timeline, marked v1.0.11 release
- Site `_tabs/PHONEFAST.md`: Updated speed comparison data, memory row, architecture design, long-run stress test

### 🛠️ Build
- `scripts/install_pkg.sh`: Default install directory changed to `~/.local/bin`, removed `--local`/`--global` modes
- go-astiav v0.35.0 → v0.40.0 (FFmpeg 8.0 compatibility)
- `scripts/test.sh`: Fixed case-in-command-substitution syntax error on macOS

---

## v1.0.10 (2026-07-11)

### 🛠️ Build
- Removed darwin-amd64 (macOS Intel) support, darwin-arm64 only
- GitHub Actions CI confirmed macOS runner is fully arm64
- FFmpeg 7.1 → 8.0 (cross-build-ffmpeg.sh), source-based compilation with minimal H.264-only config

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
