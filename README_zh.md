# phonefast — Fast Android Device Control

phonefast 是一个快速 Android 设备控制命令行工具，结合 scrcpy 视频流与手机友好的操作语义，支持 MCP 协议集成与本地 CLI 两种使用方式。

**核心特性：**
- 🚀 **Daemon 模式** — 后台常驻进程，Unix Socket JSON-RPC，命令即时响应
- 📱 **直接模式** — 无 daemon，每次新建连接，适合临时操作
- 🔌 **MCP 协议** — SSE / STDIO 双传输，AI 助手可直接控制手机
- 💓 **三层保活** — TCP Keepalive + 应用心跳 + 写触发，断线 10 秒内自动重连
- 📝 **异步日志** — 协程式文件写入，记录所有关键操作与函数上下文

---

## 安装

**前置依赖：**
- Go 1.21+
- `adb` 在 PATH 中
- `ffmpeg`（截图功能需要）
- `git`（版本信息自动注入）
- `upx`（可选，压缩二进制体积）

### 构建脚本

使用统一的构建脚本 `scripts/build.sh`：

```bash
# 全平台构建 + 打包（默认）
bash scripts/build.sh --all

# 当前平台构建（仅二进制）
bash scripts/build.sh

# 指定平台
bash scripts/build.sh --macos       # macOS amd64 + arm64
bash scripts/build.sh --linux       # Linux amd64 + arm64
bash scripts/build.sh --windows     # Windows amd64

# 指定版本号（默认从 git tag 读取，无 tag 则为 "dev"）
bash scripts/build.sh --all --version 1.0.0
```

### 产物结构

```
dist/<version>/
├── <platform>/
│   ├── phonefast              # CLI 二进制
│   ├── phonefast.exe          # (Windows)
│   ├── scrcpy-server.jar      # scrcpy 服务器 (Android 端)
│   ├── scrcpy-server.version  # 版本标记文件
│   ├── README.md              # 操作文档
│   └── docs/                  # 详细文档
└── <platform>/
    └── phonefast-<version>-<os>-<arch>.tar.gz   # 发布包（--all / --macos / --linux / --windows）
```

### 构建流程

脚本自动执行以下步骤：

1. **版本检测** — 优先级：`--version` 参数 → `git describe --tags` → `"dev"`
2. **前置检查** — 确认 Go 工具链 + `android/scrcpy-server.jar` 存在
3. **Go 构建** — 交叉编译，注入版本/构建时间/commit hash 到 `-ldflags`
4. **产物组装** — 复制 `scrcpy-server.jar`、`scrcpy-server.version`、`README.md`、`docs/`
5. **压缩打包** — 生成 `.tar.gz`（macOS/Linux）或 `.zip`（Windows）发布包

### 手动安装到系统（可选）

```bash
cp dist/<version>/darwin_arm64/phonefast /usr/local/bin/
mkdir -p /usr/local/share/phonefast
cp dist/<version>/darwin_arm64/scrcpy-server.jar /usr/local/share/phonefast/
cp dist/<version>/darwin_arm64/scrcpy-server.version /usr/local/share/phonefast/
```

---

## 快速开始

```bash
# 查看连接的设备
phonefast devices

# 启动 daemon（首次自动启动，也可手动启动）
phonefast daemon

# 默认走 daemon — 即时响应（<10ms）
phonefast back
phonefast tap 540 960
phonefast screenshot /tmp/screen.png

# 直接模式 — 每次新建连接（~2.5s），加 --foreground
phonefast --foreground back
phonefast --foreground tap 540 960

# 启动 MCP 服务器（供 AI 助手使用）
phonefast serve
```

---

## 命令参考

### 格式说明

```bash
phonefast [--foreground|--daemon] <command> [args...]
```

- 默认使用 daemon 模式（自动启动 daemon），延迟 <10ms。
- `--foreground` / `--direct` — 直接模式，每次新建 scrcpy 连接，~2.5s。
- `--daemon` — 显式指定 daemon 模式（与默认行为相同，保留用于兼容）。

---

### 触摸操作

#### `tap` — 点击坐标

```bash
phonefast [--foreground|--daemon] tap <x> <y>
```

| 参数 | 描述 |
|------|------|
| `x` | X 坐标（像素） |
| `y` | Y 坐标（像素） |

```bash
phonefast tap 540 960                 # 点击屏幕中心
phonefast --foreground tap 100 200    # 直接模式
```

#### `tap_element` — 点击 UI 元素

```bash
phonefast [--foreground|--daemon] tap_element <index|text>
```

| 参数 | 描述 |
|------|------|
| `index` | UI 元素索引（来自 `ui` 命令） |
| `text` | UI 元素文本或描述（模糊搜索） |

```bash
phonefast tap_element 5              # 点击第 5 个 UI 元素
phonefast tap_element "Settings"    # 点击文本含 "Settings" 的元素
```

#### `swipe` — 滑动手势

