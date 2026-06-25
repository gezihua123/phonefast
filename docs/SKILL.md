---
name: phonefast
description: "Android AI 编程提效百倍的 CLI 工具。phonefast 把手机变成 AI Agent 原生外设，通过 daemon 常驻进程 + Unix Socket JSON-RPC 实现毫秒级延迟操控。支持截图、UI 元素获取、点击、滑动、文本输入、应用启动等操作，并提供 MCP 服务（SSE/STDIO）供 AI 调用。当用户提到 phonefast、Android 手机自动化、手机操控、手机截图、手机点击、scrcpy、daemon 模式、MCP 手机、AI 手机外设、Android CLI 工具时激活。"

metadata:
  skillhub.creator: "suyanjiao03"
  skillhub.version: "V1"
  skillhub.source: "personal"

# 软件依赖声明（机器可读）
dependencies:
  - name: adb
    type: required
    scope: host-tool
    affects: all-commands
    install:
      macos: "brew install android-platform-tools"
      linux: "sudo apt install adb"
      windows: "下载 Google platform-tools 并加入 PATH"
    verify: "adb version"
  - name: ffmpeg
    type: required-for-screenshot
    scope: host-tool
    affects: ["screenshot", "observe"]
    install:
      macos: "brew install ffmpeg"
      linux: "sudo apt install ffmpeg"
      windows: "从 ffmpeg.org 下载并加入 PATH"
    verify: "ffmpeg -version"
  - name: android-device-usb-debugging
    type: required
    scope: device
    affects: all-commands
    note: "Android 设备开启 USB 调试，USB 连接（不支持 WiFi）"
---

# phonefast — Android AI 编程提效百倍的 CLI

phonefast 把手机变成了 AI Agent 原生外设。通过 daemon 常驻进程 + Unix Socket JSON-RPC，每次触控延迟不到 10 毫秒，对比 adb shell 方案快 100 倍。

## 软件依赖（速览）

> ⚠️ 使用前必须确认以下外部软件已安装。缺一项则对应功能不可用。

| 软件 | 必要性 | 影响范围 | macOS 安装 | Linux 安装 | 验证 |
|------|--------|----------|------------|------------|------|
| **adb** | 硬依赖（必需） | 所有命令 | `brew install android-platform-tools` | `sudo apt install adb` | `adb version` |
| **ffmpeg** | 截图必需 | screenshot / observe | `brew install ffmpeg` | `sudo apt install ffmpeg` | `ffmpeg -version` |
| **Android 设备** | 必需 | 所有命令 | 开启 USB 调试 + USB 连接 | 开启 USB 调试 + USB 连接 | `adb devices` |

> 纯控制操作（tap/swipe/back/home/type）只需 adb；截图类操作（screenshot/observe）需 adb + ffmpeg。

## 核心优势

- **快** — daemon 常驻进程 + Unix Socket JSON-RPC，触控延迟 <10ms，对比 adb shell 方案 3-5 秒一次，快 100 倍
- **准** — 截图走 H.264 关键帧管道，ffmpeg 直出无损 PNG；`observe` 原子操作一次拿到画面 + 控件树，杜绝竞态
- **省 token** — MCP 模式返回原生 `ImageContent`（`image/png`），LLM 多模态引擎直接像素级识别

## 外部依赖（详细说明）

phonefast 运行依赖以下主机端工具，安装前务必确认已就绪：

### 1. ADB（Android Debug Bridge）— 硬依赖

**用途**：设备发现、scrcpy-server 部署、socket 端口转发、UI dump 兜底、应用启动。phonefast 几乎所有设备交互都经由 adb。

