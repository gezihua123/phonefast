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
| Both visual + element positions | `phonefast observe` | ~148ms |
| Visual only (show user) | `phonefast screenshot <path>` | ~167ms |
| Elements only (flat list) | `phonefast ui` | ~191ms |
| Elements with hierarchy info | `phonefast ui --format flatref` | ~191ms |

**When to use each:**
- `observe` → New/unknown screen, need to locate elements, confirm action result
- `screenshot` → User asked for an image, or just need a quick visual check
- `ui` → Simple screen layout, find elements by index
- `ui --format flatref` → Complex nested layout, need parent-child relationships, identify interactive vs decorative elements
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

> **Common packages**: `com.android.settings`, `com.tencent.mm`(微信), `com.taobao.taobao`, `com.ss.android.ugc.aweme`(抖音).

**完整 Android Keycode 参考表**（按功能分类）：

#### 系统/导航键
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_HOME` | 3 | Home 键 |
| `KEYCODE_BACK` | 4 | 返回键 |
| `KEYCODE_APP_SWITCH` | 187 | 最近应用/多任务 |
| `KEYCODE_MENU` | 82 | 菜单键 |
| `KEYCODE_NOTIFICATION` | 188 | 通知面板 |
| `KEYCODE_SEARCH` | 84 | 搜索键 |
| `KEYCODE_SYSTEM_NAVIGATION_UP` | 280 | 系统导航上 |
| `KEYCODE_SYSTEM_NAVIGATION_DOWN` | 281 | 系统导航下 |
| `KEYCODE_SYSTEM_NAVIGATION_LEFT` | 282 | 系统导航左 |
| `KEYCODE_SYSTEM_NAVIGATION_RIGHT` | 283 | 系统导航右 |
| `KEYCODE_ALL_APPS` | 284 | 所有应用 |
| `KEYCODE_ASSIST` | 219 | Google Assistant |

#### 电源/屏幕控制
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_POWER` | 26 | 电源键 |
| `KEYCODE_SLEEP` | 276 | 休眠 |
| `KEYCODE_WAKEUP` | 224 | 唤醒 |
| `KEYCODE_BRIGHTNESS_DOWN` | 220 | 亮度降低 |
| `KEYCODE_BRIGHTNESS_UP` | 221 | 亮度提高 |

#### 音量/音频
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_VOLUME_UP` | 24 | 音量+ |
| `KEYCODE_VOLUME_DOWN` | 25 | 音量- |
| `KEYCODE_VOLUME_MUTE` | 164 | 音量静音 |
| `KEYCODE_MUTE` | 91 | 麦克风静音 |
| `KEYCODE_MEDIA_AUDIO_TRACK` | 222 | 切换音轨 |

#### 媒体控制
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_MEDIA_PLAY_PAUSE` | 85 | 播放/暂停 |
| `KEYCODE_MEDIA_STOP` | 86 | 停止 |
| `KEYCODE_MEDIA_NEXT` | 87 | 下一曲 |
| `KEYCODE_MEDIA_PREVIOUS` | 88 | 上一曲 |
| `KEYCODE_MEDIA_REWIND` | 89 | 快退 |
| `KEYCODE_MEDIA_FAST_FORWARD` | 90 | 快进 |
| `KEYCODE_MEDIA_PLAY` | 126 | 播放 |
| `KEYCODE_MEDIA_PAUSE` | 127 | 暂停 |
| `KEYCODE_MEDIA_CLOSE` | 128 | 关闭媒体 |
| `KEYCODE_MEDIA_EJECT` | 129 | 弹出 |
| `KEYCODE_MEDIA_RECORD` | 130 | 录制 |
| `KEYCODE_MEDIA_TOP_MENU` | 226 | 媒体顶层菜单 |
| `KEYCODE_MEDIA_TIME` | 260 | 媒体时间显示 |
| `KEYCODE_MEDIA_SKIP_FORWARD` | 272 | 向前跳过 |
| `KEYCODE_MEDIA_SKIP_BACKWARD` | 273 | 向后跳过 |
| `KEYCODE_MEDIA_STEP_FORWARD` | 275 | 向前单步 |
| `KEYCODE_MEDIA_STEP_BACKWARD` | 274 | 向后单步 |

