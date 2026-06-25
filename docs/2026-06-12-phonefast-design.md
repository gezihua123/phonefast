# phonefast 设计文档

> 日期: 2026-06-12
> 状态: 已确认

## 1. 问题定义

Android 自愈开发中，真机 UI 测试的观测环节存在严重性能瓶颈：

| 操作 | 当前方式 | 耗时 |
|------|---------|------|
| 截图 | `adb shell screencap` + `adb pull` | 1-2s |
| UI 树 | `adb shell uiautomator dump` + `adb shell cat` | 1-3s |
| **单页观测合计** | 串行执行 | **3-5s** |
| 30 页遍历 | — | **90-150s** |

核心问题：每次观测都要走 ADB 通道，涉及子进程启动、磁盘写入、两次 ADB 往返。

## 2. 解决方案

复用 scrcpy 已建立的设备端本地 socket 通道（视频流 + 控制注入），并新增一条 UI hierarchy socket，将单页观测从 3-5s 压到 ~100ms（约 30x 提升）。

### 2.1 核心思路

```
之前: screencap(1.5s) → adb pull → uiautomator dump(1.5s) → adb cat = 3s 串行
之后: 视频抓关键帧(20ms) + socket UI dump(50ms) = ~80ms 并行
```

### 2.2 架构概览

```
┌─────────────────────────────────────────────────────┐
│  phonefast (Golang) - PC 侧                          │
│                                                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐          │
│  │  video   │  │ control  │  │    ui    │          │
│  │ socket   │  │ socket   │  │  socket  │          │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘          │
│       │              │              │                │
│  ┌────┴─────┐  ┌─────┴─────┐  ┌────┴─────┐         │
│  │ H.264    │  │ 二进制    │  │ JSON     │         │
│  │ 帧解码   │  │ 控制协议  │  │ UI 树    │         │
│  └──────────┘  └───────────┘  └──────────┘         │
│                                                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐          │
│  │   MCP    │  │   CLI    │  │   ADB    │          │
│  │ Server   │  │  Runner  │  │ 管理     │          │
│  └──────────┘  └──────────┘  └──────────┘          │
└─────────────────────────────────────────────────────┘
       │              │              │
       ▼              ▼              ▼
   SSE/STDIO    CLI JSON      ADB USB/WiFi
       │              │              │
       ▼              ▼              ▼
┌─────────────────────────────────────────────────────┐
│  Android 设备                                        │
│                                                      │
│  ┌──────────────────────────────────────────────┐   │
│  │  scrcpy-server (app_process, 已有)            │   │
│  │  ├── video socket → MediaCodec H.264 帧流     │   │
│  │  ├── control socket → InputManager 触控注入   │   │
│  │  └── ui socket [新增] → UiAutomation dump    │   │
│  └──────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
```

## 3. 模块设计

### 3.1 项目结构

```
phonefast/
├── cmd/phonefast/
│   └── main.go                 # CLI 入口 (serve / run)
├── internal/
│   ├── adb/
│   │   ├── device.go           # 设备发现、连接
│   │   └── deploy.go           # push + 启动 scrcpy-server
│   ├── session/
│   │   ├── session.go          # 会话生命周期管理
│   │   ├── video.go            # H.264 解码 → 关键帧 → PNG
│   │   ├── control.go          # 触控/按键/文本注入
│   │   └── ui.go               # UI 树请求/解析
│   └── mcp/
│       ├── server.go           # MCP SSE/STDIO 服务
│       └── tools.go            # MCP tool 注册 (兼容 phone-mcp)
├── pkg/
│   ├── protocol/
│   │   ├── control.go          # scrcpy 控制协议常量 + 编解码
│   │   └── ui.go               # UI dump 请求/响应协议
│   └── h264/
│       └── decoder.go          # H.264 AnnexB 解析 + 关键帧提取
├── android/
│   └── phonefast-agent/        # scrcpy-server 补丁 (ui socket 新增)
│       └── src/.../UISocketHandler.java
├── go.mod
├── go.sum
└── docs/
    └── 2026-06-12-phonefast-design.md
```

### 3.2 模块职责

| 模块 | 职责 | 输入 | 输出 |
|------|------|------|------|
| `adb` | 设备发现、scrcpy-server 部署和启动 | ADB path, device serial | 设备列表, 部署状态 |
| `session` | 单设备会话生命周期，管理 3 条 socket | device serial | Session 实例 |
| `video` | 从 video socket 读 H.264 帧，提取最新关键帧 | video socket fd | PNG/JPEG 字节 |
| `control` | 通过 control socket 注入触控/按键/文本 | 坐标/文本/按键码 | — |
| `ui` | 通过 ui socket 请求 UI hierarchy | request | JSON UI 元素列表 |
| `mcp` | MCP 协议服务，暴露截图/操作/UI树等 tool | MCP 请求 | MCP 响应 |

### 3.3 Android 侧修改（scrcpy-server 补丁）

在现有 scrcpy-server 的 `Server.java` 中新增一个 `UISocketHandler`：

- 监听独立的 local socket（`scrcpy_ui_<scid>`）
- 收到 `dump` 请求时，调用 `UiAutomation.takeSnapshot()` 获取当前 UI 树
- 直接序列化为 JSON 写回 socket（不做 XML 中转）
- 可选：注册 AccessibilityService 实现增量推送（v2 迭代）

改动量预估：新增 ~150 行 Java，不改 scrcpy 原有任何逻辑。

## 4. 数据流

### 4.1 单次页面观测 (observe)