```bash
phonefast [--foreground|--daemon] swipe <x1> <y1> <x2> <y2> [duration_ms]
```

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `x1` `y1` | 起始坐标 | — |
| `x2` `y2` | 终点坐标 | — |
| `duration_ms` | 滑动时长（毫秒） | 500 |

```bash
phonefast swipe 540 1600 540 400 500   # 向上滑动
phonefast swipe 200 500 800 500 300    # 向右滑动 300ms
```

---

### 文本输入

#### `type` — 输入文本

```bash
phonefast [--foreground|--daemon] type <text>
```

在当前焦点输入框中输入文本。

```bash
phonefast type "Hello World"
phonefast type "搜索关键词"
```

---

### 按键操作

#### `back` — 返回键

```bash
phonefast [--foreground|--daemon] back
```

#### `home` — Home 键

```bash
phonefast [--foreground|--daemon] home
```

#### `key` — 发送按键

```bash
phonefast [--foreground|--daemon] key <keyname|keycode>
```

**支持的键名：**

| 键名 | 说明 |
|------|------|
| `enter` | 回车 |
| `tab` | Tab 键 |
| `delete` / `backspace` | 删除 |
| `space` | 空格 |
| `escape` / `esc` | Esc 键 |
| `volume_up` / `volume_down` | 音量+/- |
| `volume_mute` | 静音 |
| `power` | 电源键 |
| `menu` | 菜单键 |
| `search` | 搜索键 |
| `camera` | 相机键 |
| `back` | 返回（同 back 命令） |
| `home` | Home（同 home 命令） |
| `dpad_up` / `dpad_down` / `dpad_left` / `dpad_right` / `dpad_center` | 方向键 |
| `page_up` / `page_down` | 翻页 |
| `media_play_pause` / `media_stop` / `media_next` / `media_previous` | 媒体控制 |

```bash
phonefast key enter
phonefast key backspace
phonefast key volume_up
phonefast key power
phonefast key dpad_down

# 也可以直接用键码
phonefast key 4       # BACK
phonefast key 3       # HOME
phonefast key 66      # ENTER
```

---

### 应用操作

#### `launch` — 启动应用

```bash
phonefast [--foreground|--daemon] launch <package>
```

需要通过 Android 包名指定（不支持应用显示名，如 "设置"、"Chrome"）。

```bash
phonefast launch com.android.settings     # 设置
phonefast launch com.android.chrome        # Chrome
phonefast launch com.tencent.mm             # 微信
```

---

### 屏幕捕获与分析

#### `screenshot` — 截图

```bash
phonefast [--foreground|--daemon] screenshot [file]
```

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `file` | 保存路径 | stdout（base64） |

```bash
phonefast screenshot /tmp/screen.png       # 保存为 PNG
phonefast screenshot                        # 输出 base64
```

#### `ui` — UI 元素列表

```bash
phonefast [--foreground|--daemon] ui
```

输出当前屏幕所有可交互 UI 元素（最多显示 50 个），包括索引、文本、资源 ID、类名、可点击状态。

```
[0] id="content" (FrameLayout)
[1] id="webView" (FrameLayout)
[2] text="搜索" (EditText) [clickable]
[3] text="设置" id="settings_btn" (Button) [clickable]
```

#### `observe` — 截图 + UI

```bash
phonefast [--foreground|--daemon] observe
```

并行使截图与 UI 采集，一次调用获取完整屏幕状态。

---

### 工具命令

#### `wait` — 等待

```bash
phonefast [--foreground|--daemon] wait <ms>
```

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `ms` | 等待毫秒数 | 1000 |

#### `status` — Daemon 状态

```bash
phonefast [--foreground|--daemon] status
```

```bash
# 示例输出
daemon running (pid 60977)
  device:    13709314CF044927 (488x1080)
  control:   true
  ui:        true
```

#### `devices` — 设备列表

```bash
phonefast devices
```

```bash
# 示例输出
Connected devices:
  13709314CF044927  device  [TECNO_KL8h]
```

#### `run` — JSON 单次操作

```bash
phonefast [--foreground|--daemon] run '<json>'
```

适用于脚本自动化调用。

```bash
phonefast run '{"action":"tap","args":{"x":540,"y":960}}'
phonefast run '{"action":"screenshot"}'
phonefast run '{"action":"back"}'
phonefast run '{"action":"list_devices"}'
```

支持的 action：`tap`, `tap_element`, `swipe`, `back`, `home`, `type_text`, `press_key`, `launch_app`, `screenshot`, `get_ui_elements`, `observe`, `list_devices`, `wait`。

---

## Daemon 管理

Daemon 是一个后台常驻进程，持有设备的长连接，通过 Unix Socket 接收 JSON-RPC 请求。

```bash
# 启动 daemon（后台运行）
phonefast daemon

# 前台运行（查看实时日志）
phonefast daemon --foreground

# 指定设备序列号
phonefast daemon --serial 13709314CF044927

# 自定义 socket/PID 文件路径
phonefast daemon --socket /tmp/my-phone.sock

# 查看 daemon 状态
phonefast daemon --status

# 停止 daemon
phonefast daemon --stop
```