#### 方向键 (DPAD)
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_DPAD_UP` | 19 | 上 |
| `KEYCODE_DPAD_DOWN` | 20 | 下 |
| `KEYCODE_DPAD_LEFT` | 21 | 左 |
| `KEYCODE_DPAD_RIGHT` | 22 | 右 |
| `KEYCODE_DPAD_CENTER` | 23 | 确认/选中 |

#### 电话键
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_CALL` | 5 | 拨号/接听 |
| `KEYCODE_ENDCALL` | 6 | 挂断/拒接 |

#### 字母键 (A–Z)
| Keycode | 值 | Keycode | 值 | Keycode | 值 |
|---------|----|---------|----|---------|----|
| `KEYCODE_A` | 29 | `KEYCODE_J` | 36 | `KEYCODE_S` | 47 |
| `KEYCODE_B` | 30 | `KEYCODE_K` | 37 | `KEYCODE_T` | 48 |
| `KEYCODE_C` | 31 | `KEYCODE_L` | 38 | `KEYCODE_U` | 49 |
| `KEYCODE_D` | 32 | `KEYCODE_M` | 39 | `KEYCODE_V` | 50 |
| `KEYCODE_E` | 33 | `KEYCODE_N` | 40 | `KEYCODE_W` | 51 |
| `KEYCODE_F` | 34 | `KEYCODE_O` | 41 | `KEYCODE_X` | 52 |
| `KEYCODE_G` | 35 | `KEYCODE_P` | 42 | `KEYCODE_Y` | 53 |
| `KEYCODE_H` | 36 | `KEYCODE_Q` | 43 | `KEYCODE_Z` | 54 |
| `KEYCODE_I` | 37 | `KEYCODE_R` | 44 | | |

#### 数字键
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_0` | 7 | 数字 0 |
| `KEYCODE_1` | 8 | 数字 1 |
| `KEYCODE_2` | 9 | 数字 2 |
| `KEYCODE_3` | 10 | 数字 3 |
| `KEYCODE_4` | 11 | 数字 4 |
| `KEYCODE_5` | 12 | 数字 5 |
| `KEYCODE_6` | 13 | 数字 6 |
| `KEYCODE_7` | 14 | 数字 7 |
| `KEYCODE_8` | 15 | 数字 8 |
| `KEYCODE_9` | 16 | 数字 9 |

#### 符号键
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_STAR` | 17 | `*` |
| `KEYCODE_POUND` | 18 | `#` |
| `KEYCODE_COMMA` | 55 | `,` |
| `KEYCODE_PERIOD` | 56 | `.` |
| `KEYCODE_SPACE` | 62 | 空格 |
| `KEYCODE_GRAVE` | 68 | `` ` `` |
| `KEYCODE_MINUS` | 69 | `-` |
| `KEYCODE_EQUALS` | 70 | `=` |
| `KEYCODE_LEFT_BRACKET` | 71 | `[` |
| `KEYCODE_RIGHT_BRACKET` | 72 | `]` |
| `KEYCODE_BACKSLASH` | 73 | `\` |
| `KEYCODE_SEMICOLON` | 74 | `;` |
| `KEYCODE_APOSTROPHE` | 75 | `'` |
| `KEYCODE_SLASH` | 76 | `/` |
| `KEYCODE_AT` | 77 | `@` |
| `KEYCODE_PLUS` | 228 | `+` |

#### 编辑键
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_ENTER` | 66 | 回车 |
| `KEYCODE_DEL` | 67 | 退格 (Backspace) |
| `KEYCODE_FORWARD_DEL` | 112 | 删除 (Delete) |
| `KEYCODE_TAB` | 61 | Tab |
| `KEYCODE_ESCAPE` | 111 | ESC / 收起键盘 |
| `KEYCODE_INSERT` | 124 | Insert |
| `KEYCODE_CAPS_LOCK` | 115 | Caps Lock |
| `KEYCODE_NUM_LOCK` | 143 | Num Lock |
| `KEYCODE_SCROLL_LOCK` | 116 | Scroll Lock |
| `KEYCODE_SYM` | 63 | 符号修饰键 |
| `KEYCODE_FUNCTION` | 119 | Function 修饰键 |

#### 修饰键
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_SHIFT_LEFT` | 59 | 左 Shift |
| `KEYCODE_SHIFT_RIGHT` | 60 | 右 Shift |
| `KEYCODE_ALT_LEFT` | 57 | 左 Alt |
| `KEYCODE_ALT_RIGHT` | 58 | 右 Alt |
| `KEYCODE_CTRL_LEFT` | 113 | 左 Ctrl |
| `KEYCODE_CTRL_RIGHT` | 114 | 右 Ctrl |
| `KEYCODE_META_LEFT` | 117 | 左 Meta |
| `KEYCODE_META_RIGHT` | 118 | 右 Meta |

