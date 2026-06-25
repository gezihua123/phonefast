# phonefast 架构分析

> 日期: 2026-06-12

## 1. 现状分析

### 1.1 瓶颈定位

当前 Android 真机 UI 测试使用 phone-mcp (Python MCP server)，底层通过 ADB 子进程调用操作设备：

| 操作 | 实现方式 | 延迟来源 | 实测耗时 |
|------|---------|---------|---------|
| 截图 | `adb shell screencap -p /sdcard/tmp.png` + `adb pull` | ADB 往返 ×2 + 磁盘 I/O ×2 | 1-2s |
| UI 树 | `adb shell uiautomator dump /sdcard/ui_dump.xml` + `adb shell cat` | ADB 往返 ×2 + 磁盘 I/O + XML 解析 | 1-3s |
| 触控 | `adb shell input tap x y` | ADB 往返 ×1 + InputManager 注入 | ~200ms |

单页观测 = 截图 + UI 树串行 = 3-5s。30 页遍历 = 90-150s 纯等待时间。

### 1.2 scrcpy 架构参考

scrcpy 是成熟的 Android 屏幕镜像工具，架构已证明高性能：

- **设备端**: Java server (`scrcpy-server.jar`) 通过 `app_process` 启动
- **Socket 通信**: 本地抽象 socket (`scrcpy_<scid>`) 提供视频流 + 控制通道
- **视频**: `MediaCodec` → `SurfaceEncoder` → H.264 帧流（12 字节头 + 帧数据）
- **控制**: 二进制协议通过控制 socket 注入触控/按键/文本/启动应用
- **延迟**: 端到端 < 50ms

### 1.3 优化策略

复用 scrcpy 已建立的 socket 通道基础设施，新增一条 UI hierarchy socket：

```
scrcpy-server (设备端)
├── video socket   → H.264 帧流（已有，用于截图）
├── control socket → 二进制控制协议（已有，用于操控）
└── ui socket      → JSON UI hierarchy dump（新增）
```

Golang PC 端同时持有三条连接：
- 截图：从 video socket 抓取最新关键帧（~20ms）
- UI 树：向 ui socket 发送 dump 请求（~50ms）
- 控制：走 control socket 注入触控/按键（< 10ms）

## 2. 技术选型

### 2.1 为什么用 Golang？

| 维度 | Python (phone-mcp) | Golang (phonefast) |
|------|-------------------|-------------------|
| 并发模型 | asyncio (单线程事件循环) | goroutine (原生多核) |
| H.264 处理 | 调用 ffmpeg subprocess | 可选 CGO/FFmpeg 或纯 Go 解码 |
| 部署 | 需要 Python 环境 + pip 依赖 | 单一静态二进制 |
| 内存占用 | ~50MB+ | ~10-20MB |
| 启动速度 | ~500ms | <50ms |
| 二进制分发 | ❌ | ✅ (`go build` 产出单文件) |

### 2.2 scrcpy-server 补丁策略

选择在 scrcpy-server 内部新增 UI socket handler，而非独立 Android agent：

- **优势**: 无需额外进程、共享同一个 app_process 实例、自动随 scrcpy-server 生命周期管理
- **成本**: ~150 行 Java 新增代码，零侵入原逻辑
- **升级路径**: 补丁可独立维护，跟随 scrcpy 上游更新时冲突极小

### 2.3 性能预期

| 指标 | phone-mcp | phonefast | 提升倍数 |
|------|----------|-----------|---------|
| 截图 | 1-2s | <50ms | 20-40x |
| UI 树 dump | 1-3s | <100ms | 10-30x |
| 触控注入 | ~200ms | <10ms | 20x |
| **单页观测** | **3-5s** | **<200ms** | **15-25x** |
| 30 页遍历 | 90-150s | <6s | 15-25x |

## 3. 模块详解

### 3.1 `pkg/protocol` — 协议编码

- `control.go`: scrcpy 控制协议常量 + 消息编码（~250 行）
  - 支持 touch/keycode/text/scroll/start_app/back 六种消息类型
  - 二进制格式：1 字节 type + 可变长度 payload
  - 完全兼容 scrcpy ControlMessage.java 格式

