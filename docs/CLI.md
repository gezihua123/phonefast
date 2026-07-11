# phonefast CLI 使用手册

> 版本: 1.0.8 | 协议: MIT | 平台: macOS / Linux / Windows

phonefast 是一个高性能 Android 设备控制命令行工具，基于 Go 语言和 scrcpy 协议构建。专为 AI Agent 高频交互场景设计，单次操作延迟 <10ms，支持 Daemon 后台常驻模式和 MCP 协议集成。

---

## 目录

1. [安装与构建](#1-安装与构建)
2. [快速开始](#2-快速开始)
3. [模式标志](#3-模式标志)
4. [命令参考](#4-命令参考)
   - [4.1 触摸操作](#41-触摸操作)
   - [4.2 文本输入](#42-文本输入)
   - [4.3 按键操作](#43-按键操作)
   - [4.4 应用操作](#44-应用操作)
   - [4.5 屏幕捕获与分析](#45-屏幕捕获与分析)
   - [4.6 工具命令](#46-工具命令)
   - [4.7 JSON 批处理](#47-json-批处理)
   - [4.8 版本信息](#48-版本信息)
5. [Daemon 管理](#5-daemon-管理)
6. [MCP 服务器](#6-mcp-服务器)
7. [使用场景与最佳实践](#7-使用场景与最佳实践)
8. [架构概览](#8-架构概览)
9. [日志与故障恢复](#9-日志与故障恢复)
10. [附录：常见问题](#10-附录常见问题)

---

## 1. 安装与构建

### 前置依赖

| 依赖 | 版本要求 | 用途 |
|------|---------|------|
| Go | 1.21+ | 编译工具链 |
| `adb` | — | Android 设备通信 |
| `git` | — | 版本信息自动注入 |
| FFmpeg 静态库 | 7.1（默认） | 视频解码（CGO 模式必需） |
| `nasm` | 可选 | x86 FFmpeg asm 优化 |
| `zig` | 可选 | 非本机交叉编译 |
| `upx` | 可选 | 压缩二进制体积 |

### 构建

```bash
# 克隆仓库
git clone https://github.com/gezihua123/phonefast.git
cd phonefast

# CGO 构建（推荐，视频解码用硬件加速，需 FFmpeg 静态库）
bash scripts/build.sh                            # 自动下载 FFmpeg + 构建

# 无 CGO 构建（无需 FFmpeg，视频解码用子进程方案）
CGO_ENABLED=0 go build -o phonefast ./cmd/phonefast/

# 全平台
bash scripts/build.sh --all                      # 全平台交叉编译
bash scripts/build.sh --macos                    # macOS amd64 + arm64
bash scripts/build.sh --linux                    # Linux amd64 + arm64
bash scripts/build.sh --windows                  # Windows amd64
bash scripts/build.sh --all --version 1.0.0      # 指定版本号
```

### FFmpeg 静态库准备

CGO 构建需要 FFmpeg 静态库（链接进二进制，无需系统安装 `ffmpeg` 命令）。

```bash
# 一键准备（下载预编译库，失败自动回退源码编译）
bash scripts/download-ffmpeg.sh                    # 当前平台
bash scripts/download-ffmpeg.sh x86_64-linux-gnu   # 指定目标
bash scripts/download-ffmpeg.sh --all               # 所有平台

# 从源码编译（备选，约 5-15 分钟/目标）
bash scripts/cross-build-ffmpeg.sh aarch64-darwin

# 手动指定 FFmpeg 路径（跳过脚本）
export PKG_CONFIG_PATH="$(pwd)/build/cross-ffmpeg/aarch64-darwin/lib/pkgconfig"
CGO_ENABLED=1 go build -o phonefast ./cmd/phonefast/
```

### 产物结构

```
dist/<version>/
├── <platform>/
│   ├── phonefast                  # CLI 二进制
│   ├── phonefast.exe              # (Windows)
│   ├── scrcpy-server.jar          # scrcpy 服务器 (Android 端)
│   ├── scrcpy-server.version      # 版本标记文件
│   ├── README.md                  # 操作文档
│   └── docs/                      # 详细文档
└── <platform>/
    └── phonefast-<version>-<os>-<arch>.tar.gz     # 发布包
```

### 手动安装

```bash
# 将二进制放入 PATH
cp dist/<version>/darwin_arm64/phonefast /usr/local/bin/

# 复制依赖文件
mkdir -p /usr/local/share/phonefast
cp dist/<version>/darwin_arm64/scrcpy-server.jar /usr/local/share/phonefast/
cp dist/<version>/darwin_arm64/scrcpy-server.version /usr/local/share/phonefast/
```

---

## 2. 快速开始

```bash
# 1. 连接你的 Android 设备（USB 调试开启）
# 2. 验证设备已连接
phonefast devices

# 3. 执行操作（Daemon 模式，<10ms 延迟）
phonefast tap 540 960                # 点击屏幕中心
phonefast back                        # 返回
phonefast screenshot /tmp/screen.png  # 截图
phonefast observe                     # 截图 + UI 元素

# 4. 或使用直接模式（每次新建连接，~2.5s）
phonefast --foreground tap 540 960

# 5. 启动 MCP 服务器（供 AI 助手使用）
phonefast serve
```

---

## 3. 模式标志

### 格式

```bash
phonefast [--foreground|--daemon] [--serial <SERIAL>] <command> [args...]
```

### 标志说明

| 标志 | 别名 | 说明 | 默认 |
|------|------|------|------|
| `--daemon` | — | Daemon 模式，后台常驻进程（默认行为） | ✓ |
| `--foreground` | `--direct` | 直接模式，每次新建 scrcpy 连接 | — |
| `--serial` | — | 指定设备序列号（多设备时必用） | 自动检测 |

### 两种模式对比

| 维度 | Daemon 模式 | 直接模式 |
|------|-------------|----------|
| 命令格式 | `phonefast <cmd>`（默认） | `phonefast --foreground <cmd>` |
| 响应速度 | <10ms | ~2.5s |
| 资源占用 | 后台常驻一个 daemon 进程 | 每次新建/销毁连接 |
| 适用场景 | 批量操作、脚本自动化、AI Agent 高频循环 | 临时单次操作 |
| 自动管理 | 自动启动、自动重启、断线自动恢复 | 无状态 |

### 多设备管理

多台 Android 设备连接时，使用 `--serial` 指定目标设备：

```bash
phonefast --serial 13709314CF044927 tap 540 960
phonefast --serial R3CNB0000000XYZ screenshot /tmp/s.png
```

---

## 4. 命令参考

### 4.1 触摸操作

#### `tap` — 点击坐标

```bash
phonefast [--foreground|--daemon] tap <x> <y>
```

| 参数 | 描述 | 是否必需 |
|------|------|---------|
| `x` | X 坐标（像素） | ✓ |
| `y` | Y 坐标（像素） | ✓ |

**示例：**
```bash
phonefast tap 540 960                  # 点击屏幕中心
phonefast tap 100 200                  # 点击左上角区域
phonefast --foreground tap 244 540     # 直接模式点击
```

**输出：**
```
Tapped at (540, 960)
```

---

#### `tap_element` — 点击 UI 元素

```bash
phonefast [--foreground|--daemon] tap_element <index|text>
```

| 参数 | 类型 | 描述 | 是否必需 |
|------|------|------|---------|
| `index` | 数字 | UI 元素索引（来自 `ui` 命令） | 二选一 |
| `text` | 字符串 | UI 元素文本或描述（模糊搜索） | 二选一 |

**示例：**
```bash
# 按索引点击（索引来自 `ui` 命令输出中的 [N]）
phonefast tap_element 5

# 按文本搜索（模糊匹配，不区分大小写）
phonefast tap_element "Settings"
phonefast tap_element "发送"
phonefast tap_element "compose"
```

**说明：**
- 索引模式：必须先用 `ui` 或 `observe` 获取当前 UI 元素列表，查看元素对应的索引号
- 文本模式：模糊匹配元素的 `text` 属性和 `content-desc` 属性，匹配第一个元素
- 文本匹配不区分大小写

---

#### `swipe` — 滑动手势

```bash
phonefast [--foreground|--daemon] swipe <x1> <y1> <x2> <y2> [duration_ms]
```

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `x1` `y1` | 起始坐标（像素） | — |
| `x2` `y2` | 终点坐标（像素） | — |
| `duration_ms` | 滑动时长（毫秒） | `500` |

**示例：**
```bash
# 向上滑动（从底部到顶部）
phonefast swipe 540 1600 540 400

# 向下滑动
phonefast swipe 540 400 540 1600

# 向右滑动（300ms 快速滑动）
phonefast swipe 200 500 800 500 300

# 向左滑动（800ms 慢速滑动）
phonefast swipe 800 500 200 500 800
```

**输出：**
```
Swiped from (540, 1600) to (540, 400)
```

---

### 4.2 文本输入

#### `type` / `text` — 输入文本

```bash
phonefast [--foreground|--daemon] type <text>
```

向当前焦点所在的输入框输入文本。

| 参数 | 描述 | 是否必需 |
|------|------|---------|
| `text` | 要输入的文本内容 | ✓ |

**示例：**
```bash
phonefast type "Hello World"
phonefast type "搜索关键词"
phonefast type "user@example.com"
```

**输出：**
```
Typed: Hello World
```

**注意：**
- 输入前确保目标输入框已获得焦点（可以先 `tap` 点击输入框）
- 输入的内容会逐个字符模拟按键，支持字母、数字、中文等

---

### 4.3 按键操作

#### `back` — 返回键

```bash
phonefast [--foreground|--daemon] back
```

模拟 Android 系统返回键（KeyEvent.KEYCODE_BACK）。

**示例：**
```bash
phonefast back
```

**输出：**
```
Back pressed
```

---

#### `home` — Home 键

```bash
phonefast [--foreground|--daemon] home
```

模拟 Android 系统 Home 键（KeyEvent.KEYCODE_HOME），回到桌面。

**示例：**
```bash
phonefast home
```

**输出：**
```
Home pressed
```

---

#### `key` / `press_key` — 发送按键

```bash
phonefast [--foreground|--daemon] key <keyname|keycode>
```

支持按键名称和数字键码两种方式。

| 参数 | 描述 | 是否必需 |
|------|------|---------|
| `keyname` | 按键名称（见下表） | 二选一 |
| `keycode` | Android KeyEvent 数字键码 | 二选一 |

**支持的键名：**

| 分类 | 键名 | 说明 | 对应键码 |
|------|------|------|---------|
| 导航 | `back` | 返回 | 4 |
| | `home` | 桌面 | 3 |
| | `menu` | 菜单 | 82 |
| | `search` | 搜索 | 84 |
| 输入 | `enter` | 回车 | 66 |
| | `tab` | Tab | 61 |
| | `delete` / `backspace` | 删除 | 67 |
| | `space` | 空格 | 62 |
| | `escape` / `esc` | 退出 | 111 |
| 音量 | `volume_up` | 音量加 | 24 |
| | `volume_down` | 音量减 | 25 |
| | `volume_mute` | 静音 | 164 |
| 系统 | `power` | 电源 | 26 |
| | `camera` | 相机 | 27 |
| 方向 | `dpad_up` | 上 | 19 |
| | `dpad_down` | 下 | 20 |
| | `dpad_left` | 左 | 21 |
| | `dpad_right` | 右 | 22 |
| | `dpad_center` | 确定 | 23 |
| 翻页 | `page_up` | 上一页 | 92 |
| | `page_down` | 下一页 | 93 |
| 媒体 | `media_play_pause` | 播放/暂停 | 85 |
| | `media_stop` | 停止 | 86 |
| | `media_next` | 下一曲 | 87 |
| | `media_previous` | 上一曲 | 88 |

**示例：**
```bash
# 按名称
phonefast key enter
phonefast key backspace
phonefast key volume_up
phonefast key power
phonefast key dpad_down
phonefast key media_play_pause

# 按键码
phonefast key 4       # BACK
phonefast key 3       # HOME
phonefast key 66      # ENTER
phonefast key 24      # VOLUME_UP
```

**输出：**
```
Key 'enter' pressed
```

---

### 4.4 应用操作

#### `launch` — 启动应用

```bash
phonefast [--foreground|--daemon] launch <package>
```

| 参数 | 描述 | 是否必需 |
|------|------|---------|
| `package` | Android 应用包名 | ✓ |

**常用应用包名：**

| 应用 | 包名 |
|------|------|
| 系统设置 | `com.android.settings` |
| Chrome | `com.android.chrome` |
| 微信 | `com.tencent.mm` |
| 支付宝 | `com.eg.android.AlipayGphone` |
| 淘宝 | `com.taobao.taobao` |
| 抖音 | `com.ss.android.ugc.aweme` |
| 小红书 | `com.xingin.xhs` |

**示例：**
```bash
phonefast launch com.android.settings
phonefast launch com.tencent.mm
phonefast launch com.android.chrome
```

**输出：**
```
Launched: com.android.settings
```

**注意：**
- 不支持应用显示名（如"设置"、"Chrome"），必须使用 Android 包名
- 可通过 `adb shell pm list packages | grep <关键词>` 查找包名

---

### 4.5 屏幕捕获与分析

#### `screenshot` — 截图

```bash
phonefast [--foreground|--daemon] screenshot [file]
```

| 参数 | 描述 | 默认值 | 是否必需 |
|------|------|--------|---------|
| `file` | 保存路径 | 不指定则输出 base64 到 stdout | 可选 |

**截图原理：** 从 H.264 视频流中提取关键帧（I-frame），通过 `ffmpeg` 转码为 PNG 输出。

**示例：**
```bash
# 保存为文件
phonefast screenshot /tmp/screen.png

# 输出 base64 到 stdout（可用于管道或脚本）
phonefast screenshot

# 配合工具使用
phonefast screenshot | base64 -d > screen.png
```

**输出（文件模式）：**
```
Screenshot saved to /tmp/screen.png
```

**输出（base64 模式）：**
```
data:image/png;base64,iVBORw0KGgoAAAANS...
```

---

#### `ui` — UI 元素列表

```bash
phonefast [--foreground|--daemon] ui [max_elements] [--summary] [--format <fmt>]
```

获取当前屏幕 UI 元素的层级信息。

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `max_elements` | 最大显示元素数量 | `100` |
| `--summary` | 概要模式，过滤纯布局类元素 | — |
| `--format` | 输出格式：`flat`（默认）、`flatref`、`jsonl`、`simplexml`、`yml` | `flat` |

**示例：**
```bash
# 平铺格式（默认）
phonefast ui

# 层级引用格式（flatref，每行自包含 parent 引用）
phonefast ui --format flatref

# JSON Lines 格式（LLM 精确解析）
phonefast ui --format jsonl

# 概要模式
phonefast ui --summary
```

**flatref 格式（推荐用于 AI Agent）：**

flatref 是专为 LLM 设计的层级格式，每行一个元素，用 `|` 分隔四个语义列：

```
#N <身份> | bounds=[l,t][r,b] | [状态] | depth=N parent=#M
```

```
#0 (FrameLayout) | bounds=[0,0][1080,2400] | | depth=0 parent=#-1
#19 id="back_btn" (ImageButton) | bounds=[0,0][96,96] | [clickable] | depth=3 parent=#18
#21 text="安装" (TextView) | bounds=[899,432][975,491] | | depth=4 parent=#20
```

| 列 | 内容 | 说明 |
|----|------|------|
| 身份 | `#N text="..." desc="..." id="..." (Class)` | 元素是什么 |
| 位置 | `bounds=[l,t][r,b]` | 元素在哪 |
| 状态 | `[clickable] [focused] [selected] [disabled]` | 可否交互 |
| 层级 | `depth=N parent=#M` | 在树中的位置 |

**其他层级格式：**

| 格式 | 特点 | 适合场景 |
|------|------|---------|
| `flatref` | `\|` 分隔四列，token 最省 | AI Agent 日常使用 |
| `jsonl` | 每行独立 JSON，基准准确率最高 | 精确结构化解析 |
| `simplexml` | 嵌套 XML，可读性好 | 人工阅读 |
| `yml` | YAML 缩进层级 | 配置文件风格 |
| `flat` | 传统平铺格式（默认） | 向后兼容 |

**字段说明：**

| 字段 | 说明 |
|------|------|
| `#N` | 元素 ID（用于 `parent=#N` 引用） |
| `text="..."` | 元素文本 |
| `desc="..."` | 无障碍描述（content-desc） |
| `id="..."` | 资源 ID（仅显示最后一段） |
| `(ClassName)` | 元素类名（简化） |
| `[clickable]` | 可点击标记 |
| `[focused]` | 已聚焦标记 |
| `[selected]` | 已选中标记 |
| `[disabled]` | 已禁用标记 |
| `bounds=[l,t][r,b]` | 边界坐标（左上、右下） |
| `depth=N` | 层级深度（0=根） |
| `parent=#M` | 父节点 ID 引用 |

---

#### `observe` — 截图 + UI（原子操作）

```bash
phonefast [--foreground|--daemon] observe [max_elements] [--summary]
```

并行使截图与 UI 元素采集，一次调用获取完整的屏幕状态快照。

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `max_elements` | 最大显示元素数量 | `100` |
| `--summary` | 概要模式 | — |

**与分别调用 `screenshot` + `ui` 的区别：**

| 对比项 | `observe` | `screenshot` + `ui` |
|--------|-----------|-------------------|
| 原子性 | ✓ 截图和 UI 同时采集 | 有竞态时间窗口 |
| 调用次数 | 1 次 | 2 次 |
| 延迟 | ~148ms | ~167ms + ~191ms |

**示例：**
```bash
# 完整观察（截图 + UI 一起返回）
phonefast observe

# 概要模式
phonefast observe --summary

# 只显示 20 个元素
phonefast observe 20
```

**输出：** 包含截图（通常输出为 base64 data URI）和 UI 元素列表。

---

### 4.6 工具命令

#### `wait` — 等待

```bash
phonefast [--foreground|--daemon] wait <ms>
```

在操作序列中插入等待时间，常用于等待页面加载或动画完成。

| 参数 | 描述 | 默认值 |
|------|------|--------|
| `ms` | 等待毫秒数 | `1000` |

**示例：**
```bash
# 等待 1 秒（默认）
phonefast wait

# 等待 3 秒
phonefast wait 3000

# 在操作之间使用
phonefast tap 540 960 && phonefast wait 2000 && phonefast tap 540 960
```

**输出：**
```
Waited 2000ms
```

---

#### `help` — 显示帮助信息

```bash
phonefast help
phonefast --help
phonefast -h
```

显示命令列表和用法说明。

**示例：**
```bash
phonefast help
phonefast --help
```

**输出：**
```
phonefast — Fast Android device control

Commands (default: daemon mode, auto-starts daemon, <10ms):
  phonefast tap <x> <y>                     Tap at coordinates
  ...
```

---

#### `status` — 显示 Daemon 状态

```bash
phonefast [--foreground|--daemon] status
```

**示例：**
```bash
phonefast status
```

**输出（Daemon 模式）：**
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

**输出（直接模式）：**
```
daemon running (pid 60977)
  device:    13709314CF044927 (488x1080)
  control:   true
  ui:        true
```

---

#### `devices` — 列出设备

```bash
phonefast devices
```

**示例：**
```bash
phonefast devices
```

**输出：**
```
Connected devices:
  13709314CF044927  device  [TECNO_KL8h]
  R3CNB0000000XYZ   device  [Pixel_6]
```

**字段说明：**

| 字段 | 说明 |
|------|------|
| `Serial` | 设备序列号（用于 `--serial` 标志） |
| `Status` | 连接状态：`device`（正常）、`offline`、`unauthorized` |
| `Model` | 设备型号 |

---

#### `connect` / `disconnect` — 设备连接管理

> **注意：** `connect` 和 `disconnect` 命令已废弃。使用 `daemon --stop` 替代。

```bash
# 停止当前 daemon 连接
phonefast daemon --stop

# 重新连接（启动 daemon）
phonefast daemon --serial <SERIAL>
```

---

### 4.7 JSON 批处理

#### `run` — JSON 单次操作

```bash
phonefast [--foreground|--daemon] run '<json>'
```

以 JSON 格式指定操作，适合脚本自动化调用。

| 参数 | 描述 | 是否必需 |
|------|------|---------|
| `json` | JSON 对象或数组 | ✓ |

**单个操作：**
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

**批处理（JSON 数组）：**

按顺序依次执行多个操作：

```bash
phonefast run '[
  {"action":"launch_app","args":{"package":"com.android.settings"}},
  {"action":"wait","args":{"duration_ms":2000}},
  {"action":"screenshot","args":{}},
  {"action":"back"}
]'
```

**支持的 action 列表：**

| Action | 参数 | 说明 |
|--------|------|------|
| `tap` | `x`, `y` | 点击坐标 |
| `tap_element` | `index` 或 `text` | 点击 UI 元素 |
| `swipe` | `start_x`, `start_y`, `end_x`, `end_y`, `duration_ms` | 滑动 |
| `back` | — | 返回 |
| `home` | — | 桌面 |
| `type_text` | `text` | 输入文本 |
| `press_key` | `keycode` 或 `key` | 按键 |
| `launch_app` | `package`（或 `app`） | 启动应用 |
| `screenshot` | — | 截图 |
| `get_ui_elements` | — | UI 元素 |
| `observe` | — | 截图 + UI |
| `list_devices` | — | 设备列表 |
| `wait` | `duration_ms` | 等待 |

**扁平参数简写：**

如果 `args` 不存在，工具会自动从 JSON 顶层读取参数：

```bash
# 完整写法
phonefast run '{"action":"tap","args":{"x":540,"y":960}}'

# 等效简写
phonefast run '{"action":"tap","x":540,"y":960}'
```

---

### 4.8 版本信息

```bash
phonefast --version
phonefast -v
```

**输出：**
```
phonefast 1.0.1 (commit a1b2c3d4, built 2026-07-01T10:00:00Z)
```

---

## 5. Daemon 管理

Daemon 是 phonefast 的核心机制。它是一个后台常驻进程，持有设备的长连接，通过 Unix Socket 接收 JSON-RPC 请求，实现 <10ms 的命令延迟。

### 启动与停止

```bash
# 启动 daemon（后台运行）
phonefast daemon

# 前台运行（查看实时日志到 stdout）
phonefast daemon --foreground

# 查看 daemon 状态
phonefast daemon --status

# 停止 daemon
phonefast daemon --stop
```

### 高级选项

```bash
# 指定设备序列号（多设备时）
phonefast daemon --serial 13709314CF044927

# 自定义 socket 路径
phonefast daemon --socket /tmp/my-phone.sock

# 前台运行 + 指定设备和 socket
phonefast daemon --foreground --serial R3CNB0000000XYZ --socket /tmp/phone2.sock
```

| 标志 | 描述 | 默认值 |
|------|------|--------|
| `--foreground` / `-f` | 前台运行，日志输出到 stdout | 后台运行 |
| `--stop` | 停止正在运行的 daemon | — |
| `--status` | 查看 daemon 状态 | — |
| `--serial` | 指定设备序列号 | 自动检测 |
| `--socket` / `-s` | 自定义 Unix Socket 路径 | 自动生成 |

### 自动管理

Daemon 具有完善的自动管理机制：

1. **自动启动** — 使用任何命令时，如果 daemon 未运行，自动在后台启动
2. **自动恢复** — 如果 daemon 进程存在但无响应，自动杀死并重启
3. **防重复** — 多次调用 `phonefast daemon` 不会重复启动（已运行则退出并提示）

### 启动流程

当 CLI 检测到 daemon 未运行时，自动执行以下步骤：

```
① 检查 PID 文件 → ② 清理残留文件 → ③ fork 子进程
④ 等待 Unix Socket 就绪 → ⑤ 轮询 daemon 健康状态
⑥ 确认设备已连接 → ⑦ 返回命令执行
```

启动超时约 8 秒，超时后输出错误信息。

### 设备绑定

Daemon 与设备序列号绑定，每个设备一个 daemon 进程。文件路径规则：

| 文件 | 路径模式 |
|------|---------|
| PID 文件 | `/tmp/phonefast-{uid}-{serial}.pid` |
| Socket | `/tmp/phonefast-{uid}-{serial}.sock` |

> **注意：** `{uid}` 为当前系统用户 ID（`os.Getuid()`），用于隔离不同用户的 daemon 实例。旧版无 uid 格式的文件（如 `/tmp/phonefast-{serial}.sock`）在启动时自动清理。

---

## 6. MCP 服务器

phonefast 可以作为 MCP（Model Context Protocol）服务器，供 Claude Desktop 等 AI 助手直接控制手机。

### 启动服务器

```bash
# SSE 模式（默认）
phonefast serve

# 自定义端口
phonefast serve --port 8080

# 自定义路径
phonefast serve --path /mcp

# STDIO 模式（Claude Desktop 集成用）
phonefast serve --transport stdio

# 自定义监听地址
phonefast serve --host 127.0.0.1 --port 8019
```

| 标志 | 描述 | 默认值 |
|------|------|--------|
| `--transport` / `-t` | 传输模式：`sse` 或 `stdio` | `sse` |
| `--port` / `-p` | 端口号 | `8019` |
| `--host` / `-H` | 监听地址 | `0.0.0.0` |
| `--path` | URL 路径前缀 | `/Phone` |

### 模式说明

| 模式 | 启动方式 | 适用场景 |
|------|---------|---------|
| SSE | `phonefast serve` | 远程连接、自定义客户端 |
| STDIO | `phonefast serve --transport stdio` | Claude Desktop 集成 |

### 客户端配置

**SSE 模式（MCP 配置）：**
```json
{
  "mcpServers": {
    "phonefast": {
      "url": "http://localhost:8019/Phone/sse"
    }
  }
}
```

**STDIO 模式（Claude Desktop 配置）：**
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
| `list_devices` | — | 列出已连接设备 |
| `screenshot` | — | 截图（返回 native ImageContent） |
| `get_ui_elements` | `format`（flat/flatref/jsonl/simplexml/yml）、`max_elements` | 获取 UI 层级（支持多格式） |
| `observe` | — | 截图 + UI 原子操作 |
| `tap` | `x`, `y` | 点击坐标 |
| `tap_element` | `index` 或 `text` | 点击 UI 元素 |
| `swipe` | `start_x`, `start_y`, `end_x`, `end_y`, `duration_ms` | 滑动手势 |
| `type_text` | `text` | 输入文本 |
| `back` | — | 返回键 |
| `home` | — | Home 键 |
| `press_key` | `keycode` 或 `key` | 按键事件 |
| `launch_app` | `package` | 启动应用（包名） |
| `wait` | `duration_ms` | 等待 |

---

## 7. 使用场景与最佳实践

### 场景一：AI Agent 交互循环

AI Agent 与手机交互的典型循环：观察（截图+UI）→ 分析 → 操作 → 再观察。

```bash
phonefast observe                       # 步骤 1: 观察
phonefast tap 540 960                   # 步骤 2: 操作
phonefast wait 1500                     # 等待动画
phonefast observe                       # 步骤 3: 确认结果
```

### 场景二：自动化测试脚本

```bash
#!/bin/bash
# app_test.sh — 自动测试脚本

# 打开设置
phonefast launch com.android.settings
phonefast wait 2000

# 截图记录
phonefast screenshot /tmp/step1_settings.png

# 点击搜索
phonefast tap_element "搜索"
phonefast wait 1000
phonefast type "WiFi"
phonefast wait 1000

# 返回桌面
phonefast home
```

### 场景三：JSON 批处理工作流

```bash
phonefast run '[
  {"action":"launch_app","args":{"package":"com.android.chrome"}},
  {"action":"wait","args":{"duration_ms":3000}},
  {"action":"type_text","text":"hello world"},
  {"action":"wait","duration_ms":2000},
  {"action":"screenshot"},
  {"action":"back"},
  {"action":"home"}
]'
```

### 场景四：多设备操作

```bash
# 终端 1: 控制设备 A
phonefast --serial DEVICE_A tap 540 960

# 终端 2: 控制设备 B
phonefast --serial DEVICE_B --foreground tap 100 200
```

### 最佳实践

1. **默认使用 Daemon 模式** — 自动启动、低延迟、自动恢复
2. **操作间加 `wait`** — 等待页面加载/动画完成（一般 1-3 秒）
3. **使用 `tap_element` 代替坐标** — 文本搜索比坐标点击更鲁棒
4. **批量操作用 JSON 批处理** — `run` 命令支持 JSON 数组
5. **多设备指定 `--serial`** — 多设备连接时务必指定序列号
6. **`observe` 优于 `screenshot` + `ui`** — 原子操作，消除竞态

---

## 8. 架构概览

```
phonefast CLI
    │
    ├── Daemon 模式 ──→ Unix Socket ──→ Daemon 进程 ──→ TCP ──→ scrcpy-server（设备端）
    │                   JSON-RPC          持有长连接         控制+视频+UI
    │
    └── 直接模式 ──→ 每次新建 session ──→ TCP ──→ scrcpy-server（设备端）
                      部署+启动+连接+关闭
```

### 内部模块

| 模块 | 路径 | 功能 |
|------|------|------|
| **CLI** | `cmd/phonefast/main.go` | 命令行解析、命令分发、模式选择 |
| **ADB** | `internal/adb/` | 设备发现、scrcpy 部署与生命周期 |
| **Daemon** | `internal/daemon/` | 守护进程、JSON-RPC 分发、健康检查 |
| **MCP** | `internal/mcp/` | MCP 服务器（SSE/STDIO）、工具注册 |
| **Session** | `internal/session/` | 设备会话：视频流、控制、UI、截图 |
| **H.264** | `pkg/h264/` | 视频流解析、关键帧提取 |
| **Protocol** | `pkg/protocol/` | scrcpy 协议编码与控制消息 |

### 技术栈

| 组件 | 技术 |
|------|------|
| 语言 | Go（原生二进制，无运行时依赖） |
| 设备通信 | scrcpy 协议（TCP 隧道） |
| 进程通信 | Unix Socket JSON-RPC |
| 视频流 | H.264 → ffmpeg 转 PNG |
| UI 采集 | UISocketHandler（自定义 scrcpy-server 扩展） |
| AI 集成 | MCP（Model Context Protocol）SSE / STDIO |

---

## 9. 日志与故障恢复

### 异步日志

日志异步写入 `/tmp/phonefast-{uid}.log`，记录所有关键操作。

**日志格式：**
```
09:13:56.879 [session.go:139 Connect()] connected: 488x1080  control=true
09:13:59.602 [rpc.go:115 Dispatch()] rpc back
09:13:59.603 [control.go:138 Back()] back
09:13:59.624 [control.go:38 Tap()] tap (244,540)
09:13:59.952 [control.go:93 Swipe()] swipe (200,900)→(200,300) 300ms
09:14:26.000 [daemon.go:328 healthLoop()] health: connection dead, reconnecting...
09:14:29.000 [daemon.go:298 reconnect()] reconnected: 13709314CF044927 (488x1080)
```

### 三级保活机制

| 层级 | 机制 | 间隔 | 说明 |
|------|------|------|------|
| 1. OS 级 | TCP Keepalive | 视频 30s / 控制 15s | 操作系统自动检测死连接 |
| 2. 应用级 | `healthLoop` 协程 | 10s 轮询 | 检测视频+控制连接，自动重连 |
| 3. 写触发 | `markControlBroken` | 即时 | 写入失败立即标记，下次请求自动重连并重试 |

当设备 USB 断开或 scrcpy-server 被 kill 时，Daemon 在 **10 秒内**自动检测并恢复连接。

### 故障恢复场景

| 故障 | 恢复行为 | 恢复时间 |
|------|---------|---------|
| USB 断开后重连 | 自动重连 scrcpy | <10s |
| scrcpy-server 崩溃 | daemon 自动重启 scrcpy | <10s |
| daemon 进程崩溃 | CLI 自动重启 daemon | <8s |
| UI socket 超时 | 下次调用自动恢复 | 即时 |
| TCP broken pipe | daemon 自动重连 | <10s |

---

## 10. 附录：常见问题

### 1. 如何查看设备是否连接？

```bash
phonefast devices
```

输出 `device` 表示已授权连接，`unauthorized` 表示未授权（需要在手机上确认 USB 调试授权）。

### 2. 多台设备同时连接怎么选？

使用 `--serial` 标志指定目标设备：

```bash
phonefast --serial 13709314CF044927 tap 540 960
```

### 3. Daemon 启动失败怎么办？

常见原因：

- **设备未连接** — 运行 `phonefast devices` 检查
- **ADB 未授权** — 在手机上确认 USB 调试授权
- **端口冲突** — 如有其他 scrcpy 实例运行，先关闭
- **scrcpy-server.jar 缺失** — 确保依赖文件在正确位置

解决方案：

```bash
# 重启 ADB 服务
adb kill-server
adb start-server

# 重新连接设备
adb devices

# 重试
phonefast daemon
```

### 4. `tap_element` 无法找到元素？

- 先用 `phonefast ui` 查看当前屏幕有哪些元素
- 确认元素确实在屏幕上可见
- 文本搜索是模糊匹配，检查文本拼写
- 某些非标准视图可能不会被采集到

### 5. 如何获取应用包名？

```bash
# 列出所有安装的应用
adb shell pm list packages

# 按关键词搜索
adb shell pm list packages | grep -i wechat
adb shell pm list packages | grep -i chrome
```

### 6. Daemon 模式和直接模式如何选？

| 你的需求 | 推荐模式 |
|---------|---------|
| 频繁操作、批量脚本 | Daemon 模式（默认） |
| 偶尔单次操作 | 直接模式（`--foreground`） |
| AI Agent 高频交互 | Daemon 模式 |
| 临时使用别人的电脑 | 直接模式 |
| 自动化 CI 流程 | Daemon 模式 |

### 7. `screenshot` 与 `observe` 有何区别？

`screenshot` 仅截图，`observe` 同时在一次调用中完成截图 + UI 采集。`observe` 是原子操作，不会出现"截图时是一个页面，UI 采集时页面已变化"的竞态问题。

### 8. MCP 服务器无法连接？

- 确认端口未被占用：`lsof -i :8019`
- 确保防火墙未阻止指定端口
- SSE 模式使用 URL: `http://localhost:8019/Phone/sse`
- STDIO 模式需在 MCP 客户端配置中正确设置命令和参数

---

> 更多信息，请访问 [GitHub 仓库](https://github.com/gezihua123/phonefast)