#### 导航/翻页键
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_PAGE_UP` | 92 | Page Up |
| `KEYCODE_PAGE_DOWN` | 93 | Page Down |
| `KEYCODE_MOVE_HOME` | 122 | 移动到行首 |
| `KEYCODE_MOVE_END` | 123 | 移动到行尾 |

#### 功能键 (F1–F12)
| Keycode | 值 | Keycode | 值 |
|---------|----|---------|----|
| `KEYCODE_F1` | 131 | `KEYCODE_F7` | 137 |
| `KEYCODE_F2` | 132 | `KEYCODE_F8` | 138 |
| `KEYCODE_F3` | 133 | `KEYCODE_F9` | 139 |
| `KEYCODE_F4` | 134 | `KEYCODE_F10` | 140 |
| `KEYCODE_F5` | 135 | `KEYCODE_F11` | 141 |
| `KEYCODE_F6` | 136 | `KEYCODE_F12` | 142 |

#### 摄像头
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_CAMERA` | 27 | 拍照键 |
| `KEYCODE_FOCUS` | 80 | 对焦键 |

#### 其他
| Keycode | 值 | 说明 |
|---------|----|------|
| `KEYCODE_HEADSETHOOK` | 79 | 有线耳机按键 (接听/挂断) |
| `KEYCODE_LANGUAGE_SWITCH` | 204 | 切换输入语言 |
| `KEYCODE_BUTTON_A` | 96 | 游戏手柄 A |
| `KEYCODE_BUTTON_B` | 97 | 游戏手柄 B |
| `KEYCODE_BUTTON_C` | 98 | 游戏手柄 C |
| `KEYCODE_BUTTON_X` | 99 | 游戏手柄 X |
| `KEYCODE_BUTTON_Y` | 100 | 游戏手柄 Y |
| `KEYCODE_BUTTON_Z` | 101 | 游戏手柄 Z |
| `KEYCODE_BUTTON_L1` | 102 | 游戏手柄 L1 |
| `KEYCODE_BUTTON_R1` | 103 | 游戏手柄 R1 |
| `KEYCODE_BUTTON_L2` | 104 | 游戏手柄 L2 |
| `KEYCODE_BUTTON_R2` | 105 | 游戏手柄 R2 |
| `KEYCODE_BUTTON_THUMBL` | 106 | 左摇杆按下 |
| `KEYCODE_BUTTON_THUMBR` | 107 | 右摇杆按下 |
| `KEYCODE_BUTTON_START` | 108 | 游戏手柄 Start |
| `KEYCODE_BUTTON_SELECT` | 109 | 游戏手柄 Select |
| `KEYCODE_BUTTON_MODE` | 110 | 游戏手柄 Mode |
| `KEYCODE_AVR_POWER` | 245 | AVR 电源 |
| `KEYCODE_AVR_INPUT` | 246 | AVR 输入切换 |

> **使用示例**: `phonefast --daemon key KEYCODE_VOLUME_UP` — 增大音量；`phonefast --daemon key KEYCODE_POWER` — 锁屏/唤醒；`phonefast --daemon key KEYCODE_ESCAPE` — 收起键盘。

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

1. **Choose the right info command** — `observe` for new/unknown screens, `screenshot`/`ui` when you only need one, skip when coordinates are known.
2. **Use `--format flatref` for complex layouts** — The `|`-separated columns give full hierarchy; column 3 (`[flags]`) tells you what's interactive vs decorative.
3. **Re-observe after actions** — Confirm the screen changed as expected.
4. **Calculate tap center** — From bounds: `(left+right)/2, (top+bottom)/2`.
5. **Wait after app launches** — 1–3s before observing.
6. **Don't hardcode flows** — Read current UI tree and adapt.
7. **Skip `--daemon` flag** — Current version uses daemon by default, no flag needed.

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

