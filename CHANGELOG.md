# Changelog

## Unreleased

### 🐛 Fixes
- **CLI degrades to direct mode when the daemon can't start**: previously a one-shot command (`tap`/`swipe`/`screenshot`/...) exited with a misleading "daemon failed to start (check device connection)" whenever the auto-started daemon failed (its stderr was discarded to /dev/null, hiding the real cause). Now the command falls back to its existing direct-mode (`--foreground`) implementation (~2.5s) with a warning, while the daemon's startup error is written to `/tmp/phonefast-{uid}.log` (the new `log.DefaultPath()` is referenced in the error message). `wait` no longer triggers a pointless auto-start since it sleeps locally.
- **`connect`/`disconnect`/`serve` fail fast on a missing daemon**: these commands have no direct-mode fallback (they unconditionally hit the daemon socket), so the degrade-to-direct path would print "Falling back to direct mode" and then fail with "daemon unreachable". They now route through `ensureDaemonOrExit()`, exiting with a clear error pointing at the log file. The new `daemonStartMode()` helper centralizes auto-start routing — which commands skip it (`daemon`/`daemon_worker`/`serve`/`devices`/`help`/`status`/`wait`), which require it (`connect`/`disconnect`), and which tolerate degradation (everything else).
- **Mid-RPC daemon self-heal for CLI one-shots**: the CLI now installs `daemon.SetEnsurer(ensureDaemonE)` in daemon mode, so if the daemon crashes *during* a command, `daemon.Client.Call` detects the unreachable socket, auto-restarts the daemon (deduplicated across concurrent callers), and retries the request once — the same recovery the MCP server already used. Previously a mid-RPC crash left a one-shot command to die with a broken-socket error.
- **`PHONEFAST_NO_DAEMON_FALLBACK=1` opt-out**: agents/CI that prefer a clear error over a ~250× slower direct-mode call can set this env var to disable graceful degradation and fail hard on auto-start failure.

### 🏗️ Architecture (daemon core refactor — cross-model review findings)

- **`Device` interface**: RPC handlers and `DeviceActor` now depend on a 22-method `Device` interface (`internal/daemon/device.go`) instead of the concrete `*session.Session` — daemon business logic is unit-testable without a device (`fakeDevice` in tests). Session display/serial fields became accessors (`Serial()`/`DeviceWidth()`/...).
- **Direct mode unified with daemon handlers**: the CLI's `--foreground` path dispatches through the same in-process `Dispatcher` the daemon uses (`DispatchResult`), and the hand-maintained second dispatch (`dispatchDirect`, ~155 lines) is deleted. Both modes now produce byte-identical output: `screenshot` is JPEG in both, `ui` honors `--format` in both, `run` result structures match. Action names live once in `pkg/protocol/methods.go` (was: stringly-typed literals drifting across three packages).
- **`Dispatcher` struct**: `Dispatch` is now a method holding the OCR service — the `ocrSvc` package global and `SetOCRService` cross-package injection are gone.
- **`Supervisor` in the daemon package**: lifecycle supervision (spawn/wait/kill/stop) moved out of `cmd/phonefast` into `internal/daemon`; `EnsureRunning` collapses concurrent restarts with a mutex + done-channel (the 50ms busy-poll is gone). `Client` gains a `WithEnsurer` option and the `globalEnsurer`/`globalRestartInFlight` process globals are deleted; `mcp.New` gains a matching `WithEnsurer`.
- **Session-owned observe concurrency**: `session.ObserveFull` runs screenshot + `GetUIFull` concurrently inside the session (30s watchdog that still joins both goroutines before returning) — `handleObserve` is now one device call, restoring the actor's single-threaded session model and fixing a use-after-return hazard.
- **CLI state in `cmdEnv`**: `useDaemon`/`daemonSerial`/`binName` package globals replaced by an explicit `cmdEnv` struct; command handlers are methods on it. New invariant guarded by `TestForegroundNeverTouchesDaemon`: `--foreground` never creates a daemon process.
- **God-package split**: `internal/daemon` is file-split (`rpc.go` wire types, `dispatch.go`, `handlers.go`, `params.go`, `device.go`, `server.go`, `supervisor*.go`); `pngToJPEG`/`jpegBufPool` moved to `pkg/avcodec`. Subpackage split deliberately rejected (import cycle between the wire/types layer and its users — rationale in `device.go`/`docs/DEV.md`).
- **Convergence fixes found during the review**: `dispatchDirect`'s `tap_element` missed `ScaleToDevice` (fixed by deletion); `handlePressKey` no longer depends on callers lowercasing key names; CLI `tap`/`swipe` argument parsing errors now fail loudly instead of acting on (0,0).

