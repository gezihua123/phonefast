---
name: phonefast
description: |
  Control Android devices through phonefast — a high-speed daemon for AI agents.
  Checks if phonefast is installed and the daemon is running; if not, auto-installs
  and starts it. Then executes the requested phonefast command (screenshot, tap,
  swipe, type_text, observe, etc.).
  Trigger keywords: 操作手机, 控制手机, 手机截图, 手机自动化, 打开App, 发消息,
  手机屏幕, 安卓手机, 安卓控制, 手机点击, 自动点击, 手机输入, 手机滑动,
  手机测试, 手机调试, 手机UI, 手机界面, 获取界面元素, 启动应用, 按返回键,
  按Home键, 手机助手, AI手机, 手机大脑, 手机agent, 手机远程控制, 自动化测试,
  app测试, 查看手机, 看看手机, 手机上有什么, 手机当前界面, 手机画面, 手机显示,
  手机操作, 手机执行, 帮我点手机, 帮我操作手机, 手机批处理, 手机批量操作,
  电话fast, phonefast, phone fast, android control, android automation,
  mobile automation, phone automation, device automation, screenshot android,
  screenshot phone, tap on phone, click on screen, swipe on device, type on phone,
  type text android, launch app android, observe phone screen,
  phone screen analysis, UI analysis android, android testing,
  android agent, mobile agent, phone agent, android debug, adb automation,
  scrcpy, mobile testing, app testing, phone MCP, android MCP, mobile MCP,
  control my phone, see my phone, what's on my phone, interact with phone,
  phone tool.
---

# phonefast — Android Device Control for AI Agents

Control Android devices through [phonefast](https://github.com/gezihua123/phonefast),
a daemon-based tool providing <30ms response time for mobile operations.

## Workflow overview

```
1. Check device connection (adb)
2. Ensure phonefast binary is ready
3. Understand the screen (observe / screenshot / get_ui_elements)
4. Execute action (tap / swipe / type / key / launch)
5. Confirm result (observe again if needed)
```

---

## Step-by-step

### 1. Check device

```bash
adb devices
```
- Expect at least one `device` entry.
- **No device?** → Ask user to enable USB debugging and connect. WiFi: `adb connect <ip>:5555`.
- **Multiple devices?** → phonefast controls one at a time. Disconnect extras.
- **adb not found?** → Ask user to install Android platform tools.

### 2. Ensure phonefast is ready

```bash
export PATH="$HOME/.local/bin:$PATH"
if command -v phonefast >/dev/null 2>&1; then
  echo "✓ phonefast already installed at $(which phonefast)"
else
  echo "→ phonefast not found, installing..."
  curl -fL https://raw.githubusercontent.com/gezihua123/phonefast/master/scripts/install_pkg.sh | bash -s -- --local
  echo "✓ phonefast installed"
fi
```

> `--local` installs to `~/.local/bin` (no sudo). Omit `--local` for `/usr/local/bin` (requires sudo).

### 3. Understand the screen (choose one)

| Need | Command | Latency |
|------|---------|---------|
| Both visual + element positions | `phonefast --daemon observe` | ~148ms |
| Visual only (show user) | `phonefast --daemon screenshot <path>` | ~167ms |
| Elements only (coordinates/text) | `phonefast --daemon ui` | ~191ms |

**When to use each:**
- `observe` → New/unknown screen, need to locate elements, confirm action result
- `screenshot` → User asked for an image, or you just need a quick visual check
- `get_ui_elements` → You already know the layout, just need updated coordinates or find a specific element
- **Skip all** if user gave exact coordinates or a system command (back/home/key)

### 4. Execute action

| Purpose | Command | Latency |
|---------|---------|---------|
| Tap at coordinates | `phonefast --daemon tap <x> <y>` | ~30ms |
| Swipe | `phonefast --daemon swipe <x1> <y1> <x2> <y2> <dur_ms>` | ~326ms |
| Type text | `phonefast --daemon type "<text>"` | ~13ms |
| Press Back | `phonefast --daemon back` | ~20ms |
| Press Home | `phonefast --daemon home` | ~29ms |
| Press a key | `phonefast --daemon key <keycode_name>` | ~30ms |
| Launch app | `phonefast --daemon launch <package>` | ~11ms |
| Check daemon | `phonefast --daemon status` | ~1ms |
| Stop daemon | `phonefast daemon --stop` | — |
| Show version | `phonefast --version` | — |
| Start MCP server (SSE) | `phonefast serve` | — |
| Start MCP server (STDIO) | `phonefast serve --transport stdio` | — |

> **Keycodes**: `KEYCODE_POWER`, `KEYCODE_VOLUME_UP/DOWN`, `KEYCODE_ENTER`, `KEYCODE_DEL`, `KEYCODE_MENU`.
> **Common packages**: `com.android.settings`, `com.tencent.mm`(微信), `com.taobao.taobao`, `com.ss.android.ugc.aweme`(抖音).

**Batch execution** (for known sequences):
```bash
phonefast run '[{"action": "tap", "x": 300, "y": 500}, {"action": "wait", "duration_ms": 500}]'
```

### 5. Confirm (if needed)

After screen-changing actions, run `observe` again to verify the result and get updated UI context.

---

## Scenario examples

**"看看手机"** → `screenshot` → describe screen to user
**"打开微信"** → `launch_app com.tencent.mm` → wait 2s → `observe`
**"发消息给张三说'明天见'"** → `observe` → find 张三 contact → tap → `observe` → find input → tap → `type_text "明天见"` → `observe` → find send → tap
**"滑到底部"** → `observe` → `swipe 540 2000 540 400 500` → `observe`

---

## Key rules

1. **Choose the right info command** — `observe` for new/unknown screens, `screenshot`/`get_ui_elements` when you only need one, skip when coordinates are known.
2. **Re-observe after actions** — Confirm the screen changed as expected.
3. **Calculate tap center** — From bounds: `(left+right)/2, (top+bottom)/2`.
4. **Wait after app launches** — 1–3s before observing.
5. **Don't hardcode flows** — Read current UI tree and adapt.
6. **Always use `--daemon`** — Avoids cold start overhead.

---

## Error handling

| Error | Action |
|-------|--------|
| `phonefast: command not found` | Run the install command above |
| `no device connected` | Check USB/WiFi, ask user to connect |
| `daemon not running` | `phonefast daemon` |
| Device shows "unauthorized" | User must accept RSA prompt on phone |
| `observe` timeout | Retry once; if persists, restart daemon |
| `launch_app` fails | Wrong package — ask user or suggest common ones |
| Tap succeeds but no change | Wrong coords — re-observe and recalculate |

---

## Output interpretation

- **`observe`** → Image + structured UI tree. Analyze for screen context, find elements by `text`, `bounds`, `clickable`, `resource-id`.
- **`ui`** → Elements with `bounds=[l,t,r,b]`, `text`, `content-desc`, `clickable`, `class`.
- **`screenshot [file]`** → Saved to file. `screenshot` (no args) → base64 data URI to stdout.

---

## Architecture (reference)

For full benchmarks and comparison: [phonefast.md](https://github.com/gezihua123/phonefast/blob/master/phonefast.md)

Key advantages over raw ADB / other tools:
- **Atomic observe** — screenshot + UI tree in one call (148ms), no race conditions
- **Daemon mode** — Unix Socket JSON-RPC, <1ms overhead per command
- **ImageContent** — MCP mode returns native `image/png`, ~50% less LLM token cost
- **99.99% reliability** — 12h stress test, 144k operations, auto-recovery
