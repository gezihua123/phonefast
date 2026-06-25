# Screenshot & ImageContent 实现原理

## 截图管线

```
                 H.264 视频流
Android 设备 ──────────────────→ scrcpy 视频 Socket
                                      │
                                      ▼
                              h264.Decoder (AnnexB 解析)
                                      │
                           ┌──────────┴──────────┐
                           │ drainFrames() 后台协程 │  ← 持续消费帧，保持解码器热
                           │ LatestKeyframe()      │  ← 截图时取最新关键帧
                           └─────────────────────┘
                                      │
                                      ▼
                              ffmpeg (子进程)
                              H.264 → PNG
                                      │
                                      ▼
                              base64 编码
                                      │
                                      ▼
                              MCP ImageContent 返回
```

### 各层职责

| 层级 | 文件 | 职责 |
|------|------|------|
| 视频解码 | `pkg/h264/` | 解析 H.264 AnnexB 格式，提取 NAL 单元，缓存最新关键帧 |
| 帧管理 | `internal/session/video.go` | `Screenshot()` 获取关键帧, `drainFrames()` 持续刷新 |
| 格式转换 | `video.go:keyframeToPNG()` | 调用 ffmpeg 将 H.264 关键帧 → PNG 字节流 |
| MCP 协议 | `internal/mcp/tools.go` | 封装为 MCP `ImageContent` 返回给客户端 |

### 为什么用关键帧（Keyframe / IDR）

H.264 视频流包含两类帧：
- **I 帧（IDR/Keyframe）**：完整画面，可独立解码
- **P/B 帧**：仅存差异数据，依赖参考帧

截图必须用 I 帧。`h264.Decoder.LatestKeyframe()` 返回最近一个完整的 I 帧。
如果当前没有可用的 I 帧（刚连接），`requestKeyframe()` 向设备发送 `RESET_VIDEO` 指令，
触发设备立即生成一个 I 帧，等待最多 3 秒。

### ffmpeg 转换管道

```
H.264 AnnexB 字节流
    │
    ▼  stdin (pipe:0)
ffmpeg -f h264 -i pipe:0 -frames:v 1 -f image2pipe -vcodec png pipe:1
    │
    ▼  stdout (pipe:1)
PNG 字节流
```

关键参数：
- `-f h264`：输入格式为裸 H.264 AnnexB
- `-frames:v 1`：只处理一帧
- `-vcodec png`：输出 PNG 编码
- 超时：5 秒 (`context.WithTimeout`)

---

## MCP ImageContent 协议格式

### 标准 MCP ImageContent

mcp-go v0.54.1 定义（`types.go`）：

```go
type ImageContent struct {
    Type     string `json:"type"`     // 固定 "image"
    Data     string `json:"data"`     // base64 编码的图片数据
    MIMEType string `json:"mimeType"` // MIME 类型，如 "image/png"
}
```

### phonefast 实际返回格式

MCP 模式下 `screenshot` 与 `observe` 使用 mcp-go 原生 `ImageContent` 返回截图。

`screenshot` 返回 `TextContent`（尺寸说明）+ `ImageContent`：

```json
{
  "content": [
    {"type": "text", "text": "Screenshot (1080x2400)"},
    {"type": "image", "data": "iVBORw0KGgoAAAANSUhEUgAA...", "mimeType": "image/png"}
  ]
}
```

`observe` 返回三段：说明文本 + `ImageContent` + UI 元素文本：

```json
{
  "content": [
    {"type": "text", "text": "Observe: 42 interactive elements"},
    {"type": "image", "data": "iVBORw0KGgo...", "mimeType": "image/png"},
    {"type": "text", "text": "Interactive elements on screen:\n[0] ..."}
  ]
}
```

### CallToolResult 结构

