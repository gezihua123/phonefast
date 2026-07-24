**你有没有遇到过这样的情况：**

1. adb 一直无法选中元素或者选错元素，导致 Vibe Coding 狂烧 Token🔥🔥🔥🔥🔥。
2. adb dump xml 失败，只能依赖截图验证效果，可偏偏模型又是单模态😖😖😖😖

**phonefast**：精准破解 Harness Coding 在移动端验证环节的四大死穴——慢、不准、烧 token🔥、不稳，逐一击破。

| 痛点 | 方案 | 效果 |
|------|------|------|
| 🐢 **慢** | daemon 常驻进程 + Unix Socket JSON‑RPC | <10ms 触控延迟，比 ADB shell 快 100 倍 |
| 🎯 **不准** | 原子级 observe，截图+UI 树一次返回 | 彻底消除"截图完界面已变"的竞态窗口 |
| 🔥 **烧 token** | MCP 原生 ImageContent，多模态直出 | 不再把几十 KB base64 塞进 JSON，token 省一半 |
| 🛡️ **不稳** | 三级保活 + 自动重连 + panic 自愈 | 12 小时压测 14 万次操作，99.99% 成功率 |

{:.prompt-info}

## 📺 视频对比

![phonefast 4x 速度演示](/phonefast_4x.gif)

