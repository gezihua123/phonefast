# phonefast — Fast Android Device Control

phonefast is a fast Android device control CLI that combines scrcpy video streaming with phone-friendly operation semantics, supporting both MCP protocol integration and local CLI usage.

**Core features:**
- 🚀 **Daemon mode** — Background persistent process, Unix Socket JSON-RPC, instant command response
- 📱 **Direct mode** — No daemon, creates a new connection each time, suitable for ad-hoc operations
- 🔌 **MCP protocol** — SSE / STDIO dual transport, AI assistants can control the phone directly
- 💓 **Three-level keepalive** — TCP Keepalive + application heartbeat + write-triggered recovery, auto-reconnect within 10 seconds
- 📝 **Async logging** — Coroutine-based file writes, recording all critical operations and function context

---

## Installation

**Prerequisites:**
- Go 1.21+
- `adb` in PATH
- `ffmpeg` (required for screenshots)
- `git` (automatic version injection)
- `upx` (optional, to compress binary size)

### Build Script

Use the unified build script `scripts/build.sh`:

```bash
# Cross-platform build + packaging (default)
bash scripts/build.sh --all

# Current platform build (binary only)
bash scripts/build.sh

# Specific platform
bash scripts/build.sh --macos       # macOS amd64 + arm64
bash scripts/build.sh --linux       # Linux amd64 + arm64
bash scripts/build.sh --windows     # Windows amd64

# Specify version number (default reads from git tag, or "dev" if no tag)
bash scripts/build.sh --all --version 1.0.0
```

### Output Structure

```
dist/<version>/
├── <platform>/
│   ├── phonefast              # CLI binary
│   ├── phonefast.exe          # (Windows)
│   ├── scrcpy-server.jar      # scrcpy server (Android side)
│   ├── scrcpy-server.version  # version marker file
│   ├── README.md              # usage documentation
│   └── docs/                  # detailed docs
└── <platform>/
    └── phonefast-<version>-<os>-<arch>.tar.gz   # release package (--all / --macos / --linux / --windows)
```

### Build Process

The script automatically performs the following steps:

1. **Version detection** — Priority: `--version` flag → `git describe --tags` → `"dev"`
2. **Prerequisite check** — Verify Go toolchain + `android/scrcpy-server.jar` exists
3. **Go build** — Cross-compile, inject version/build time/commit hash via `-ldflags`
4. **Artifact assembly** — Copy `scrcpy-server.jar`, `scrcpy-server.version`, `README.md`, `docs/`
5. **Archive packaging** — Generate `.tar.gz` (macOS/Linux) or `.zip` (Windows) release package

### Manual System Install (Optional)

```bash
cp dist/<version>/darwin_arm64/phonefast /usr/local/bin/
mkdir -p /usr/local/share/phonefast
cp dist/<version>/darwin_arm64/scrcpy-server.jar /usr/local/share/phonefast/
cp dist/<version>/darwin_arm64/scrcpy-server.version /usr/local/share/phonefast/
```

---

## Quick Start

```bash
# List connected devices
phonefast devices

# Start daemon (automatically starts on first use, also manual)
phonefast daemon

# Default daemon mode — instant response (<10ms)
phonefast back
phonefast tap 540 960
phonefast screenshot /tmp/screen.png

# Direct mode — new connection each time (~2.5s), add --foreground
phonefast --foreground back
phonefast --foreground tap 540 960

# Start MCP server (for AI assistants)
phonefast serve
```

---

## Demo

![phonefast 4x speed demo](assets/phonefast_demo.gif)

> phonefast in action — observe, tap, swipe, and type on an Android device with sub-30ms latency.

---

## Performance

phonefast's daemon mode delivers consistently low latency across all operations. Below are the results from a **12-hour stress test** (v1.0.11, 145,843 operations, 100% success rate, zero reconnects):