```
CallToolResult (screenshot)
├── Content[0]: TextContent   ← 尺寸说明
└── Content[1]: ImageContent  ← base64 PNG

CallToolResult (observe)
├── Content[0]: TextContent   ← 元素数量说明
├── Content[1]: ImageContent  ← base64 PNG
└── Content[2]: TextContent   ← 格式化的 UI 元素列表
```

实现见 `internal/mcp/tools.go` 的 `handleScreenshot` / `handleObserve`，使用 `mcp.NewToolResultImage(...)` 构造。`mcpResultToToolResult`（CLI `run` 通道）已支持把 `ImageContent` 转成 `ToolContent{Type, Data, MimeType}`。

---

## 与通用 MCP 实现的差异

### 1. 图片来源：实时捕获 vs 静态文件

| | 通用 MCP ImageContent | phonefast |
|---|---|---|
| 数据来源 | 本地文件、HTTP 下载、算法生成 | Android 设备实时屏幕 |
| 延迟 | 取决于 IO | ~50ms（取关键帧）+ ~200ms（ffmpeg 转码） |
| 动态性 | 静态 | 每次调用获取最新画面 |

### 2. 传输路径

通用 MCP 服务器的图片路径：
```
磁盘文件 → 读取 → base64 → ImageContent
```

phonefast 的图片路径：
```
ADB Socket → TCP 视频流 → H.264 解码 → ffmpeg 子进程 → base64 → ImageContent
```

关键差异：phonefast 的图片**不经过磁盘**，全程内存管道传输。

### 3. 格式转换必须有外部依赖

通用 MCP 服务器如果已有 PNG 文件，无需任何格式转换。

phonefast 必须依赖 **ffmpeg**（主机安装），因为：
- scrcpy 只输出 H.264 视频流，不输出 PNG
- Go 标准库没有 H.264 解码器
- 纯 Go H.264 解码库会显著增加二进制体积

### 4. 并发控制

通用实现通常无状态，每次请求独立读取。

phonefast 的 `drainFrames()` 是一个**后台协程**持续消费视频帧：
- 截图时取 `decoder.LatestKeyframe()` —— 非阻塞
- 如果锁设计不当，`Screenshot()` 和 `drainFrames()` 会互相死锁
- 解决：`Screenshot()` 只在取帧+尺寸时短暂持锁，等待 I 帧时不持锁

### 5. observe 工具的特殊性

phonefast 的 `observe` 工具在一次调用中并行执行截图 + UI 元素获取：

```go
// video.go:198
func (s *Session) Observe() (screenshot []byte, uiElements []protocol.UIElement, err error) {
    go func() {
        r.pngData, r.w, r.h, r.screenErr = s.Screenshot()
        elems, uiErr := s.GetUIElements()
        // ...
    }()
    // 5s 超时
}
```

返回的 `CallToolResult` 包含：
- `TextContent`：UI 元素列表（可点击元素的 index/text/坐标）
- `ImageContent`：截图

这使得 LLM 可以在一次调用中同时获得画面和可交互元素，减少往返次数。

### 6. CLI 模式兼容

MCP 模式返回 `ImageContent`，CLI 模式 (`phonefast screenshot [file]`) 提供两种输出：
- **有文件名参数**：写入 PNG 文件
- **无参数**：输出 data URI 到 stdout

```bash
# 保存到文件
$ phonefast screenshot screen.png
Screenshot saved to screen.png

# 输出 data URI（可管道给其他工具）
$ phonefast screenshot
data:image/png;base64,iVBORw0KGgo...
```

---

## 相关文件

| 文件 | 说明 |
|------|------|
| `internal/mcp/tools.go` | MCP 工具 handler，封装 ImageContent |
| `internal/session/video.go` | `Screenshot()` 取帧 + ffmpeg 转码 |
| `internal/session/session.go` | `drainFrames()` 后台帧消费 |
| `pkg/h264/` | H.264 AnnexB 解析器 |
| `internal/daemon/rpc.go` | 守护进程的截图 JSON-RPC handler |
| `cmd/phonefast/main.go` | CLI 截图命令入口 |
