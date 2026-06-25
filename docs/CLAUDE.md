# phonefast

Fast Android device control via MCP and CLI, combining scrcpy video streaming with phone-friendly tool semantics.

## Architecture

```
cmd/phonefast/main.go       CLI entry point (serve / daemon / tap / ...)
internal/adb/               ADB device discovery, scrcpy-server deploy & lifecycle
internal/daemon/             Daemon process: Unix-socket JSON-RPC server, health loop, reconnect
internal/log/                Async file logger with caller context (goroutine-based)
internal/mcp/                MCP server (mcp-go), tool registration with schema validation
internal/session/            Device session: video streaming, control ops, UI dump, screenshot
pkg/h264/                    H.264 AnnexB parser — reads scrcpy video frames, extracts keyframes
pkg/protocol/                scrcpy wire protocol encoding (touch, keycode, scroll, text, UI dump)
android/                     scrcpy server jar + Java source + phonefast-agent additions
tests/                       Test scripts & benchmarks (see tests/README.md)
```

## Two usage modes

| Mode | Invocation | Mechanism | Latency |
|------|------------|-----------|---------|
| Daemon (default) | `phonefast <cmd>` | Unix socket JSON-RPC to background daemon | <10ms |
| Direct | `phonefast --foreground <cmd>` | New scrcpy session per call | ~2.5s |

- By default all commands route through the daemon. If no daemon is running, `ensureDaemon()` auto-starts it.
- `--foreground` / `--direct` flags bypass the daemon for one-shot direct connections.
- `--daemon` is kept for backward compatibility (same as default).
- If daemon's connection is dead, `healthLoop` (10s) or first failed write triggers `reconnect()`.

## Key design decisions

- **scrcpy tunnel_forward=true**: device runs server, PC connects as client over ADB-forwarded TCP
- **UI dump**: fast path via custom `UISocketHandler` (abstract socket); fallback to `uiautomator dump` ADB
- **Screenshot**: pipes H.264 keyframe through ffmpeg → PNG (requires ffmpeg on host)
- **Each tool call opens a new UI socket** (server closes after each dump)
- **Control pressure**: u16 fixed-point (not IEEE float32)
- **Version detection**: reads `.version` sidecar first, falls back to scanning `classes.dex`
- **PointerID = -1**: virtual finger injection matching scrcpy's default pointer ID
- **Connection resilience**: TCP keepalive (15s ctrl / 30s video) + 10s app health loop + write-failure detection

## Logging

Async file logger at `/tmp/phonefast-{uid}.log`. Format:
```
2026-06-16 09:13:56.879 [control.go:38 Tap()] tap (244,540)
```
Logs: daemon lifecycle, session connect, RPC dispatch, every control operation, heartbeat, reconnect.

## Build

```bash
# 全平台 + 打包（默认）
bash scripts/build.sh --all

# 当前平台
bash scripts/build.sh

# 指定版本
bash scripts/build.sh --all --version 1.0.0
```

构建流程：版本检测 → Go 交叉编译（注入 ldflags）→ 产物组装（jar + version + docs）→ 压缩打包。
产物输出到 `dist/<version>/<platform>/`，全平台构建额外生成 `.tar.gz` / `.zip`。

## Build scrcpy server jar

```bash
# One-command: clone → patch → build → copy jar
bash scripts/build-server.sh

# Or using existing scrcpy clone
bash scripts/build-server.sh ~/Desktop/code/scrcpy
```

The build script: clones `scrcpy v3.3.4`, applies `android/patches/0001-phonefast-uisocket.patch`, runs Gradle, copies jar to `android/scrcpy-server.jar`.

## Test

```bash
go test ./...                              # unit tests
python3 tests/test_e2e.py                  # single-shot scrcpy socket test
python3 tests/test_release.py              # dist/ smoke test (15 checks, ~30s)
python3 tests/test_continuous.py           # 5-min endurance, outputs to test_runs/
python3 tests/test_mcp.py                  # MCP protocol full test (STDIO/SSE)
python3 tests/benchmark.py --quick         # perf benchmark (STDIO/SSE)
bash tests/benchmark.sh                    # phone-mcp vs phonefast latency cmp
```

See `tests/README.md` for the complete test suite documentation.

## CLI commands

```
phonefast tap <x> <y>
phonefast tap_element <idx|text>
phonefast swipe <x1> <y1> <x2> <y2> [dur_ms]
phonefast type <text>
phonefast back
phonefast home
phonefast key <keyname|keycode>
phonefast press_key <keyname|keycode>
phonefast launch <package>
phonefast screenshot [file]
phonefast ui
phonefast observe
phonefast wait <ms>
phonefast status
phonefast devices
phonefast run '<json>'

phonefast --foreground tap <x> <y>    # Direct mode (no daemon, ~2.5s)
phonefast --daemon tap <x> <y>        # Explicit daemon (same as default)

phonefast daemon [--foreground] [--stop] [--status] [--serial X] [--socket PATH]
phonefast serve [--transport sse|stdio] [--port N] [--host H] [--path /P]
```

## MCP tools

| Tool | Args | Description |
|------|------|-------------|
| list_devices | — | List connected Android devices |
| screenshot | — | Capture screen as base64 PNG |
| get_ui_elements | — | Get interactive UI elements |
| observe | — | Screenshot + UI elements in one call |
| tap | x, y | Tap at coordinates |
| tap_element | index or text | Tap UI element by index/text |
| swipe | start_x/y, end_x/y, duration_ms | Swipe gesture |
| type_text | text | Type text into focused field |
| back | — | Press back button |
| home | — | Press home button |
| press_key | keycode or key | Send key event |
| launch_app | package | Launch app by package name (e.g. com.android.settings) |
| wait | duration_ms | Sleep for N milliseconds |

## Android server

`android/scrcpy-server.jar` — scrcpy v3.3.4 + phonefast patch for `UISocketHandler`.
Build: `bash scripts/build-server.sh`. Patch: `android/patches/0001-phonefast-uisocket.patch`.
See `android/README.md` for details.