- `ui.go`: phonefast UI dump 协议（~70 行）
  - 请求: `"dump\0"` (5 bytes)
  - 响应: 4 字节 big-endian 长度 + JSON payload
  - UIElement 格式兼容 phone-mcp

### 3.2 `pkg/h264` — H.264 帧解析

- `decoder.go`: scrcpy 视频流解析器（~220 行）
  - 读取 12 字节帧头 (pts+flags + size)
  - 识别 SPS/PPS 配置帧和 IDR 关键帧
  - 组装完整的 AnnexB 关键帧（SPS + PPS + IDR）
  - 不依赖 CGO（v1 截图回退到外部 ffmpeg）

### 3.3 `internal/adb` — ADB 交互层

- `device.go`: 设备发现、ADB shell 封装
- `deploy.go`: scrcpy-server.jar 部署和启动

### 3.4 `internal/session` — 会话管理

- `session.go`: 会话生命周期（连接/关闭/重连）
  - `adb forward` 为 3 条 socket 建立 TCP 转发
  - 自动重试连接（最多 5 次，200ms 间隔）
  - 后台 goroutine 持续排干视频帧

- `video.go`: 截图实现
  - `Screenshot()`: 从 decoder 获取最新关键帧 → PNG
  - `WaitStable()`: 等待视频流稳定
  - 降级方案：无 real H.264 decoder 时返回占位图

- `control.go`: 设备控制
  - `Tap/Swipe/Back/Home/TypeText/LaunchApp/Scroll`
  - `Observe()`: 并行截图 + UI dump

- `ui.go`: UI 树获取
  - 优先走快速 ui socket
  - 回退到 `adb shell uiautomator dump`（兼容无补丁场景）
  - 内置 uiautomator XML 解析器（避免 `encoding/xml` 对非标准 XML 的脆弱性）

### 3.5 `internal/mcp` — MCP 服务

- `server.go`: MCP SSE/STDIO 服务器（JSON-RPC 2.0）
- `tools.go`: 13 个 MCP tool 实现，完全兼容 phone-mcp 接口
  - `list_devices / screenshot / get_ui_elements`
  - `tap / tap_element / swipe / type_text`
  - `back / home / press_key / launch_app / wait`
  - `observe` (新增：一次调用同时获取截图 + UI 树)

### 3.6 `cmd/phonefast` — CLI 入口

- `main.go`: CLI 入口（~170 行）
  - `phonefast serve`: 启动 MCP 服务器（SSE/STDIO）
  - `phonefast run '{"action":"screenshot"}'`: CLI 单次调用模式
  - `phonefast devices`: 列出设备

## 4. 兼容性与回退

### 4.1 零侵入 MCP 接口

所有 MCP tool 名称和参数格式完全兼容 phone-mcp，现有 Skill（如 `Agents.md` 中定义的 QA/UI 工作流）无需改动即可切换后端。

### 4.2 无补丁回退

若 scrcpy-server 未打 UI socket 补丁，`session.ui.go` 自动回退到 `adb shell uiautomator dump`，功能不受影响（仅 UI dump 速度退回 1-3s）。

### 4.3 Android 兼容性

| 组件 | 最低 API | 说明 |
|------|---------|------|
| scrcpy video | API 21 (5.0) | MediaCodec + SurfaceControl |
| scrcpy control | API 21 | InputManager.injectInputEvent |
| UI handler | API 21 | AccessibilityService / UiAutomation |

## 5. 待完成项

### Phase 1 剩余

- [ ] H.264 关键帧 → PNG 实际解码（集成 FFmpeg 或纯 Go decoder）
- [ ] 视频帧变化检测（WaitStable 升级为像素级差分）
- [ ] session 层集成测试（需要真实设备/模拟器）

### Phase 2

- [ ] scrcpy-server 实际集成 UISocketHandler 并重新构建 jar
- [ ] ui socket 协议性能基准测试
- [ ] 多设备支持（多 session 管理）
- [ ] 并发安全性审查（goroutine 竞态）

### Phase 3

- [ ] AccessibilityService 增量 UI 树推送（替代 on-demand dump）
- [ ] VLM 集成：视频帧 → VLM 实时 UI 审查
- [ ] 性能监控面板（每页观测耗时分布）