| Operation | P50 | P95 | P99 | Notes |
|-----------|:---:|:---:|:---:|-------|
| `tap` | **13ms** | 13ms | 14ms | Touch at coordinates |
| `back` / `home` / `press_key` | **12-13ms** | 13ms | 14ms | Hardware key events |
| `screenshot` | **28ms** | 126ms | 128ms | H.264 keyframe → PNG |
| `observe` | **28ms** | 126ms | 129ms | Screenshot + UI (atomic) |
| `get_ui_elements` | **46ms** | 132ms | 151ms | UI tree via UISocketHandler |
| `swipe` | **318ms** | 322ms | 323ms | Gesture (includes 300ms duration) |
| `type_text` / `launch_app` / `status` | **1ms** | 1-2ms | 2-4ms | Fire-and-forget semantics |
| `wait` | **33ms** | 33ms | 33ms | Sleep command |

**Key benchmarks:**
- Daemon mode response time: **<13ms** for touch and key operations
- Screenshot P50: **28ms** (4.3x faster than v1.0.0's 121ms)
- Real physical memory: **~16MB** steady-state (verified via vmmap)
- 12-hour stress test: **145,843 ops, 100% success, 0 reconnects**
- 200 consecutive screenshots: **P50 = 12ms, P95 = 13ms** (hot decoder)

> Detailed benchmark history, version comparison, and methodology at [docs/benchmark.md](docs/benchmark.md).

---

## Command Reference

### Format

```bash
phonefast [--foreground|--daemon] <command> [args...]
```

- Default uses daemon mode (auto-starts daemon), latency <10ms.
- `--foreground` / `--direct` — Direct mode, creates a new scrcpy connection each time, ~2.5s.
- `--daemon` — Explicitly specify daemon mode (same as default, kept for compatibility).

---

### Touch Operations

#### `tap` — Tap at coordinates

```bash
phonefast [--foreground|--daemon] tap <x> <y>
```

| Parameter | Description |
|-----------|-------------|
| `x` | X coordinate (pixels) |
| `y` | Y coordinate (pixels) |

```bash
phonefast tap 540 960                 # Tap screen center
phonefast --foreground tap 100 200    # Direct mode
```

#### `tap_element` — Tap UI element

```bash
phonefast [--foreground|--daemon] tap_element <index|text>
```

| Parameter | Description |
|-----------|-------------|
| `index` | UI element index (from `ui` command) |
| `text` | UI element text or description (fuzzy search) |

```bash
phonefast tap_element 5              # Tap the 5th UI element
phonefast tap_element "Settings"    # Tap element containing "Settings" text
```

#### `swipe` — Swipe gesture

```bash
phonefast [--foreground|--daemon] swipe <x1> <y1> <x2> <y2> [duration_ms]
```

| Parameter | Description | Default |
|-----------|-------------|---------|
| `x1` `y1` | Start coordinates | — |
| `x2` `y2` | End coordinates | — |
| `duration_ms` | Swipe duration (milliseconds) | 500 |

```bash
phonefast swipe 540 1600 540 400 500   # Swipe up
phonefast swipe 200 500 800 500 300    # Swipe right 300ms
```

---

### Text Input

#### `type` — Input text

```bash
phonefast [--foreground|--daemon] type <text>
```

Inputs text into the current focused input field.

```bash
phonefast type "Hello World"
phonefast type "Search keyword"
```

---

### Key Operations

#### `back` — Back key

```bash
phonefast [--foreground|--daemon] back
```

#### `home` — Home key

```bash
phonefast [--foreground|--daemon] home
```

#### `key` — Send key event

```bash
phonefast [--foreground|--daemon] key <keyname|keycode>
```

**Supported key names:**

| Key Name | Description |
|----------|-------------|
| `enter` | Enter |
| `tab` | Tab key |
| `delete` / `backspace` | Delete |
| `space` | Space |
| `escape` / `esc` | Escape key |
| `volume_up` / `volume_down` | Volume +/- |
| `volume_mute` | Mute |
| `power` | Power button |
| `menu` | Menu key |
| `search` | Search key |
| `camera` | Camera key |
| `back` | Back (same as back command) |
| `home` | Home (same as home command) |
| `dpad_up` / `dpad_down` / `dpad_left` / `dpad_right` / `dpad_center` | D-pad keys |
| `page_up` / `page_down` | Page up/down |
| `media_play_pause` / `media_stop` / `media_next` / `media_previous` | Media controls |

```bash
phonefast key enter
phonefast key backspace
phonefast key volume_up
phonefast key power
phonefast key dpad_down

# Can also use keycodes directly
phonefast key 4       # BACK
phonefast key 3       # HOME
phonefast key 66      # ENTER
```

---

### App Operations

#### `launch` — Launch app

```bash
phonefast [--foreground|--daemon] launch <package>
```

Uses Android package name (display names like "Settings" or "Chrome" are not supported).

```bash
phonefast launch com.android.settings     # Settings
phonefast launch com.android.chrome        # Chrome
phonefast launch com.tencent.mm             # WeChat
```

---

### Screen Capture & Analysis

#### `screenshot` — Take screenshot

```bash
phonefast [--foreground|--daemon] screenshot [file]
```

| Parameter | Description | Default |
|-----------|-------------|---------|
| `file` | Save path | stdout (base64) |

```bash
phonefast screenshot /tmp/screen.png       # Save as PNG
phonefast screenshot                        # Output base64
```

#### `ui` — UI element list

```bash
phonefast [--foreground|--daemon] ui
```

Outputs all interactive UI elements on the current screen (max 50), including index, text, resource ID, class name, and clickable state.

```
[0] id="content" (FrameLayout)
[1] id="webView" (FrameLayout)
[2] text="Search" (EditText) [clickable]
[3] text="Settings" id="settings_btn" (Button) [clickable]
```

#### `observe` — Screenshot + UI

```bash
phonefast [--foreground|--daemon] observe
```

Concurrently captures screenshot and UI data in a single call for a complete screen state snapshot.

---

### Utility Commands

#### `wait` — Wait

```bash
phonefast [--foreground|--daemon] wait <ms>
```

| Parameter | Description | Default |
|-----------|-------------|---------|
| `ms` | Milliseconds to wait | 1000 |

#### `status` — Daemon status

```bash
phonefast [--foreground|--daemon] status
```

```bash
# Example output
daemon running (pid 60977)
  device:    13709314CF044927 (488x1080)
  control:   true
  ui:        true
```

#### `devices` — List devices

```bash
phonefast devices
```

```bash
# Example output
Connected devices:
  13709314CF044927  device  [TECNO_KL8h]
```

#### `run` — JSON single operation

```bash
phonefast [--foreground|--daemon] run '<json>'
```

Suitable for script automation.

```bash
phonefast run '{"action":"tap","args":{"x":540,"y":960}}'
phonefast run '{"action":"screenshot"}'
phonefast run '{"action":"back"}'
phonefast run '{"action":"list_devices"}'
```

Supported actions: `tap`, `tap_element`, `swipe`, `back`, `home`, `type_text`, `press_key`, `launch_app`, `screenshot`, `get_ui_elements`, `observe`, `list_devices`, `wait`.

---

## Daemon Management

The daemon is a background persistent process that holds long-lived device connections and receives JSON-RPC requests via Unix Socket.

```bash
# Start daemon (background)
phonefast daemon

# Foreground mode (view real-time logs)
phonefast daemon --foreground

# Specify device serial
phonefast daemon --serial 13709314CF044927

# Custom socket/PID file paths
phonefast daemon --socket /tmp/my-phone.sock

# Check daemon status
phonefast daemon --status

# Stop daemon
phonefast daemon --stop
```

**Auto-management:**
- When executing commands with `--daemon` flag, the daemon auto-starts in the background if not already running
- If the daemon process exists but is unresponsive, it is automatically killed and restarted
- Calling `phonefast daemon` multiple times will not start duplicate instances (exits if already running)

---

## MCP Server

phonefast can serve as an MCP (Model Context Protocol) server, allowing AI assistants like Claude Desktop to control the phone directly.

```bash
# SSE mode (default port 8019)
phonefast serve

# Custom port
phonefast serve --port 8080

# Custom path
phonefast serve --path /mcp

# STDIO mode (Claude Desktop integration)
phonefast serve --transport stdio
```

### Client Configuration

**SSE mode:**
```json
{
  "mcpServers": {
    "phonefast": {
      "url": "http://localhost:8019/Phone/sse"
    }
  }
}
```

**STDIO mode:**
```json
{
  "mcpServers": {
    "phonefast": {
      "command": "phonefast",
      "args": ["serve", "--transport", "stdio"]
    }
  }
}
```

### MCP Tool List

| Tool | Parameters | Description |
|------|------------|-------------|
| `list_devices` | — | List connected Android devices |
| `screenshot` | — | Capture current screen (base64 PNG) |
| `get_ui_elements` | — | Get interactive UI elements |
| `observe` | — | Screenshot + UI elements (single call) |
| `tap` | `x`, `y` | Tap at coordinates |
| `tap_element` | `index` or `text` | Tap UI element |
| `swipe` | `start_x`, `start_y`, `end_x`, `end_y`, `duration_ms` | Swipe gesture |
| `type_text` | `text` | Input text |
| `back` | — | Back key |
| `home` | — | Home key |
| `press_key` | `keycode` or `key` | Send key event |
| `launch_app` | `package` | Launch app (package name only, e.g. `com.android.settings`) |
| `wait` | `duration_ms` | Wait for N milliseconds |

---

## Architecture

```
phonefast CLI
    │
    ├── --daemon mode ──→ Unix Socket ──→ daemon process ──→ TCP ──→ scrcpy server (device)
    │                    JSON-RPC          holds persistent      control+video+UI
    │                                      connections
    └── direct mode ──→ new session each time ──→ TCP ──→ scrcpy server (device)
                         deploy+start+connect+close

Internal structure:
  internal/
  ├── adb/       ADB device discovery, scrcpy deployment & lifecycle
  ├── daemon/    Daemon process, JSON-RPC dispatch, health checks
  ├── log/       Async file logging
  ├── mcp/       MCP server (based on mcp-go), tool registration
  ├── session/   Device session: video stream, control, UI capture, screenshot
  pkg/
  ├── h264/      H.264 AnnexB parsing, keyframe extraction
  └── protocol/  scrcpy protocol encoding & control messages
```

---

## Logging

Writes asynchronously to `/tmp/phonefast-{uid}.log`, recording all critical operations and calling context.

**Log format:**
```
2026-06-16 09:13:56.879 [session.go:139 Connect()] connected: 488x1080  control=true
2026-06-16 09:13:59.602 [rpc.go:115 Dispatch()] rpc back
2026-06-16 09:13:59.603 [control.go:138 Back()] back
2026-06-16 09:13:59.624 [control.go:38 Tap()] tap (244,540)
2026-06-16 09:13:59.952 [control.go:93 Swipe()] swipe (200,900)→(200,300) 300ms
2026-06-16 09:14:26.000 [daemon.go:328 healthLoop()] health: connection dead, reconnecting...
2026-06-16 09:14:29.000 [daemon.go:298 reconnect()] reconnected: 13709314CF044927 (488x1080)
```

**Coverage:** daemon lifecycle, device connection, RPC dispatch, control operations, heartbeat detection, reconnection.

---

## Reconnection

Three-level keepalive mechanism:

| Level | Mechanism | Interval | Description |
|-------|-----------|----------|-------------|
| OS level | TCP Keepalive | Video 30s / Control 15s | OS detects dead connections |
| App level | `healthLoop` goroutine | 10s | Checks video + control connection liveness, auto-reconnect |
| Write-triggered | `markControlBroken` | Instant | Immediately marks on write failure, reconnects and retries on next request |

When the device USB disconnects or scrcpy is killed, the daemon auto-detects and recovers within 10 seconds.

---

## Mode Comparison

| | Daemon Mode | Direct Mode |
|------|-------------|-------------|
| Command format | `phonefast <cmd>` (default) | `phonefast --foreground <cmd>` |
| Response speed | <10ms | ~2.5s |
| Resource usage | Single daemon process in background | Creates/destroys connection each time |
| Use case | Batch operations, script automation | Ad-hoc single operations |
| Auto-management | Auto-start/restart/recovery | Stateless |

---

## License

MIT