```
observe() {
    // 并行执行
    go: 从 video socket 读最新关键帧 → 解码 → PNG
    go: 向 ui socket 发送 "dump\0" → 读取 JSON → 解析 UI 元素
    
    wait_all → 返回 {screenshot, ui_elements}
}

// 目标延迟: < 200ms (端到端)
```

### 4.2 页面遍历 (traverse)

```
traverse(pages) {
    for page in pages {
        navigate_to(page)      // 走 control socket 跳转
        wait_stable()          // 视频帧变化检测 → 页面稳定
        result = observe()     // 并行抓帧 + dump UI 树
        yield result
    }
}
```

## 5. 协议定义

### 5.1 Control 协议（复用 scrcpy）

参考 `ControlMessage.java`，Golang 侧按相同二进制格式编解码：

| Type | 值 | 用途 |
|------|----|------|
| INJECT_KEYCODE | 0 | 按键注入 |
| INJECT_TEXT | 1 | 文本输入 |
| INJECT_TOUCH_EVENT | 2 | 触控按下/移动/抬起 |
| INJECT_SCROLL_EVENT | 3 | 滚动 |
| BACK_OR_SCREEN_ON | 4 | 返回/亮屏 |
| START_APP | 16 | 启动应用 |

消息格式：1 字节 type + 可变长度 payload（按类型编码）

### 5.2 UI 协议（新增）

```
request:  "dump\0"              (5 bytes)
response: 4 字节 big-endian 长度 + JSON 字节
```

JSON 格式兼容 phone-mcp `UIElement`：

```json
{
  "elements": [
    {
      "index": 0,
      "text": "设置",
      "content_desc": "",
      "resource_id": "com.example:id/btn_settings",
      "class_name": "android.widget.Button",
      "bounds": [16, 48, 200, 144],
      "center": [108, 96],
      "clickable": true,
      "enabled": true
    }
  ]
}
```

## 6. MCP 接口

兼容 phone-mcp 现有 tool 名，Agent/Skill 可无缝切换：

| Tool | 之前实现 | phonefast 实现 |
|------|---------|---------------|
| `screenshot` | ADB screencap+pull | video socket 抓关键帧 |
| `get_ui_elements` | ADB uiautomator dump+cat | ui socket dump |
| `tap` | ADB input tap | control socket INJECT_TOUCH_EVENT |
| `tap_element` | get_ui_elements + tap | ui dump + control touch |
| `swipe` | ADB input swipe | control socket scroll/touch |
| `type_text` | ADB input text | control socket INJECT_TEXT |
| `back` | ADB input keyevent 4 | control socket BACK_OR_SCREEN_ON |
| `home` | ADB input keyevent KEYCODE_HOME | control socket INJECT_KEYCODE |
| `launch_app` | ADB am start | control socket START_APP |
| `list_devices` | ADB devices | ADB devices |
| `wait` | time.sleep | 视频帧变化检测 + 稳定等待 |

## 7. 错误处理

| 场景 | 策略 |
|------|------|
| scrcpy 进程崩溃 | Session 层检测 → 自动 restart，最多 3 次，指数退避 (1s, 2s, 4s) |
| video socket 断流 | 等待下一个 keyframe，超时 3s 返回错误 |
| UI dump 超时 (1s) | 回退到 `adb shell uiautomator dump` 作为保底 |
| control socket 写入失败 | 重试 1 次，失败则 report error |
| 设备 USB 断开 | 清理所有 socket → MCP 层返回 "device disconnected" |

## 8. 性能目标

| 指标 | 当前 (phone-mcp) | phonefast 目标 | 提升 |
|------|-----------------|---------------|------|
| 截图 | 1-2s | < 50ms | 20-40x |
| UI 树 dump | 1-3s | < 100ms | 10-30x |
| 触控注入 | ~200ms (ADB) | < 10ms (socket) | 20x |
| 单页观测 (截图+UI) | 3-5s | < 200ms | 15-25x |
| 30 页遍历 | 90-150s | < 6s | 15-25x |

## 9. 依赖与约束

- **Go 1.21+** — PC 侧主语言
- **Android 5.0+** (API 21) — 目标设备最低版本
- **scrcpy v3.x server** — 版本锁定在桌面已有的 `code/scrcpy` 代码
- **无音频需求** — 忽略 audio socket
- **单设备优先** — v1 只处理单设备，多设备后续扩展

## 10. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| 修改 scrcpy-server 后需跟进上游更新 | 维护成本 | 改动极小且隔离，合并冲突概率低 |
| 某些设备 UiAutomation 不可用 | UI dump 失败 | 自动回退到 ADB uiautomator dump |
| H.264 解码 CPU 开销 | PC 性能 | 只解关键帧（I-frame），不解 P/B 帧 |
| 关键帧间隔影响截图延迟 | 截图可能滞后 1-3 帧 | 触发 I-frame 刷新请求（control socket RESET_VIDEO） |

## 11. 实施计划

### Phase 1: 基础骨架 (Day 1-2)
- [ ] Go 项目初始化，模块结构搭建
- [ ] ADB 设备发现 + scrcpy-server 部署
- [ ] video socket 连接 + H.264 AnnexB 解析

### Phase 2: 核心通道 (Day 3-4)
- [ ] control socket 协议实现（touch/key/text/app）
- [ ] scrcpy-server 补丁：ui socket handler
- [ ] ui socket 客户端 + JSON 解析

### Phase 3: MCP 集成 (Day 5-6)
- [ ] MCP SSE/STDIO server
- [ ] 所有 tool 实现（兼容 phone-mcp 接口）
- [ ] CLI run 模式

### Phase 4: 测试与优化 (Day 7)
- [ ] 单元测试 + 集成测试
- [ ] 性能基准测试
- [ ] 与现有 Skill 对接验证