**自动管理：**
- 使用 `--daemon` 标志执行命令时，如果 daemon 未运行，会自动在后台启动
- 如果 daemon 进程存在但无响应，会自动杀死并重启
- 多次调用 `phonefast daemon` 不会重复启动（已运行则退出）

---

## MCP 服务器

phonefast 可作为 MCP (Model Context Protocol) 服务器，供 Claude Desktop 等 AI 助手直接控制手机。

```bash
# SSE 模式（默认端口 8019）
phonefast serve

# 自定义端口
phonefast serve --port 8080

# 自定义路径
phonefast serve --path /mcp

# STDIO 模式（Claude Desktop 集成）
phonefast serve --transport stdio
```

### 客户端配置

**SSE 模式：**
```json
{
  "mcpServers": {
    "phonefast": {
      "url": "http://localhost:8019/Phone/sse"
    }
  }
}
```

**STDIO 模式：**
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

### MCP 工具列表

| 工具 | 参数 | 说明 |
|------|------|------|
| `list_devices` | — | 列出已连接的 Android 设备 |
| `screenshot` | — | 截取当前屏幕（base64 PNG） |
| `get_ui_elements` | — | 获取可交互 UI 元素 |
| `observe` | — | 截图 + UI 元素（一次调用） |
| `tap` | `x`, `y` | 点击坐标 |
| `tap_element` | `index` 或 `text` | 点击 UI 元素 |
| `swipe` | `start_x`, `start_y`, `end_x`, `end_y`, `duration_ms` | 滑动手势 |
| `type_text` | `text` | 输入文本 |
| `back` | — | 返回键 |
| `home` | — | Home 键 |
| `press_key` | `keycode` 或 `key` | 发送按键 |
| `launch_app` | `package` | 启动应用（仅包名，如 `com.android.settings`） |
| `wait` | `duration_ms` | 等待 N 毫秒 |

---

## 架构

```
phonefast CLI
    │
    ├── --daemon 模式 ──→ Unix Socket ──→ daemon 进程 ──→ TCP ──→ scrcpy server（设备）
    │                    JSON-RPC          持有长连接        控制+视频+UI
    │
    └── 直接模式 ──→ 每次新建 session ──→ TCP ──→ scrcpy server（设备）
                     部署+启动+连接+关闭

内部结构:
  internal/
  ├── adb/       ADB 设备发现、scrcpy 部署与生命周期
  ├── daemon/    守护进程、JSON-RPC 分发、健康检查
  ├── log/       异步文件日志
  ├── mcp/       MCP 服务器（基于 mcp-go）、工具注册
  ├── session/   设备会话：视频流、控制、UI 采集、截图
  pkg/
  ├── h264/      H.264 AnnexB 解析、关键帧提取
  └── protocol/  scrcpy 协议编码与控制消息
```

---

## 日志

异步写入 `/tmp/phonefast-{uid}.log`，记录所有关键操作与调用上下文。

**日志格式：**
```
2026-06-16 09:13:56.879 [session.go:139 Connect()] connected: 488x1080  control=true
2026-06-16 09:13:59.602 [rpc.go:115 Dispatch()] rpc back
2026-06-16 09:13:59.603 [control.go:138 Back()] back
2026-06-16 09:13:59.624 [control.go:38 Tap()] tap (244,540)
2026-06-16 09:13:59.952 [control.go:93 Swipe()] swipe (200,900)→(200,300) 300ms
2026-06-16 09:14:26.000 [daemon.go:328 healthLoop()] health: connection dead, reconnecting...
2026-06-16 09:14:29.000 [daemon.go:298 reconnect()] reconnected: 13709314CF044927 (488x1080)
```

**覆盖范围：** daemon 生命周期、设备连接、RPC 分发、控制操作、心跳检测、断线重连。

---

## 断线恢复

三级保活机制：

| 层级 | 机制 | 间隔 | 说明 |
|------|------|------|------|
| OS 级 | TCP Keepalive | 视频 30s / 控制 15s | 操作系统检测死连接 |
| 应用级 | `healthLoop` 协程 | 10s | 检测视频+控制连接是否存活，自动重连 |
| 写触发 | `markControlBroken` | 即时 | 写入失败立刻标记，下次请求重连并重试 |

当设备 USB 断开或 scrcpy 被 kill 时，daemon 在 10 秒内自动检测并恢复。

---

## 两种模式对比

| | Daemon 模式 | 直接模式 |
|------|-------------|----------|
| 命令格式 | `phonefast <cmd>`（默认） | `phonefast --foreground <cmd>` |
| 响应速度 | <10ms | ~2.5s |
| 资源占用 | 后台常驻一个 daemon 进程 | 每次新建/销毁连接 |
| 适用场景 | 批量操作、脚本自动化 | 临时单次操作 |
| 自动管理 | 自动启动/重启/恢复 | 无状态 |

---

## License

MIT
