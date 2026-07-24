# phonefast CLI Manual

> Version: 1.0.11 | License: MIT | Platform: macOS / Linux / Windows

phonefast is a high-performance Android device control CLI tool, built with Go and the scrcpy protocol. Designed for high-frequency AI Agent interaction scenarios, it achieves sub-10ms per-command latency and supports a background daemon mode as well as MCP protocol integration.

---

## Table of Contents

1. [Installation and Build](#1-installation-and-build)
2. [Quick Start](#2-quick-start)
3. [Mode Flags](#3-mode-flags)
4. [Command Reference](#4-command-reference)
   - [4.1 Touch Operations](#41-touch-operations)
   - [4.2 Text Input](#42-text-input)
   - [4.3 Key Operations](#43-key-operations)
   - [4.4 App Operations](#44-app-operations)
   - [4.5 Screen Capture and Analysis](#45-screen-capture-and-analysis)
   - [4.6 Utility Commands](#46-utility-commands)
   - [4.7 JSON Batch Processing](#47-json-batch-processing)
   - [4.8 Version Information](#48-version-information)
5. [Daemon Management](#5-daemon-management)
6. [MCP Server](#6-mcp-server)
7. [Use Cases and Best Practices](#7-use-cases-and-best-practices)
8. [Architecture Overview](#8-architecture-overview)
9. [Logging and Fault Recovery](#9-logging-and-fault-recovery)
10. [Appendix: Frequently Asked Questions](#10-appendix-frequently-asked-questions)

---

## 1. Installation and Build

### Prerequisites

| Dependency | Version Requirement | Purpose |
|------------|-------------------|---------|
| Go | 1.21+ | Build toolchain |
| `adb` | — | Android device communication |
| `git` | — | Automatic version info injection |
| FFmpeg static libraries | 7.1 (default) | Video decoding (required for CGO mode) |
| `nasm` | Optional | x86 FFmpeg asm optimization |
| `zig` | Optional | Non-native cross-compilation |
| `upx` | Optional | Binary size compression |

### Build

```bash
# Clone the repository
git clone https://github.com/gezihua123/phonefast.git
cd phonefast

# CGO build (recommended, hardware-accelerated video decoding, requires FFmpeg static libs)
bash scripts/build.sh                            # Auto-download FFmpeg + build (plain, 24MB)

# Self-contained build (embeds ONNX Runtime, 42MB, zero external deps)
bash scripts/build.sh --full                     # Also build -full variant
bash scripts/build.sh --full-only                # Only build -full

# Non-CGO build (no FFmpeg needed, uses subprocess for video decoding)
CGO_ENABLED=0 go build -o phonefast ./cmd/phonefast/

# Cross-platform builds
bash scripts/build.sh --all                      # Cross-compile for all platforms
bash scripts/build.sh --macos                    # macOS arm64
bash scripts/build.sh --linux                    # Linux amd64 + arm64
bash scripts/build.sh --windows                  # Windows amd64
bash scripts/build.sh --all --version 1.0.0      # Specify version number
bash scripts/build.sh --all --full               # All platforms + self-contained -full

# Python build tool (scripts/build.sh is a thin wrapper)
python3 scripts/build.py --full                  # Equivalent to above
python3 scripts/build.py --all --ensure-ffmpeg   # Compile FFmpeg libs if missing
```

> **OCR variants:**
> - **plain** (24MB): PP-OCR v3 models embedded; ONNX Runtime loaded from system
>   (`brew install onnxruntime` on macOS, `apt install libonnxruntime` on Linux).
> - **-full** (42MB, macOS arm64 only): embeds ONNX Runtime — single-file,
>   zero-dependency, works on any Mac without brew.
> - Both variants support `phonefast ocr` out of the box.
> - NCNN engine is opt-in (`-tags ncnn`), see [docs/DEV.md](DEV.md#ocr-识别方案调研与选型).

### Preparing FFmpeg Static Libraries

CGO builds require FFmpeg static libraries (linked into the binary, no system `ffmpeg` command needed).

```bash
# One-click setup (download prebuilt libraries, fall back to source compilation on failure)
bash scripts/download-ffmpeg.sh                    # Current platform
bash scripts/download-ffmpeg.sh x86_64-linux-gnu   # Specific target
bash scripts/download-ffmpeg.sh --all               # All platforms

# Build from source (alternative, ~5-15 minutes per target)
bash scripts/cross-build-ffmpeg.sh aarch64-darwin

# Manually specify FFmpeg path (skip script)
export PKG_CONFIG_PATH="$(pwd)/build/cross-ffmpeg/aarch64-darwin/lib/pkgconfig"
CGO_ENABLED=1 go build -o phonefast ./cmd/phonefast/
```

### Artifact Structure

```
dist/<version>/
├── <platform>/
│   ├── phonefast                  # CLI binary
│   ├── phonefast.exe              # (Windows)
│   ├── scrcpy-server.jar          # scrcpy server (Android side)
│   ├── scrcpy-server.version      # Version marker file
│   ├── README.md                  # Documentation
│   └── docs/                      # Detailed documentation
└── <platform>/
    └── phonefast-<version>-<os>-<arch>.tar.gz     # Release package
```

### Manual Installation

```bash
# Place the binary in your PATH
cp dist/<version>/darwin_arm64/phonefast /usr/local/bin/

# Copy dependency files
mkdir -p /usr/local/share/phonefast
cp dist/<version>/darwin_arm64/scrcpy-server.jar /usr/local/share/phonefast/
cp dist/<version>/darwin_arm64/scrcpy-server.version /usr/local/share/phonefast/
```

---

## 2. Quick Start

```bash
# 1. Connect your Android device (USB debugging enabled)
# 2. Verify the device is connected
phonefast devices

# 3. Execute actions (Daemon mode, <10ms latency)
phonefast tap 540 960                # Tap the center of the screen
phonefast back                        # Go back
phonefast screenshot /tmp/screen.png  # Take a screenshot
phonefast observe                     # Screenshot + UI elements

# 4. Or use direct mode (creates a new connection each time, ~2.5s)
phonefast --foreground tap 540 960

# 5. Start the MCP server (for AI assistant use)
phonefast serve
```

---

## 3. Mode Flags

### Format

```bash
phonefast [--foreground|--daemon] [--serial <SERIAL> | -s <SERIAL>] <command> [args...]
```

### Flag Descriptions

| Flag | Alias | Description | Default |
|------|-------|-------------|---------|
| `--daemon` | — | Daemon mode, persistent background process (default behavior) | ✓ |
| `--foreground` | `--direct` | Direct mode, creates a new scrcpy connection each time | — |
| `--serial` | `-s` | Specify target device serial (required for multiple devices) | Auto-detect |

### Mode Comparison

| Dimension | Daemon Mode | Direct Mode |
|-----------|-------------|-------------|
| Command format | `phonefast <cmd>` (default) | `phonefast --foreground <cmd>` |
| Response speed | <10ms | ~2.5s |
| Resource usage | One persistent daemon process in background | Creates/destroys connection each time |
| Use case | Batch operations, script automation, AI Agent high-frequency loops | Occasional one-off operations |
| Self-management | Auto-start, auto-restart, automatic reconnection on disconnect | Stateless |

### Multi-Device Management

A single daemon process serves all connected devices; each request is routed to the target device via its `device` field. Use `-s` (or `--serial`) to select a device — the flag is per-command, not per-daemon:

```bash
phonefast -s 13709314CF044927 tap 540 960
phonefast --serial R3CNB0000000XYZ screenshot /tmp/s.png
```

Without `-s`, the first connected device (per ADB order) is used.


---

## 4. Command Reference

### 4.1 Touch Operations

#### `tap` — Tap at Coordinates

```bash
phonefast [--foreground|--daemon] tap <x> <y>
```

| Parameter | Description | Required |
|-----------|-------------|----------|
| `x` | X coordinate (pixels) | ✓ |
| `y` | Y coordinate (pixels) | ✓ |

**Examples:**
```bash
phonefast tap 540 960                  # tap screen center
phonefast --foreground tap 244 540     # direct mode
```

---

#### `tap_element` — Tap a UI Element

```bash
phonefast [--foreground|--daemon] tap_element <index|text>
```

| Parameter | Type | Description | Required |
|-----------|------|-------------|----------|
| `index` | Number | UI element index (from `ui` command) | One of two |
| `text` | String | UI element text or description (fuzzy search) | One of two |

```bash
phonefast tap_element 5              # by index (from [N] in `ui` output)
phonefast tap_element "Settings"     # by text (fuzzy, case-insensitive)
```

**Notes:**
- Index mode: First use `ui` or `observe` to get the current UI element list and find the corresponding element index
- Text mode: Fuzzy matches the element's `text` and `content-desc` attributes, matches the first element
- Text matching is case-insensitive

---

#### `swipe` — Swipe Gesture

```bash
phonefast [--foreground|--daemon] swipe <x1> <y1> <x2> <y2> [duration_ms]
```

| Parameter | Description | Default |
|-----------|-------------|---------|
| `x1` `y1` | Start coordinates (pixels) | — |
| `x2` `y2` | End coordinates (pixels) | — |
| `duration_ms` | Swipe duration (milliseconds) | `500` |

```bash
phonefast swipe 540 1600 540 400        # swipe up
phonefast swipe 200 500 800 500 300     # fast swipe right (300ms)
```

---

### 4.2 Text Input

#### `type` / `text` — Input Text

```bash
phonefast [--foreground|--daemon] type <text>
```

Types text into the currently focused input field.

| Parameter | Description | Required |
|-----------|-------------|----------|
| `text` | Text content to input | ✓ |

**Examples:**
```bash
phonefast type "Hello World"
phonefast type "搜索关键词"
phonefast type "user@example.com"
```

**Output:**
```
Typed: Hello World
```

**Notes:**
- Ensure the target input field has focus before typing (you can `tap` the input field first)
- Text is simulated character by character as key events, supporting letters, numbers, Chinese characters, etc.

---

### 4.3 Key Operations

#### `back` / `home` — System Keys

```bash
phonefast back     # KEYCODE_BACK (4)
phonefast home     # KEYCODE_HOME (3), returns to home screen
```

#### `key` / `press_key` — Send a Key Event

```bash
phonefast key <keyname|keycode>
```

Supports both key name and numeric keycode.

**Supported Key Names:**

| Category | Key Name | Description | Keycode |
|----------|----------|-------------|---------|
| Navigation | `back` | Back | 4 |
| | `home` | Home | 3 |
| | `menu` | Menu | 82 |
| | `search` | Search | 84 |
| Input | `enter` | Enter | 66 |
| | `tab` | Tab | 61 |
| | `delete` / `backspace` | Delete | 67 |
| | `space` | Space | 62 |
| | `escape` / `esc` | Escape | 111 |
| Volume | `volume_up` | Volume Up | 24 |
| | `volume_down` | Volume Down | 25 |
| | `volume_mute` | Mute | 164 |
| System | `power` | Power | 26 |
| | `camera` | Camera | 27 |
| Directional | `dpad_up` | D-pad Up | 19 |
| | `dpad_down` | D-pad Down | 20 |
| | `dpad_left` | D-pad Left | 21 |
| | `dpad_right` | D-pad Right | 22 |
| | `dpad_center` | D-pad Center | 23 |
| Page | `page_up` | Page Up | 92 |
| | `page_down` | Page Down | 93 |
| Media | `media_play_pause` | Play/Pause | 85 |
| | `media_stop` | Stop | 86 |
| | `media_next` | Next Track | 87 |
| | `media_previous` | Previous Track | 88 |

```bash
phonefast key enter        # by name
phonefast key 66           # by keycode (ENTER)
```

---

### 4.4 App Operations

#### `launch` — Launch an App

```bash
phonefast [--foreground|--daemon] launch <package>
```

| Parameter | Description | Required |
|-----------|-------------|----------|
| `package` | Android application package name | ✓ |

**Common App Package Names:**

| App | Package Name |
|-----|-------------|
| System Settings | `com.android.settings` |
| Chrome | `com.android.chrome` |
| WeChat | `com.tencent.mm` |
| Alipay | `com.eg.android.AlipayGphone` |
| Taobao | `com.taobao.taobao` |
| Douyin (TikTok) | `com.ss.android.ugc.aweme` |
| Xiaohongshu (RedNote) | `com.xingin.xhs` |

**Examples:**
```bash
phonefast launch com.android.settings
phonefast launch com.tencent.mm
phonefast launch com.android.chrome
```

**Output:**
```
Launched: com.android.settings
```

**Notes:**
- App display names (e.g. "Settings", "Chrome") are not supported; you must use the Android package name
- Use `adb shell pm list packages | grep <keyword>` to find package names

---

### 4.5 Screen Capture and Analysis

#### `screenshot` — Take a Screenshot

```bash
phonefast [--foreground|--daemon] screenshot [file]
```

| Parameter | Description | Default | Required |
|-----------|-------------|---------|----------|
| `file` | Save path | Omit to output base64 to stdout | Optional |

**Screenshot Mechanism:** Extracts a keyframe (I-frame) from the H.264 video stream and transcodes it to PNG via `ffmpeg`.

**Examples:**
```bash
# Save to file
phonefast screenshot /tmp/screen.png

# Output base64 to stdout (usable with pipes or scripts)
phonefast screenshot

# Use with tools
phonefast screenshot | base64 -d > screen.png
```

**Output (file mode):**
```
Screenshot saved to /tmp/screen.png
```

**Output (base64 mode):**
```
data:image/png;base64,iVBORw0KGgoAAAANS...
```

---

#### `ui` — UI Element List

```bash
phonefast [--foreground|--daemon] ui [max_elements] [--summary] [--format <fmt>]
```

Retrieves the hierarchical information of UI elements on the current screen.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `max_elements` | Maximum number of elements to display | `100` |
| `--summary` | Summary mode, filters purely structural layout elements | — |
| `--format` | Output format: `flat` (default), `flatref`, `jsonl`, `simplexml`, `yml` | `flat` |

**Examples:**
```bash
# Flat format (default)
phonefast ui

# Hierarchical reference format (flatref, each line self-contained with parent reference)
phonefast ui --format flatref

# JSON Lines format (precise LLM parsing)
phonefast ui --format jsonl

# Summary mode
phonefast ui --summary
```

**flatref Format (Recommended for AI Agents):**

flatref is a hierarchical format designed specifically for LLMs. Each line represents one element, using `|` to separate four semantic columns:

```
#N <identity> | bounds=[l,t][r,b] | [state] | depth=N parent=#M
```

```
#0 (FrameLayout) | bounds=[0,0][1080,2400] | | depth=0 parent=#-1
#19 id="back_btn" (ImageButton) | bounds=[0,0][96,96] | [clickable] | depth=3 parent=#18
#21 text="安装" (TextView) | bounds=[899,432][975,491] | | depth=4 parent=#20
```

| Column | Content | Description |
|--------|---------|-------------|
| Identity | `#N text="..." desc="..." id="..." (Class)` | What the element is |
| Position | `bounds=[l,t][r,b]` | Where the element is |
| State | `[clickable] [focused] [selected] [disabled]` | Whether it is interactive |
| Hierarchy | `depth=N parent=#M` | Where it is in the tree |

**Other Hierarchical Formats:**

| Format | Features | Best For |
|--------|----------|----------|
| `flatref` | `|` separated four columns, most token-efficient | AI Agent daily use |
| `jsonl` | Independent JSON per line, highest baseline accuracy | Precise structured parsing |
| `simplexml` | Nested XML, good readability | Human reading |
| `yml` | YAML indented hierarchy | Config file style |
| `flat` | Traditional flat format (default) | Backward compatibility |

**Field Descriptions:**

| Field | Description |
|-------|-------------|
| `#N` | Element ID (used for `parent=#N` references) |
| `text="..."` | Element text |
| `desc="..."` | Accessibility description (content-desc) |
| `id="..."` | Resource ID (last segment only) |
| `(ClassName)` | Element class name (simplified) |
| `[clickable]` | Clickable flag |
| `[focused]` | Focused flag |
| `[selected]` | Selected flag |
| `[disabled]` | Disabled flag |
| `bounds=[l,t][r,b]` | Bounding coordinates (top-left, bottom-right) |
| `depth=N` | Hierarchical depth (0=root) |
| `parent=#M` | Parent node ID reference |

---

#### `observe` — Screenshot + UI (Atomic Operation)

```bash
phonefast [--foreground|--daemon] observe [max_elements] [--summary]
```

Concurrently captures a screenshot and UI element data, obtaining a complete screen state snapshot in a single call.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `max_elements` | Maximum number of elements to display | `100` |
| `--summary` | Summary mode | — |

**Differences from separate `screenshot` + `ui` calls:**

| Aspect | `observe` | `screenshot` + `ui` |
|--------|-----------|---------------------|
| Atomicity | ✓ Screenshot and UI captured simultaneously | Has a race condition time window |
| API calls | 1 call | 2 calls |
| Latency | ~148ms | ~167ms + ~191ms |

**Examples:**
```bash
# Full observation (screenshot + UI returned together)
phonefast observe

# Summary mode
phonefast observe --summary

# Show only 20 elements
phonefast observe 20
```

**Output:** Contains a screenshot (typically output as a base64 data URI) and a UI element list.

---

### 4.6 Utility Commands

#### `wait` — Wait

```bash
phonefast wait <ms>
```

A pure local sleep — not routed through the daemon, so it never blocks a device's actor or affects other devices.

Inserts a wait time into a sequence of operations, commonly used to wait for page loading or animations to complete.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `ms` | Wait duration in milliseconds | `1000` |

**Examples:**
```bash
# Wait 1 second (default)
phonefast wait

# Wait 3 seconds
phonefast wait 3000

# Use between operations
phonefast tap 540 960 && phonefast wait 2000 && phonefast tap 540 960
```

**Output:**
```
Waited 2000ms
```

---

#### `help` — Show Help

```bash
phonefast help
phonefast --help
phonefast -h
```

Displays the command list and usage instructions.

**Examples:**
```bash
phonefast help
phonefast --help
```

**Output:**
```
phonefast — Fast Android device control

Commands (default: daemon mode, auto-starts daemon, <10ms):
  phonefast tap <x> <y>                     Tap at coordinates
  ...
```

---

#### `status` — Show Daemon Status

```bash
phonefast [--foreground|--daemon] status
```

**Examples:**
```bash
phonefast status
```

**Output (Daemon mode):**
```json
{
  "connected": true,
  "control_available": true,
  "device_height": 1080,
  "device_width": 488,
  "serial": "13709314CF044927",
  "ui_available": true
}
```

**Output (Direct mode):**
```
daemon running (pid 60977)
  device:    13709314CF044927 (488x1080)
  control:   true
  ui:        true
```

---

#### `devices` — List Devices

```bash
phonefast devices
```

**Examples:**
```bash
phonefast devices
```

**Output:**
```
Connected devices:
  13709314CF044927  device  [TECNO_KL8h]
  R3CNB0000000XYZ   device  [Pixel_6]
```

**Field Descriptions:**

| Field | Description |
|-------|-------------|
| `Serial` | Device serial number (used with `--serial` flag) |
| `Status` | Connection status: `device` (normal), `offline`, `unauthorized` |
| `Model` | Device model |

---

#### `connect` / `disconnect` — Device Connection Management

> **Note:** The `connect` and `disconnect` commands are deprecated. Use `daemon --stop` instead.

```bash
# Stop the current daemon connection
phonefast daemon --stop

# Reconnect (start daemon); select a device per-command with -s
phonefast daemon
phonefast -s 13709314CF044927 tap 540 960
```

---

### 4.7 JSON Batch Processing

#### `run` — JSON-Based Single Operation

```bash
phonefast [--foreground|--daemon] run '<json>'
```

Specifies an operation in JSON format, suitable for scripted automation.

| Parameter | Description | Required |
|-----------|-------------|----------|
| `json` | JSON object or array | ✓ |

**Single operation:**
```bash
phonefast run '{"action":"tap","args":{"x":540,"y":960}}'
phonefast run '{"action":"screenshot"}'
phonefast run '{"action":"back"}'
phonefast run '{"action":"home"}'
phonefast run '{"action":"type_text","args":{"text":"Hello"}}'
phonefast run '{"action":"swipe","args":{"start_x":540,"start_y":1600,"end_x":540,"end_y":400,"duration_ms":500}}'
phonefast run '{"action":"press_key","args":{"key":"enter"}}'
phonefast run '{"action":"press_key","args":{"keycode":66}}'
phonefast run '{"action":"launch_app","args":{"package":"com.android.settings"}}'
phonefast run '{"action":"list_devices"}'
phonefast run '{"action":"wait","args":{"duration_ms":2000}}'
```

**Batch Processing (JSON Array):**

Executes multiple operations sequentially:

```bash
phonefast run '[
  {"action":"launch_app","args":{"package":"com.android.settings"}},
  {"action":"wait","args":{"duration_ms":2000}},
  {"action":"screenshot","args":{}},
  {"action":"back"}
]'
```

**Supported Actions:**

| Action | Parameters | Description |
|--------|------------|-------------|
| `tap` | `x`, `y` | Tap at coordinates |
| `tap_element` | `index` or `text` | Tap a UI element |
| `swipe` | `start_x`, `start_y`, `end_x`, `end_y`, `duration_ms` | Swipe |
| `back` | — | Back |
| `home` | — | Home |
| `type_text` | `text` | Input text |
| `press_key` | `keycode` or `key` | Key press |
| `launch_app` | `package` (or `app`) | Launch app |
| `screenshot` | — | Take screenshot |
| `get_ui_elements` | — | UI elements |
| `observe` | — | Screenshot + UI |
| `list_devices` | — | List devices |
| `wait` | `duration_ms` | Wait |

**Flattened Parameter Shorthand:**

If `args` is absent, the tool automatically reads parameters from the JSON top level:

```bash
# Full form
phonefast run '{"action":"tap","args":{"x":540,"y":960}}'

# Equivalent shorthand
phonefast run '{"action":"tap","x":540,"y":960}'
```

---

### 4.8 Version Information

```bash
phonefast --version
phonefast -v
```

**Output:**
```
phonefast 1.0.1 (commit a1b2c3d4, built 2026-07-01T10:00:00Z)
```

---

## 5. Daemon Management

The Daemon is the core mechanism of phonefast: a single persistent background process that serves all connected devices, receives JSON-RPC requests via a Unix socket, and achieves sub-10ms command latency. It does not bind to any device at startup — a per-device session (DeviceActor) is created lazily on the first request that targets that device, and reused thereafter.

### Start and Stop

```bash
# Start the daemon (background)
phonefast daemon

# Run in foreground (view real-time logs on stdout)
phonefast daemon --foreground

# Check daemon status
phonefast daemon --status

# Stop the daemon
phonefast daemon --stop
```

> Device selection is per-command via the top-level `-s`/`--serial` flag (see [Multi-Device Management](#multi-device-management)). The `daemon` subcommand no longer takes `--serial` (accepted for backward compat but ignored) or `--socket` (the unified daemon uses a single fixed socket).

### Auto-Management

The daemon features comprehensive self-management:

1. **Auto-start** — When any command is issued and the daemon is not running, it automatically starts in the background
2. **Auto-recovery** — If the daemon process exists but is unresponsive, it is automatically killed and restarted
3. **Duplicate prevention** — Multiple calls to `phonefast daemon` will not start duplicate instances (exits with a message if already running)

### Startup Sequence

When the CLI detects that the daemon is not running, it automatically performs the following steps:

```
① Check PID file → ② Clean up residual files → ③ Fork child process
④ Wait for Unix Socket readiness → ⑤ Execute command
```

The daemon starts empty (no device connection); the target device connects on the first request that uses it. Startup timeout is approximately 8 seconds.

### File Paths

The unified daemon uses a single socket/PID file shared by all devices (the target device is selected per-request, not per-socket):

| File | Path |
|------|------|
| PID file | `/tmp/phonefast-{uid}.pid` |
| Socket | `/tmp/phonefast-{uid}.sock` |

`{uid}` is the current user ID (`os.Getuid()`), isolating daemons of different users.

---

## 6. MCP Server

phonefast can act as an MCP (Model Context Protocol) server, allowing AI assistants such as Claude Desktop to control the phone directly.

### Starting the Server

```bash
# SSE mode (default), auto-detect device
phonefast serve

# Target a specific device (per-request routing, same as CLI -s)
phonefast serve -s 13709314CF044927
phonefast -s 13709314CF044927 serve          # global -s also works

# Custom port
phonefast serve --port 8080

# Custom path
phonefast serve --path /mcp

# STDIO mode (for Claude Desktop integration)
phonefast serve --transport stdio

# Custom listen address
phonefast serve --host 127.0.0.1 --port 8019
```

| Flag | Description | Default |
|------|-------------|---------|
| `--transport` / `-t` | Transport mode: `sse` or `stdio` | `sse` |
| `--port` / `-p` | Port number | `8019` |
| `--host` / `-H` | Listen address | `0.0.0.0` |
| `--path` | URL path prefix | `/Phone` |
| `--serial` / `-s` | Target device serial | Auto-detect |

The MCP server routes every tool call through the unified daemon (it does not hold its own device session). If the daemon crashes mid-session, `phonefast serve` restarts it automatically and retries the failed call.

### Mode Description

| Mode | How to Start | Use Case |
|------|-------------|----------|
| SSE | `phonefast serve` | Remote connection, custom clients |
| STDIO | `phonefast serve --transport stdio` | Claude Desktop integration |

### Client Configuration

**SSE Mode (MCP configuration):**
```json
{
  "mcpServers": {
    "phonefast": {
      "url": "http://localhost:8019/Phone/sse"
    }
  }
}
```

**STDIO Mode (Claude Desktop configuration):**
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
| `list_devices` | — | List connected devices |
| `screenshot` | — | Take screenshot (returns native ImageContent) |
| `get_ui_elements` | `format` (flat/flatref/jsonl/simplexml/yml), `max_elements` | Get UI hierarchy (multiple formats) |
| `observe` | — | Screenshot + UI atomic operation |
| `tap` | `x`, `y` | Tap at coordinates |
| `tap_element` | `index` or `text` | Tap a UI element |
| `swipe` | `start_x`, `start_y`, `end_x`, `end_y`, `duration_ms` | Swipe gesture |
| `type_text` | `text` | Input text |
| `back` | — | Back key |
| `home` | — | Home key |
| `press_key` | `keycode` or `key` | Key event |
| `launch_app` | `package` | Launch app (package name) |
| `wait` | `duration_ms` | Wait |

---

## 7. Use Cases and Best Practices

### AI Agent Interaction Loop

The typical loop: Observe (screenshot + UI) → Analyze → Act → Re-observe.

```bash
phonefast observe                       # 1. observe
phonefast tap 540 960                   # 2. act
phonefast wait 1500                     #    wait for animation
phonefast observe                       # 3. confirm result
```

### JSON Batch Workflow

```bash
phonefast run '[
  {"action":"launch_app","args":{"package":"com.android.chrome"}},
  {"action":"wait","args":{"duration_ms":3000}},
  {"action":"type_text","args":{"text":"hello world"}},
  {"action":"screenshot"},
  {"action":"home"}
]'
```

### Multi-Device

```bash
phonefast -s DEVICE_A tap 540 960       # terminal 1
phonefast -s DEVICE_B tap 100 200       # terminal 2 (parallel, isolated)
```

### Best Practices

1. **Daemon mode by default** — auto-start, <10ms, auto-recovery
2. **`wait` between operations** — allow page load / animation (1-3s)
3. **Prefer `observe` over `screenshot` + `ui`** — atomic, no race; prefer `tap_element` over raw coordinates

---

## 8. Architecture Overview

```
phonefast CLI
    │
    ├── Daemon Mode ──→ Unix Socket ──→ Daemon Process ──→ TCP ──→ scrcpy-server (device side)
    │                   JSON-RPC           per-device actor        Control+Video+UI
    │
    └── Direct Mode ──→ New session each time ──→ TCP ──→ scrcpy-server (device side)
                          Deploy+Start+Connect+Close
```

| Module | Path | Function |
|--------|------|----------|
| CLI | `cmd/phonefast/main.go` | Command parsing, dispatch, mode selection |
| Daemon | `internal/daemon/` | Unified daemon, JSON-RPC, per-device actors, health/recovery |
| MCP | `internal/mcp/` | MCP server (SSE/STDIO), routes tool calls via daemon |
| Session | `internal/session/` | Device session: video stream, control, UI, screenshot |
| ADB | `internal/adb/` | Device discovery, scrcpy deployment/lifecycle |
| Protocol | `pkg/protocol/` | scrcpy protocol encoding, control messages |

> Architecture deep-dive, screenshot pipeline (astiav CGO / ffmpeg fallback), and build details → [docs/DEV.md](DEV.md)

---

## 9. Logging and Fault Recovery

### Async Logging

Logs are written asynchronously to `/tmp/phonefast-{uid}.log`, recording all critical operations.

**Log format:**
```
09:13:56.879 [session.go:139 Connect()] connected: 488x1080  control=true
09:13:59.602 [rpc.go:115 Dispatch()] rpc back
09:13:59.603 [control.go:138 Back()] back
09:13:59.624 [control.go:38 Tap()] tap (244,540)
09:13:59.952 [control.go:93 Swipe()] swipe (200,900)→(200,300) 300ms
09:14:26.000 [daemon.go:328 healthLoop()] health: connection dead, reconnecting...
09:14:29.000 [daemon.go:298 reconnect()] reconnected: 13709314CF044927 (488x1080)
```

### Three-Tier Keepalive Mechanism

| Layer | Mechanism | Interval | Description |
|-------|-----------|----------|-------------|
| 1. OS level | TCP Keepalive | Video 30s / Control 15s | OS automatically detects dead connections |
| 2. App level | `healthLoop` goroutine | 10s poll | Detects video+control status, auto-reconnects |
| 3. Write trigger | `markControlBroken` | Immediate | Marks on write failure; next request auto-reconnects and retries |

When the device USB is disconnected or scrcpy-server is killed, the daemon automatically detects and recovers the connection within **10 seconds**.

### Fault Recovery Scenarios

| Failure | Recovery Behavior | Recovery Time |
|---------|-------------------|---------------|
| USB disconnected and reconnected | Auto-reconnect scrcpy | <10s |
| scrcpy-server crash | Daemon auto-restarts scrcpy | <10s |
| Daemon process crash | CLI auto-restarts daemon | <8s |
| UI socket timeout | Auto-recovery on next call | Instant |
| TCP broken pipe | Daemon auto-reconnects | <10s |

---

## 10. Appendix: Frequently Asked Questions

### 1. How do I check if a device is connected?

```bash
phonefast devices
```

Output `device` indicates an authorized connection; `unauthorized` means USB debugging authorization must be confirmed on the phone.

### 2. What should I do if the daemon fails to start?

- **Device not connected / ADB not authorized** — `phonefast devices` to check; confirm USB debugging on the phone
- **scrcpy-server.jar missing** — ensure dependency files are in place
- Retry: `adb kill-server && adb start-server && phonefast daemon`

### 3. `tap_element` cannot find the element?

- Use `phonefast ui` first to see what's on screen
- Text search is fuzzy and case-insensitive; check spelling
- Some non-standard views may not be captured

### 4. How do I find an app package name?

```bash
adb shell pm list packages | grep -i wechat
```

---

> For more information, visit the [GitHub repository](https://github.com/gezihua123/phonefast)