**安装**：
- macOS：`brew install android-platform-tools`
- Linux：`sudo apt install adb`（或对应发行版包名）
- Windows：从 [Google 官方](https://developer.android.com/tools/releases/platform-tools) 下载 platform-tools 并加入 PATH

**验证**：
```bash
adb version          # 确认安装
adb devices          # 确认设备已连接
```

> ⚠️ 无 adb 时，phonefast 任何命令都会失败（无法发现设备）。

### 2. ffmpeg — 截图硬依赖

**用途**：将 scrcpy 视频流的 H.264 关键帧解码为 PNG。`screenshot`、`observe` 命令依赖它；`tap`/`swipe`/`back` 等纯控制操作**不**依赖。

**安装**：
- macOS：`brew install ffmpeg`
- Linux：`sudo apt install ffmpeg`
- Windows：从 [ffmpeg.org](https://ffmpeg.org/download.html) 下载并加入 PATH

**验证**：
```bash
ffmpeg -version      # 确认安装
```

> ⚠️ 无 ffmpeg 时，`screenshot`/`observe` 会返回 "ffmpeg decode" 错误。控制类操作不受影响。

### 3. Android 设备 — USB 调试

- 设备需开启「开发者选项 → USB 调试」
- 用 USB 线连接主机（不支持 WiFi 连接）
- 首次连接时设备会弹窗要求授权调试

### 依赖速查表

| 依赖 | 类型 | 影响范围 | 缺失后果 |
|------|------|----------|----------|
| **adb** | 主机工具（硬依赖） | 所有命令 | 任何命令失败 |
| **ffmpeg** | 主机工具（截图依赖） | screenshot / observe | 截图类命令报错，控制类正常 |
| **USB 调试设备** | 设备端 | 所有命令 | 无法发现设备 |

## 安装

### 前置条件

- 已安装 **adb**（见上节）
- 已安装 **ffmpeg**（如需截图功能）
- Android 设备已开启 USB 调试并连接
- macOS / Linux / Windows 主机

### 下载安装

根据你的平台选择对应二进制包：



**升级脚本（覆盖已有安装）**：

```bash
#!/usr/bin/env bash
# 升级 phonefast：停止 daemon → 下载新版本 → 覆盖安装 → 重启 daemon
set -euo pipefail

echo "[升级] 停止运行中的 daemon ..."
phonefast daemon --stop 2>/dev/null || true

# 重新运行安装脚本（覆盖已有文件）
echo "[升级] 下载并安装新版本 ..."
bash <(curl -fsSL <安装脚本 URL>)

echo "[升级] 重启 daemon ..."
phonefast daemon

echo "[升级] 验证 daemon 状态 ..."
phonefast daemon --status
```

**手动安装步骤**：

```bash
# 1. 下载对应平台的 tar.gz 包（见上表）
# 2. 解压
tar -xzf phonefast-dev-darwin-arm64.tar.gz

# 3. 将二进制和资源文件一起移动到同一目录
install -m 755 phonefast-darwin-arm64 /usr/local/bin/phonefast
install -m 644 scrcpy-server.jar      /usr/local/bin/scrcpy-server.jar
install -m 644 scrcpy-server.version  /usr/local/bin/scrcpy-server.version

# 4. 验证安装
phonefast --version
```

> **注意**：`scrcpy-server.jar` 和 `scrcpy-server.version` 必须与 `phonefast` 二进制放在**同一目录**，否则无法部署到设备。

## 使用

### 快速开始

```bash
# 1. 启动 daemon（仅需一次，后台常驻）
phonefast daemon

# 2. 执行各种操作（默认走 daemon，延迟 <10ms）
phonefast tap 540 960                   # 点击坐标
phonefast tap_element 5                 # 点击第 5 个 UI 元素
phonefast screenshot /tmp/s.png         # 截图（需 ffmpeg）
phonefast observe                       # 截图 + UI 元素（原子操作，需 ffmpeg）
phonefast swipe 100 500 400 500         # 滑动
phonefast type "hello"                  # 输入文本
phonefast key enter                     # 发送回车键
phonefast back                          # 返回键
phonefast home                          # 主页键
phonefast launch com.android.settings   # 启动应用

# 直接模式（--foreground，每次新建连接 ~2.5s）
phonefast --foreground tap 540 960
```

### MCP 模式

phonefast 提供 MCP 服务，支持 SSE 和 STDIO 两种传输模式：

```bash
# SSE 模式（默认端口 8019）
phonefast serve

# STDIO 模式
phonefast serve --transport stdio
```

MCP 模式下 `screenshot` 返回原生 `ImageContent`（base64 PNG）：

```json
{
  "content": [
    {"type": "text", "text": "Screenshot (1080x2400)"},
    {"type": "image", "data": "iVBORw0KGgoAAA...", "mimeType": "image/png"}
  ]
}
```

`observe` 返回三段内容：说明文本 + `ImageContent`（截图）+ UI 元素文本：

```json
{
  "content": [
    {"type": "text", "text": "Observe: 42 interactive elements"},
    {"type": "image", "data": "iVBORw0KGgo...", "mimeType": "image/png"},
    {"type": "text", "text": "Interactive elements on screen:\n[0] ..."}
  ]
}
```

### 批量执行

```bash
# 通过 JSON 文件批量执行命令
phonefast --daemon run commands.json
```

### 完整命令列表

| 命令 | 说明 | 示例 | 依赖 |
|------|------|------|------|
| `daemon` | 启动常驻进程 | `phonefast daemon` | adb |
| `devices` | 列出设备 | `phonefast devices` | adb |
| `status` | Daemon 状态 | `phonefast status` | adb |
| `tap` | 坐标点击 | `phonefast tap 540 960` | adb |
| `tap_element` | 点击 UI 元素 | `phonefast tap_element 5` | adb |
| `swipe` | 滑动 | `phonefast swipe 100 500 400 500` | adb |
| `back` | 返回键 | `phonefast back` | adb |
| `home` | 主页键 | `phonefast home` | adb |
| `key` | 发送按键 | `phonefast key enter` | adb |
| `type` | 输入文本 | `phonefast type "hello"` | adb |
| `screenshot` | 截图 | `phonefast screenshot /tmp/s.png` | adb + **ffmpeg** |
| `observe` | 截图 + UI | `phonefast observe` | adb + **ffmpeg** |
| `ui` | UI 元素 | `phonefast ui` | adb |
| `launch` | 启动应用 | `phonefast launch com.android.settings` | adb |
| `wait` | 等待 | `phonefast wait 500` | — |
| `serve` | MCP 服务 | `phonefast serve` | adb（+ ffmpeg 截图时） |
| `run` | JSON 操作 | `phonefast run '{"action":"tap","args":{"x":540,"y":960}}'` | 视子命令而定 |

## 性能数据

### 延迟对比（毫秒）

| 操作 | phonefast daemon | agent-device | adb kill |
|------|------------------|--------------|----------|
| back 返回键 | **20ms** | 520ms | 8,505ms |
| home 主页键 | **29ms** | 550ms | 8,864ms |
| tap 坐标点击 | **30ms** | 748ms | 8,110ms |
| type 文本输入 | **13ms** | 32,700ms | 7,890ms |
| screenshot 截图 | **167ms** | 2,593ms | 8,939ms |
| UI 元素 | **191ms** | FAILED | 7,600ms |
| observe 截图+UI | **148ms** | N/A | ~15,500ms |
| launch 应用启动 | **11ms** | 782ms | 8,240ms |

### 典型 AI Agent 交互循环

- **phonefast daemon**: 0.4 秒/循环，20 次循环 ≈ 8 秒
- **agent-device**: 3.9 秒/循环，20 次循环 ≈ 78 秒
- **adb kill**: 24 秒/循环，20 次循环 ≈ 480 秒（8 分钟）

### 长稳压测

经 12 小时持续压测（145,400 次操作）验证：100% 成功率、零断连、零内存泄漏。控制类操作 P50 ≈ 12ms，screenshot P50 ≈ 153ms。详见 `docs/BENCHMARK.md`。

## 架构原理

### 会话生命周期

1. 部署 scrcpy-server.jar 到设备（adb push）
2. 杀掉旧 server 实例（adb shell pkill）
3. 分配 scid → 计算 video/UI 端口（端口无冲突分配器）
4. 启动 scrcpy-server（adb shell app_process）
5. ADB forward video socket
6. 连接 video socket + 读 dummy byte
7. 连接 control socket
8. 读 H.264 视频头 → 获取分辨率
9. 等待 UISocketHandler 就绪
10. ADB forward UI socket + 探测
11. 启动 drainFrames() 后台协程

### Actor 模型（daemon 内部）

每个设备由一个 `DeviceActor` goroutine 独占持有 session，通过 channel 与 accept loop 通信：

- **协程隔离** — session 访问零锁，单设备 panic 自动重启不影响其他设备
- **scid 自动分配** — 多设备场景下端口互不冲突
- **reconnect 节流** — 设备掉线时避免请求雪崩，10s health ticker 驱动重连
- **架构预留多设备** — 当前 CLI 一进程一设备，daemon 内部已支持同进程多设备

### 截图管线

Android 设备 H.264 视频流 → TCP → scrcpy video socket → drainFrames → h264.Decoder → 缓存最新关键帧 → **ffmpeg** 转换 → base64 → MCP ImageContent

### 保活机制

- TCP Keepalive（control: 15s, video: 30s）
- Actor healthLoop 10s 周期检测
- Write Failure Detection 自动重连
- reconnect 节流（5s cooldown）防止请求雪崩

## tap 命中率优化

### 失败根因

| 根因 | 说明 | 典型场景 |
|------|------|------|
| **竞态** | UI 树获取与画面渲染不同步，拿到的坐标已过期 | 动画、弹窗、页面加载中 |
| **精度** | 小按钮单点容错不够，坐标映射偏差 | 按钮 <100x100、WebView 内元素 |
| **通道** | scrcpy control socket 与 adb input 注入路径不同 | WebView、SurfaceView、游戏引擎 |

### 五层递进策略

```
L1 原子操作    observe → 立即 tap                            永不拆开调 ui+screenshot
L2 延迟防抖    observe → sleep 0.3 → tap                      弹窗/动画场景
L3 多点连击    tap 3 次覆盖按钮核心区（间隔 50ms）              按钮 <100x100
L4 通道fallback phonefast 失败后 adb shell input tap            WebView/混合应用
L5 闭环确认    tap 后 observe 对比，未命中重试（≤3 次）          必须成功的操作
```

### 场景速查

| 场景 | 层级 | 说明 |
|:---|:--:|------|
| 原生 App 大按钮（>100x100） | L1 | 基本百发百中 |
| 对话框/弹窗 | L2 | 等进入动画结束 |
| 开关/复选框 | L2+L3 | 目标太小，连点覆盖 |
| WebView/网页 | L2+L3+L4 | 坐标映射偏差大，adb 兜底 |
| 关键操作（支付/权限授权） | L2+L3+L4+L5 | 确认闭环不可省略 |

### 多工具 fallback 原则

```
phonefast observe → tap              # 首选，最快
     ↓ 失败
phonefast tap（连点 3 次）            # L3 重试
     ↓ 失败
adb shell input tap <x> <y>          # L4 换通道
     ↓ 失败
adb shell input keyevent KEYCODE     # L4 按键兜底（phonefast key 原生支持，仅作通道兜底）
```

### 特殊能力映射

| 需求 | phonefast | adb 补充 |
|------|:--:|:--:|
| 坐标点击 | `tap` | `shell input tap` |
| UI 元素点击 | `tap_element` | `shell input tap`（坐标） |
| 文本输入 | `type`（仅 ASCII） | `shell input text`（含中文） |
| 按键事件 | `key <name\|keycode>` | `shell input keyevent` |
| 截图+UI 树 | `observe`（原子） | `screencap` + `uiautomator dump`（有竞态） |
| 应用启动 | `launch <package>`（仅包名） | `shell am start` / `monkey -p` |

## 适用场景

### 推荐使用 phonefast daemon

- AI Agent 高频交互（观察→操作→再观察循环）
- 需要极低延迟 (<30ms)
- 批量自动化脚本
- 需要 MCP ImageContent 原生返回图片

### 补充工具

- **phone-mcp**: 需要 AI 视觉识别（像素级 UI 理解）/ MCP 协议集成时
- **agent-device**: 需要 iOS 自动化 / 录屏回放 / 性能采样时
- **adb**: 需要兜底兼容 / 特殊按键 / 非 ASCII 输入 / WebView 点击时

## 约束

- 仅支持 Android 设备（iOS 请使用 agent-device）
- 需要设备开启 USB 调试
- **必须安装 adb**（硬依赖）
- **截图功能必须安装 ffmpeg**（控制类操作不需要）
- 首次启动需要部署 scrcpy-server.jar（随包分发，需与二进制同目录）
- daemon 模式需要保持进程运行
- 不支持 WiFi 连接（仅 USB）
- 不支持非 ASCII 文本输入（中文/emoji 请使用 adb kill）

## 故障排查

| 现象 | 排查 |
|------|------|
| 任何命令失败 | `adb devices` 确认设备在线；确认 adb 已安装 |
| `screenshot` 报 ffmpeg 错误 | 安装 ffmpeg：`brew install ffmpeg` / `apt install ffmpeg` |
| daemon 启动失败 | 确认 `scrcpy-server.jar` 与 phonefast 二进制同目录；查看日志 `/tmp/phonefast-<uid>.log` |
| 延迟变高（~3s） | 确认走 daemon 模式（`--daemon` 前缀），非 daemon 模式每次重连 |
| 设备掉线后无响应 | daemon 10s 自动重连；如需立即恢复执行 `phonefast daemon --stop && phonefast daemon` |
| **tap 经常点不中** | 1) 确认用 `observe` 而非 `ui`+`screenshot` 分步调用 2) 加 `sleep 0.3` 防抖 3) 小按钮用连点 4) WebView 场景 fallback 到 `adb shell input tap` |
| **点完页面卡主无反应** | 1) 等待 1s 让动画/渲染完成 2) 可能是 WebView/游戏引擎走 scrcpy 通道不兼容，换 `adb input` 3) 设备卡顿：重启 daemon `phonefast daemon --stop && phonefast daemon` |

## 验证

执行完成后确认：

1. 命令退出码为 0
2. 截图文件已生成（如执行 screenshot）
3. UI 元素正常返回（如执行 observe）
4. daemon 进程在运行：`ps aux | grep phonefast`