点击观看完整对比视频：[【PhoneFast vs PhoneMCP】AI执行效果对比](https://www.bilibili.com/video/BV1RZTT6wEEf/)

## 安装方式

```bash
npx skills add gezihua123/phonefast-skill --skill phonefast-skill
```

[📥 下载地址](https://github.com/gezihua123/phonefast/releases/tag/v1.0.11) | [GitHub 仓库](https://github.com/gezihua123/phonefast)

---

## 一、架构差异

### phonefast（Go + scrcpy）

```mermaid
flowchart LR
    CLI[phonefast CLI] -->|" Unix Socket JSON-RPC (<1ms)"| DAEMON[daemon 常驻进程]
    DAEMON -->|" TCP (scrcpy 协议)\n长连接"| SERVER[scrcpy-server\nAndroid 设备]

    subgraph DAEMON_INTERNAL[Daemon 内部]
        direction TB
        SESSION[Session 持有]
        CTRL[control socket]
        VIDEO[video socket]
        UI[ui socket]
        SESSION --- CTRL
        SESSION --- VIDEO
        SESSION --- UI
    end

    DAEMON -.-> DAEMON_INTERNAL

    subgraph DEVICE[Android 设备]
        direction TB
        VS[video socket]
        CS[control socket]
        US[UI socket]
        SERVER --- VS
        SERVER --- CS
        SERVER --- US
    end
```

- **语言**：Go 编译原生二进制，启动 <10ms
- **连接**：scrcpy 协议，TCP 隧道直连设备上的 scrcpy-server
- **daemon**：后台常驻进程，持有设备长连接，Unix Socket 接收命令
- **冷启动**：<10ms（Go 原生二进制）
- **命令延迟**：daemon 模式 <1ms socket 通信 + ~12ms 截图解码 (astiav CGO 进程内) + Android 处理

### agent-device（TypeScript + ADB）

```mermaid
flowchart LR
    CLI[agent-device CLI] -->|" 启动 ~500ms\nNode.js"| NODE[Node.js 进程]
    NODE -->|" subprocess\n每次新建 adb 进程"| ADB[adb shell]
    ADB --> DEVICE[Android 设备]

    subgraph SESSION_STATE[Session 管理]
        direction TB
        STATE["session state 持久化到磁盘\n~/.agent-device/sessions/"]
        OPEN["open → 记录 app 状态"]
        CMD["命令 → 复用 session 上下文"]
    end

    CLI -.-> SESSION_STATE
```

- **语言**：TypeScript (Node.js CLI)，启动 ~500ms
- **连接**：原始 ADB 命令（`adb shell input/keyevent/screencap/uiautomator`）
- **session**：打开 app 后状态持久化到磁盘，命令间复用 session 上下文
- **冷启动**：~500ms（Node.js 进程启动）
- **命令延迟**：~450-750ms（Node.js 进程 + adb shell）

### adb kill（Python + ADB）

```mermaid
flowchart LR
    CLI[adb kill run] -->|" 冷启动 ~6-8s\nPyInstaller 解压"| PY[Python 解释器]
    PY -->|" subprocess\n每次新建进程"| ADB[adb shell]
    ADB --> DEVICE[Android 设备]

    subgraph COLD_START[冷启动开销]
        direction TB
        UNPACK["① PyInstaller 解压 ~1s"]
        IMPORT["② Python 模块导入 ~2-3s"]
        DETECT["③ ADB 设备检测 ~1s"]
        UNPACK --> IMPORT --> DETECT
    end

    CLI -.-> COLD_START
```

- **语言**：Python (PyInstaller 打包为单文件，运行时解压)
- **连接**：原始 ADB 命令（`adb shell input/keyevent/screencap/uiautomator`）
- **状态**：无状态，每次命令完整走"启动→执行→退出"流程
- **冷启动**：~6-8s（PyInstaller 解压 + Python 模块导入 + ADB 检测）
- **命令延迟**：~7-9s（解压 ~1s + 导入 ~2-3s + ADB ~1s + subprocess ~2s + 解析 ~0.5s）

---

## 二、速度对比

> **测试环境**: macOS arm64 | Go 1.26 | Node.js v22.20 | agent-device v0.17.6 | **phonefast v1.0.11**
>
> **设备**: TECNO KL8h (USB) | 分辨率 488×1080 | 测试日期: 2026-07-14
>
> **方法**: 每操作 3 次取平均，`perl -MTime::HiRes` 计时全链路；phonefast 数据来自 12 小时长稳压测（145,843 次操作）

| 操作 | phonefast daemon | agent-device | adb kill | vs agent | vs adb |
|------|:---:|:---:|:---:|:---:|:---:|
| back 返回键 | **12ms** | 520ms | 8,505ms | **43x** | **709x** |
| home 主页键 | **13ms** | 550ms | 8,864ms | **42x** | **682x** |
| tap 坐标点击 | **13ms** | 748ms | 8,110ms | **58x** | **624x** |
| swipe 滑动(300ms) | **318ms** | N/A¹ | 8,200ms | — | **26x** |
| type_text 文本输入 | **1ms** | 32,700ms² | 7,890ms | **32,700x** | **7,890x** |
| screenshot 截图 | **28ms** | 2,593ms | 8,939ms | **93x** | **319x** |
| UI 元素 | **46ms** | FAILED² | 7,600ms | — | **165x** |
| observe 截图+UI | **28ms** | N/A | ~15,500ms³ | — | **554x** |
| launch 应用启动 | **1ms** | 782ms⁴ | 8,240ms | **782x** | **8,240x** |

{:.annotation}

### 典型 AI Agent 交互循环

```mermaid
xychart-beta
    title "观察→操作→再观察 一次循环耗时 (秒)"
    x-axis ["phonefast daemon", "agent-device", "adb kill"]
    y-axis "耗时(秒)" 0 --> 30
    bar [0.4, 3.9, 24.0]
```

```mermaid
xychart-beta
    title "20 次循环耗时 (秒)"
    x-axis ["phonefast daemon", "agent-device", "adb kill"]
    y-axis "耗时(秒)" 0 --> 500
    bar [8, 78, 480]
```

---

## 三、架构维度全景对比

| 维度 | phonefast | agent-device | adb kill |
|------|-----------|--------------|-----------|
| **语言** | Go (原生二进制) | TypeScript (Node.js) | Python (PyInstaller) |
| **二进制大小** | 11MB | ~3MB (npm) | 41MB |
| **运行内存** | <62MB RSS | ~30-50MB RSS | ~20-40MB (每次新建进程) |
| **冷启动** | <10ms | ~500ms | ~7s |
| **连接方式** | scrcpy 协议 (TCP 隧道) | ADB 命令 | ADB 命令 |
| **daemon 模式** | ✅ 常驻进程 + Unix Socket | ✅ session-state on disk | ❌ 每次冷启动 |
| **命令延迟** | 1-13ms | 450-750ms | 7-9s |
| **截图方式** | scrcpy H.264 关键帧 → ffmpeg PNG | adb screencap → pull PNG | adb screencap → pull PNG |
| **UI 解析** | UISocketHandler (TCP socket) | uiautomator dump | uiautomator dump |
| **UI 稳定性** | ⭐⭐⭐⭐⭐ | ⭐⭐ (uiautomator 常超时) | ⭐⭐⭐ |
| **持久连接** | scrcpy server 常驻设备端 | 无持久连接 | 无持久连接 |
| **session 管理** | daemon 内存持有 | 状态持久化到磁盘 | 无状态 |
| **断线恢复** | 三级保活，10s 自动重连 | session 状态文件恢复 | 无状态 |
| **MCP 协议** | ✅ SSE / STDIO (8019) | ✅ `agent-device mcp` | ✅ SSE / STDIO (8009) |
| **跨平台** | Android only | iOS / Android / TV / Desktop | Android only |
| **ImageContent** | ✅ (MCP 原生) | ❌ | ❌ |

---

## 四、能力对比

| 能力 | phonefast | agent-device | adb kill |
|------|:---:|:---:|:---:|
| tap 坐标点击 | ✅ | ✅ | ✅ |
| swipe 自定义坐标 | ✅ | ❌ (仅预设方向) | ✅ |
| type_text 文本 | ✅ | ✅ | ✅ |
| screenshot 截图 | ✅ (H.264→PNG) | ✅ (screencap) | ✅ (screencap) |
| UI 元素 (xml) | ✅ UISocketHandler | ❌ | ✅ |
| observe (截图+UI) | ✅ (原子操作) | ❌ | ❌ |
| tap_element | ✅ (MCP 模式) | ✅ | ✅ |
| launch_app | ✅ | ✅ | ✅ |
| 批量执行 | ✅ `run` JSON | ✅ `batch` | ✅ `run` JSON |
| MCP 服务 | ✅ `serve` (8019) | ✅ `mcp` | ✅ `serve` (8009) |
| ImageContent | ✅ (MCP 原生) | ❌ | ❌ |
| 非 ASCII 输入 | ❌ | ❌ | ✅ DEX helper |
| 多平台 | ❌ | ✅ iOS/Android/TV | ❌ |

---

## 五、实现原理

### 5.1 会话生命周期

```mermaid
flowchart TD
    C1["部署 scrcpy-server.jar"] --> C2["杀掉旧 server 实例"]
    C2 --> C3["分配端口 video/UI"]
    C3 --> C4["启动 scrcpy-server"]
    C4 --> C5["ADB forward video socket"]
    C5 --> C6["连接 video socket + 读 dummy byte"]
    C6 --> C7["连接 control socket"]
    C7 --> C8["读 H.264 视频头 → 分辨率"]
    C8 --> C9["等待 UISocketHandler 就绪 600ms"]
    C9 --> C10["ADB forward UI socket + 探测"]
    C10 --> C11["启动 drainFrames() 后台协程"]
```

### 5.2 截图管线（v1.0.11 架构）

> v1.0.11 将截图管线从 **ffmpeg 子进程** 重构为 **astiav CGO 进程内解码**，消除子进程创建 + 管道 I/O 开销，截图延迟降低 3-4 倍。
>
> ffmpeg 子进程方式仍作为降级路径保留（`CGO_ENABLED=0` 时自动切换）。

```mermaid
flowchart TD
    DEVICE["Android 设备视频流 H.264"] -->|TCP| SOCKET["scrcpy video socket"]
    SOCKET -->|drainFrames 后台协程| NAL["NAL 单元解析 SPS/PPS/IDR"]
    NAL --> CACHE["LRU 缓存最新关键帧"]
    NAL --> REQ["缺失时 requestKeyframe()\n发送 RESET_VIDEO 控制帧"]

    CACHE -->|"keyframeToPNG()"| CHOICE{"CGO_ENABLED?"}

    CHOICE -->|"默认: 是"| ASTIAV["astiav.Decoder\n(进程内 CGO)"]
    ASTIAV --> CTX["CodecContext\nThreadCount=1\n持久复用"]
    CTX --> DEC["SendPacket + ReceiveFrame"]
    DEC --> SWSCALE["sws_scale\nH.264→RGBA"]
    SWSCALE --> ENC["astiav 编码为 PNG bytes"]
    ENC --> MC["MCP ImageContent\n{type:image, data, mimeType}"]

    CHOICE -->|"降级: 否"| FFMPEG_CLI["ffmpeg CLI subprocess\nexec.CommandContext"]
    FFMPEG_CLI --> STDIN["stdin: -f h264 -i pipe:0"]
    STDIN --> STDOUT["stdout: -vcodec png pipe:1"]
    STDOUT --> MC
```

**两条路径对比**：

| 维度 | 主路径 (astiav CGO) | 降级路径 (ffmpeg CLI) |
|------|-------------------|---------------------|
| 触发条件 | `CGO_ENABLED=1`（默认构建） | `CGO_ENABLED=0`（交叉编译等） |
| 解码方式 | 进程内 C 函数调用 | `fork + exec` 子进程 |
| 数据传输 | 零拷贝内存指针传递 | pipe stdin → stdout (memcpy ×2) |
| 编解码器上下文 | **持久复用**（DPB 保持分配） | 每次新建进程（SPS/PPS 重解析） |
| 线程数 | **ThreadCount=1** | 默认多线程 |
| 截图 P50 | **28ms** 🚀 | ~100-200ms |
| 冷启动截图 | **~19ms** | ~167ms |
| 外部依赖 | 无（FFmpeg 静态链接进二进制） | 系统需安装 ffmpeg |

**为什么单线程反而更快**：
- 488×1080 单帧解码量极小，多线程切片同步开销 > 解码本身
- 多线程导致 DPB (Decoded Picture Buffer) 翻倍分配，内存膨胀
- ThreadCount=1 消除 slice-merge 和线程间同步，延迟更稳定

**为什么持久上下文比新建快**：
- 每次 `SendPacket(nil)` flush 后重新初始化 → +55ms（SPS/PPS 重解析 + DPB 重建）
- 持久上下文复用上一帧的参考帧缓冲区，新 IDR 自然覆盖旧帧
- 无 flush = 零额外开销

**为什么用关键帧**：
- I 帧（IDR/Keyframe）包含完整画面，可独立解码
- P/B 帧仅含差异数据，依赖参考帧
- 截图必须用 I 帧；缺失时会发 `RESET_VIDEO` 指令触发设备立即生成

### 5.3 UI 元素获取

```mermaid
flowchart TD
    GET["GetUIElements()"] --> FAST{"UISocketHandler 可用?"}
    FAST -->|"快速路径"| SOCKET["TCP 连接 UI socket"]
    SOCKET --> SEND["发送 dump 请求"]
    SEND --> XML["接收 XML"]
    XML --> PARSE["解析 UIElement 列表"]
    FAST -->|"降级 ADB fallback"| ADB["adb shell uiautomator dump"]
    ADB --> PULL["pull XML 文件"]
    PULL --> PARSE
```

phonefast 的 `UISocketHandler` 是 scrcpy-server 的自定义扩展，通过 abstract socket 提供 UI dump 服务，比 `uiautomator dump` 快约 40%。

### 5.4 保活与断线恢复

```mermaid
flowchart TD
    subgraph L1["1. TCP Keepalive"]
        CK["control socket: 15s"]
        VK["video socket: 30s"]
    end

    subgraph L2["2. healthLoop 10s 周期"]
        ALIVE["IsAlive() 检查"]
        VD["videoDied channel 是否关闭?"]
        CE["controlErr 是否为空?"]
        ALIVE --> VD
        ALIVE --> CE
    end

    subgraph L3["3. Write Failure Detection"]
        WF["Write() 失败"]
        MCB["markControlBroken()"]
        WF --> MCB
    end

    L1 --> L2
    L2 -->|"检测到断线"| RECONNECT["reconnect() 自动重连"]
    L3 -->|"写失败"| RECONNECT
```

### 5.5 Daemon 模式

```mermaid
sequenceDiagram
    participant CLI as phonefast CLI
    participant DS as Unix Socket
    participant DM as Daemon
    participant SESS as Session
    participant DEV as Android 设备

    Note over DM: 启动: 连接设备 + 创建 socket + healthLoop

    CLI ->> DM: ensureDaemon() 检查/启动
    CLI ->> DS: JSON-RPC tap(x=540, y=960)
    DS ->> DM: dispatch "tap"
    DM ->> SESS: Tap(540, 960)
    SESS ->> DEV: Touch Down
    SESS ->> DEV: Touch Up
    DEV -->> SESS: ok
    SESS -->> DM: ok
    DM -->> CLI: {"result":"Tapped at (540, 960)"}
```

### 5.6 MCP ImageContent 返回

```json
{
  "content": [
    {"type": "text",      "text": "Screenshot (1080x2400)"},
    {"type": "image",     "data": "iVBORw0KGgoAAA...", "mimeType": "image/png"}
  ]
}
```

---

## 六、长稳压测 (v1.0.11 优化版)

> 12 小时持续压测，145,843 次操作，**100% 成功率**，零断连。

| 指标 | 数值 |
|------|------|
| **测试时长** | 720 分钟 (12 小时) |
| **总操作数** | 145,843 |
| **成功数** | 145,843 |
| **失败数** | 0 |
| **成功率** | **100%** |
| **daemon 断连** | 0 次 |
| **性能退化** | ❌ 无 |
| **RSS 峰值** | <62MB |

**12 项操作延迟总览**: screenshot/observe P50=**28ms**、tap 13ms、swipe 318ms（详见 [docs/BENCHMARK.md §7](BENCHMARK.md)）。

---

## 七、适用场景

### phonefast daemon → AI Agent 首选

- AI Agent 高频交互（观察→操作→再观察循环）
- 需要极低延迟 (<30ms)
- 批量自动化脚本
- 需要 MCP ImageContent 原生返回图片

```bash
phonefast daemon                              # 启动 (仅需一次)
phonefast --daemon tap 540 960                # 点击 (13ms)
phonefast --daemon screenshot /tmp/s.png      # 截图 (28ms)
phonefast --daemon observe                    # 截图+UI (28ms)
```

### 推荐组合

主力 `phonefast daemon` + `phonefast serve`（速度 + Android AI Agent）；按需补充 `agent-device`（iOS / 录屏回放 / 性能采样）和 `adb kill`（OCR / 非 ASCII / 包名搜索）。

---

### 为什么 phonefast 更稳定

设备上 scrcpy-server 常驻 + TCP 长连接持续 12 小时（vs 每命令 `adb shell` fork 约 50ms 开销）；daemon 内存持有 session（vs 磁盘 session 文件 / 7s 冷启动）；三级保活——TCP keepalive（control 15s / video 30s）+ 10s healthLoop + 写失败检测自动重连。