### Hierarchical UI formats (--format flag)

For screens with complex nested layouts, use `--format flatref` to get full hierarchy information:

```bash
phonefast ui --format flatref          # 4-column pipe-separated hierarchy
phonefast ui --format jsonl            # JSON Lines (highest parse accuracy)
phonefast ui --format simplexml        # Nested XML
phonefast ui --format yml              # YAML tree
phonefast ui                           # Legacy flat format (default)
```

#### flatref format

Four semantic columns separated by ` | ` for unambiguous LLM parsing:

```
#N <identity> | bounds=[l,t][r,b] | [flags] | depth=N parent=#M
```

| Column | Content | Meaning |
|--------|---------|---------|
| 1 身份 | `#N text="..." desc="..." id="..." (Class)` | What the element is |
| 2 位置 | `bounds=[l,t][r,b]` | Where on screen |
| 3 状态 | `[clickable] [focused] [selected] [disabled]` | Interactive state (empty = decorative) |
| 4 层级 | `depth=N parent=#M` | Tree depth + parent node ID |

**Examples:**
```
#0 (FrameLayout) | bounds=[0,0][1080,2400] | | depth=0 parent=#-1
#19 (ImageButton) | bounds=[0,0][96,96] | [clickable] | depth=3 parent=#18
#21 text="安装" (TextView) | bounds=[899,432][975,491] | | depth=4 parent=#20
#54 (FrameLayout) | bounds=[335,480][744,750] | [clickable] [focused] | depth=8 parent=#53
```

**How to parse:**
- **Split by ` | `** to get 4 fields per line
- **`#N`** is the element ID — referenced by `parent=#N` in other elements
- **`depth=N`** — root is 0, deeper = more nested
- **`parent=#M`** — trace up to understand containment: button `#21` is inside `#20` is inside `#19`...
- **Column 3 empty** (`| |`) → the element is a layout container, not directly interactive
- **Column 3 has tags** → element is interactive; use `[clickable]` to identify tap targets
- **Center tap point**: `x=(l+r)/2, y=(t+b)/2` from `bounds=[l,t][r,b]`

**Parent chain traversal** (find what contains this element):
```
#21 text="安装" ... depth=4 parent=#20
→ #20 desc="安装" ... depth=3 parent=#19  
→ #19 ... [clickable] depth=2 parent=#17
→ #17 (LinearLayout) depth=1 parent=#0
→ #0 (FrameLayout) depth=0 parent=#-1  ← root
```
So the "安装" button is inside a clickable container `#19`.

#### jsonl format

Each element is a single valid JSON line — highest parse accuracy for LLMs:

```json
{"id":19,"parent":17,"depth":2,"class":"View","bounds":"[857,399][1017,525]","clickable":true}
{"id":20,"parent":19,"depth":3,"content_desc":"安装","class":"View","bounds":"[899,432][975,491]","clickable":false}
```

Fields: `id`, `parent`, `depth`, `text`, `content_desc`, `resource_id`, `class`, `bounds`, `clickable`, `enabled`, `focused`, `selected`.

#### Choosing a format

| Need | Format |
|------|--------|
| Simple screen, few elements | flat (default, no flag) |
| Complex nested layout, need hierarchy | `flatref` |
| Need guaranteed parse accuracy | `jsonl` |
| Human reading or debugging | `simplexml` or `yml` |

---

## Architecture (reference)

For full benchmarks and comparison: [phonefast.md](https://github.com/gezihua123/phonefast/blob/master/phonefast.md)

Key advantages over raw ADB / other tools:
- **Atomic observe** — screenshot + UI tree in one call (148ms), no race conditions
- **Daemon mode** — Unix Socket JSON-RPC, <1ms overhead per command
- **ImageContent** — MCP mode returns native `image/png`, ~50% less LLM token cost
- **99.99% reliability** — 12h stress test, 144k operations, auto-recovery