### 🔧 Cleanups (post-review polish)

- **`DeviceHealth` sub-interface**: `Device` is split into a narrow `DeviceHealth` (liveness/close/identity) consumed by `handleStatus` and the actor's health/reconnect/status paths, embedded by the full `Device` — the action surface no longer leaks into lifecycle code.
- **`max_elements` default unified**: new `protocol.DefClientMaxElements = 100` is the single client-facing default across CLI, MCP tools, and raw RPC (the daemon's 5000 default is gone); `clampCollectMax` centralizes the collect-limit rule.
- **`connectDeviceFn` package variable → injected field**: `Daemon.connectFn`/`DeviceActor.connectFn` (default `connectDevice`); tests inject fakes per instance instead of swapping package state.
- **`--foreground` OCR support**: the CLI's Dispatcher now holds a lazily-created OCR service (`NewService` only stores config; the ~90MB engine loads on the first OCR call), so `phonefast --foreground ocr` works — the "OCR requires daemon mode" restriction is removed. Other direct-mode commands pay nothing.
- **`status` unified across modes**: both modes now print the same daemon-status JSON (read-only probe, never auto-starts); the separate human-readable direct-mode output is gone.
- **Session dead code removed**: `Scroll`, `WaitStable`, and the `UIPort`/`VideoConn`/`Decoder` accessors had no callers — deleted with their tests (scroll protocol encoding remains covered in `pkg/protocol`).

### 🔧 Cleanups (code-review round 3)

- **Build system: `ocr/` folded back into the root module** — the nested Go module (untracked `ocr/go.mod`, gitignored `go.work`) made fresh clones and CI fail (`GOWORK=off` build broke on missing go.sum entries). Now a single module: root `go.mod` carries the OCR deps (`golang.org/x/image` added), `go.work`/`go.work.sum` deleted, CI verified.
- **CI/release staging paths fixed** — `ci.yml`/`release.yml`/`download_models.py`/`bench-ort-dylib.sh` referenced the deleted `assets/ocr/`; all now stage into `ocr/assets/` where the `go:embed` files live.
- **Screenshot no longer stalls 3s on a dead control socket** — `requestKeyframe` now marks the control connection broken on write failure (wiring the previously-orphaned `markControlBroken`), and `ScreenshotFormat` skips the keyframe reset/wait entirely when control is unavailable, serving the cached frame with a warning.
- **Daemon restart preserves `--ocr-engine`/`--ocr-vision`** — `Supervisor.Spawn` persists its extra env to `/tmp/phonefast-<uid>.env`; `restartOnce` reuses it when auto-restarting from a different CLI invocation (previously a crash + auto-restart silently reverted OCR to the default engine); `Stop` clears it.
- **`GetUISummary` retries once on transient errors** — same stale-node mitigation `ObserveFull` already applies to `GetUIFull`.
- **`phonefast screenshot` (no path) restores the data-URI stdout contract** — mime-aware (`data:image/jpeg;base64,...`); the file-writing path applies only when a path argument is given.
- **Flat `observe` timeout no longer discards a valid capture** — results arriving within the drain-grace window are returned instead of erroring.

---

## v1.0.17 (2026-08-11)

### 🐛 Fixes
- **Screenshot no longer returns stale frames**: `ScreenshotFormat` now unconditionally sends `RESET_VIDEO` before each capture, then waits for a keyframe with a newer PTS (3 s timeout with graceful degradation). Previously it only reused the last keyframe from the H.264 stream — screen changes between IDR intervals were invisible to callers, producing identical md5 across different screens (P0 root cause).
- **TypeText unified under PFIME**: all text input (ASCII and non-ASCII) now goes through the PhoneFast IME broadcast commit, which works reliably regardless of soft-keyboard state. The old scrcpy INJECT_TEXT path was vulnerable to IME predictive input interference ("Spicy Tuna Wrapsyy" / chunk drops on emulators). The first type of a session waits 300 ms for the IME InputConnection to bind after the IME switch.
- **OCR engine switched to Apple Vision on macOS**: default ONNX PP-OCR rec model produced garbage output ("1" for "Totals", Chinese characters for English UI text). Apple Vision full OCR (VNRecognizeTextRequest) correctly identifies "Totals"/"INCOME"/"OUTCOME"/"Expenses in this Week" with confidence 0.5–1.0. Requires `--variant cgo1` build (plain variant can lose Apple Vision when CGO_ENABLED=0).

### 🛠️ Refactor
- **h264 decoder exposes `LatestKeyframePTS()`**: new public method returns the PTS of the most recent keyframe, used by ScreenshotFormat to detect keyframe refresh. Thread-safe, follows the same locking pattern as `LatestKeyframe()`.
- **protocol package adds `KeycodeEscape`** constant (111) and wires it into the key-name map for `key escape` CLI support.

### 📝 Docs
- BUILD.md: §3.2 already covers cgo1 variant, Apple Vision build, and `PHONEFAST_OCR_ENGINE` runtime selection — no changes needed.

---

## v1.0.16 (2026-07-31)

### 🚀 Features
- **Daemon-level `connect`/`disconnect`**: `phonefast connect <serial>` and `phonefast disconnect <serial>` are now first-class RPC commands that create/remove device actors on the running daemon - no more `daemon --stop` + restart to switch devices. Each `DeviceActor` owns a context derived from the daemon context, so disconnecting one device shuts down only that session while the daemon and other devices keep running.

### ⚡ Performance
- **Zero-copy JPEG fast path (CGO decoder)**: YUV420P frames are wrapped directly as `image.YCbCr` for JPEG encoding, bypassing the YUV→RGBA→YUV round-trip through `sws_ctx` + `rgbaScratch` (~7 MB saved per frame). New `frameToYCbCr` reuses a persistent `yuvBuf` instead of `astiav.Frame.ToImage()`, which allocated ~1.7 MB per call via `C.GoBytes` (RSS ballooned to 180 MB+ under sustained load).
- **Direct JPEG from decoder for screenshots**: `screenshot`/`observe` RPCs request JPEG via `ScreenshotFormat(FormatJPEG)` instead of decoding PNG then re-encoding - avoids a ~4.6 MB `image.Decode` allocation. The CLI ffmpeg fallback (always PNG) still goes through `pngToJPEG`.
- **`pngToJPEG` buffer pooling**: `sync.Pool` reuses `bytes.Buffer` across calls (~200-500 KB each), copying out before returning to pool.
- **Android `UISocketHandler` allocation reduction**: reused `ByteArrayOutputStream` (`jsonBuf`) and `Rect` (`rectBuf`) across dumps; JSON streamed directly to the socket instead of materializing intermediate `byte[]`/`String` (~150 KB transient garbage per full-mode dump eliminated).
- **Skip invisible nodes**: recursion skips GONE/unlaid-out nodes (bounds=0) and their children, cutting Binder calls - P50 ~55 ms → ~35 ms.
- **`waitForIdle(100, 500)`** before dump for stable UI capture.

### 🐛 Fixes
- **UI handler crash resilience**: `handleClient` now catches `RuntimeException` (stale node, `SecurityException`, OOM) without killing the accept thread, and always closes the client socket in `finally` - prevents socket leaks that left Go clients hanging on dead connections.
- **UI socket deadline 3 s → 5 s** for more headroom on heavy hierarchies.
- **Removed stale `WriteUIDumpRequest` test** that referenced the deleted function (was breaking the `pkg/protocol` test build).

### 🛠️ Refactor
- **Default UI mode is summary**: `GetUIElements` now sends `sum` instead of `dump`; the Android handler treats `dump` as a backward-compat alias for `sum`. `ABSOLUTE_MAX_ELEMENTS` raised 500 → 5000; default `maxElements` for `get_ui_elements`/`observe` raised 100 → 5000.
- **Text/desc truncated to 80 chars** and class names simplified via `simplifyClassName` in UI dump output for token efficiency.
- **Per-actor context**: `DeviceActor` holds its own `ctx`/`cancel`; `Daemon.removeDevice(serial)` stops a single actor, closes its session, and releases its scid.

### 📝 Docs
- Restored `docs/BENCHMARK_HISTORY.md` (full benchmark timeline recovered from session caches/git history).
- Added `tests/stress_test_uidump.py` UI-dump stress harness and `tools/ocr_local.go` local OCR debug tool.
- Synced stale version strings in `docs/CLI.md`, `docs/CLI_zh.md` (1.0.11 → 1.0.16) and `scripts/install_pkg.sh` (1.0.12 → 1.0.16).

---

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
