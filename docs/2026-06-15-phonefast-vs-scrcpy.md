# phonefast 实现 vs scrcpy 设计 — 对比分析

> 日期: 2026-06-15

---

## 1. 启动流程对比

### scrcpy 原版设计

```
adb push jar → adb forward → app_process 启动 → LocalServerSocket accept()
→ 客户端连接 video socket → 客户端连接 control socket
→ 服务端继续执行，启动 AsyncProcessor 列表
```

**关键约束**：
- `DesktopConnection.open()` 顺序调用两次 `accept()`，必须连两个 socket 才能解阻塞
- `CleanUp` 默认 fork 进程立即删除 jar（`cleanup=true`）
- 服务端版本号必须和第一个命令行参数匹配（`BuildConfig.VERSION_NAME`）

### phonefast 实现

```go
// internal/adb/deploy.go
serverParts := []string{"3.3.4"} // 硬编码版本号
serverParts = append(serverParts, "cleanup=false")  // 禁止自删除
serverParts = append(serverParts, "tunnel_forward=true")

// internal/session/session.go
s.videoConn, _ = dialWithRetry(s.videoPort, 10, 500ms)  // 解 accept #1
s.controlConn, _ = dialWithRetry(s.videoPort, 5, 200ms) // 解 accept #2
// ↑ 只有连完两个，UISocketHandler 才会初始化！
s.uiConn, _ = dialWithRetry(s.uiPort, 5, 200ms)
```

**✅ 正确之处**：
- 顺序连接 video → control 的时序是正确的
- `cleanup=false` 必要，代码中已添加

**⚠️ 问题一：版本号硬编码**

```go
// deploy.go 第 70 行
serverParts := []string{"3.3.4"} // 必须等于 BuildConfig.VERSION_NAME
```

scrcpy 服务端在 `Options.parse()` 第一件事就是版本比对：
```java
if (!clientVersion.equals(BuildConfig.VERSION_NAME)) {
    throw new IllegalArgumentException("version mismatch");
}
```
一旦 scrcpy 升级，phonefast 必须同步修改此字符串，否则连接失败。**建议**：从 jar 的 BuildConfig 类中读取版本号，或把版本号写入 scrcpy-server.jar 旁边的 `.version` 文件。

**⚠️ 问题二：forward 时序错误**

```go
// session.go 第 60-68 行
// Step 4: start server
adb.StartServer(...)

// Step 5: forward video socket
adbForward(serial, s.videoPort, socketBase)

// Step 6: forward UI socket
adbForward(serial, s.uiPort, uiSocketName)  // ← UI socket 还没存在！
```

问题：服务端还没连接 video/control 就先 forward UI socket。此时设备上 `scrcpy_XXXXXXXX_ui` 这个 abstract socket 根本还不存在（需等服务端跑过 `UISocketHandler.start()`），导致 forward 命令对应的 adb 转发是空的，连接失败。

**正确顺序应为**：
```
1. 启动服务端
2. forward video socket
3. 连 video socket（解 accept #1）
4. 连 control socket（解 accept #2，服务端继续执行到 UISocketHandler.start()）
5. 等待 ~500ms 让 UISocketHandler 线程真正 bind socket
6. forward UI socket
7. 连 UI socket
```

**⚠️ 问题三：adb forward 参数错误**

```go
// session.go 第 176 行
cmd := exec.Command(adbPath, "-s", serial, "forward", "--remove",
    fmt.Sprintf("localabstract:%s", socketName))
```

`adb forward --remove` 的正确语法是移除 **本地端口** 的转发，而非 abstract socket 名称。应为：
```go
adb -s SERIAL forward --remove tcp:LOCAL_PORT
// 或
adb -s SERIAL forward --remove-all
```

---

## 2. 视频流处理对比

### scrcpy 原版设计（Streamer.java）

```java
// 帧头 12 字节，Big Endian
headerBuffer.putLong(ptsAndFlags);   // bit63=config, bit62=keyframe, 其余=PTS(µs)
headerBuffer.putInt(packetSize);

// 视频头 12 字节（发一次，在第一帧前）
buffer.putInt(codec.getId());        // 0x68323634 = h264
buffer.putInt(videoSize.getWidth()); // Big Endian
buffer.putInt(videoSize.getHeight());
```

### phonefast 实现（pkg/h264/decoder.go）

```go
// 帧头解析 ✅ 正确
ptsAndFlags := binary.BigEndian.Uint64(header[0:8])
size := int(binary.BigEndian.Uint32(header[8:12]))
isConfig := (ptsAndFlags & PacketFlagConfig) != 0
isKeyframe := (ptsAndFlags & PacketFlagKeyFrame) != 0

// 视频头解析 ✅ 正确
d.codec = binary.BigEndian.Uint32(buf[0:4])
d.width = int(binary.BigEndian.Uint32(buf[4:8]))
d.height = int(binary.BigEndian.Uint32(buf[8:12]))
```

**✅ 字节序完全正确**（曾有 LittleEndian 的 bug，已修复）。

**⚠️ 问题：SPS/PPS 解析逻辑不健壮**

```go
// decoder.go extractConfigs()
// 用 FindStartCode 扫描 AnnexB 数据提取 SPS/PPS
startOffset := offset - 4
if startOffset < 0 || data[startOffset] != 0 || data[startOffset+1] != 0 {
    startOffset = offset - 3  // 可能是 00 00 01 (3 字节起始码)
}
```

这段逻辑在识别 3 字节起始码时有边界问题：`offset - 4` 可能越界，且 3 字节起始码情况下会把 NAL 数据前的字节包含进 SPS/PPS。

**实测正确的方式**（来自 test_e2e.py 验证）：
```python
# scrcpy 的 config 数据已经是完整 AnnexB，直接整体使用
full_keyframe = sps_pps_data + idr_data  # 直接拼接，无需解析
```

`configRaw` 备用路径（直接存储整个 config 数据包）实际上是更可靠的实现，但当前代码优先走有 bug 的 SPS/PPS 解析路径。

**建议**：移除复杂的 SPS/PPS 解析，统一使用 `configRaw + idr` 直接拼接。

**⚠️ 问题：drainFrames 没有退出保护**

```go
// session.go
func (s *Session) drainFrames() {
    for {
        s.mu.Lock()
        if s.closed { s.mu.Unlock(); return }
        s.mu.Unlock()

        conn := s.videoConn
        _, err := s.decoder.ReadFrame(conn)
        if err != nil { return }  // ← socket 断开时退出 OK
    }
}
```

`conn := s.videoConn` 在锁外读取，存在 `Close()` 被并发调用后 conn 为 nil 的竞态。正确方式应在锁内同时取 conn 并检查 nil。

**⚠️ 问题：截图实现走 ffmpeg 子进程**

```go
// video.go
func decodeViaFFmpeg(keyframe []byte, ...) ([]byte, error) {
    cmd := exec.CommandContext(ctx, "ffmpeg",
        "-f", "h264", "-i", "pipe:0",
        "-frames:v", "1", "-f", "image2pipe", "-vcodec", "png", "pipe:1")
    cmd.Stdin = bytes.NewReader(keyframe)
    ...
}
```

每次截图都要 fork ffmpeg 进程，冷启动 ~50-100ms，且依赖系统安装 ffmpeg。scrcpy 客户端用 libavcodec 内嵌解码，0 进程开销。

---

## 3. 控制协议对比

### scrcpy 原版（ControlMessageReader.java）

```java
// INJECT_TOUCH_EVENT
int action = dis.readUnsignedByte();
long pointerId = dis.readLong();         // 8 bytes
Position position = parsePosition();
float pressure = Binary.u16FixedPointToFloat(dis.readShort()); // ← u16 定点数！
int actionButton = dis.readInt();
int buttons = dis.readInt();
```

**注意 pressure 编码**：scrcpy 用 `u16 fixed-point`（`[0, 1]` 映射到 `[0, 65535]`），而非 IEEE float32。

### phonefast 实现

```go
// protocol/control.go Encode() INJECT_TOUCH_EVENT
buf = append(buf, byte(m.Action))
buf = binary.BigEndian.AppendUint64(buf, uint64(m.PointerID))
buf = appendPos(buf, m.Position)
pressure := math.Float32bits(m.Pressure)         // ← IEEE float32 ❌
buf = binary.BigEndian.AppendUint32(buf, pressure) // ← 4字节，但服务端读 2字节！
buf = binary.BigEndian.AppendUint32(buf, uint32(m.ActionBtn))
buf = binary.BigEndian.AppendUint32(buf, uint32(m.Buttons))
```

**严重错误**：
1. pressure 应编码为 `u16 fixed-point`（2 字节），phonefast 写了 IEEE float32（4 字节）
2. 多写了 2 字节导致后续 actionButton 和 buttons 的对齐全部错位
3. 实际触控注入会失败或行为异常

**正确编码**：
```go
// pressure: u16 fixed-point = clamp(p, 0, 1) * 65535
pressureU16 := uint16(m.Pressure * 65535)
buf = binary.BigEndian.AppendUint16(buf, pressureU16)  // 2 字节
buf = binary.BigEndian.AppendUint32(buf, uint32(m.ActionBtn))
buf = binary.BigEndian.AppendUint32(buf, uint32(m.Buttons))
```

同样，`INJECT_SCROLL_EVENT` 的 hScroll/vScroll 也应为 `i16 fixed-point`（`[-16, 16]` 映射到 `[-32768, 32767]`），当前 phonefast 写的是 IEEE float32。

**✅ 正确实现的部分**：
- `INJECT_KEYCODE`: 1B type + 1B action + 4B keycode + 4B repeat + 4B metaState ✅
- `INJECT_TEXT`: 1B type + 4B length + UTF-8 ✅
- `BACK_OR_SCREEN_ON`: 1B type + 1B action ✅
- `TYPE_RESET_VIDEO` (17): 1B type only ✅

---

## 4. UI 树对比

### scrcpy UISocketHandler（phonefast 新增）

```java
// handleClient 逻辑
in.readFully(requestBuf);               // 读 "dump\0" (5 bytes)
byte[] jsonBytes = dumpUIHierarchy();   // 调用 AccessibilityNodeInfo
out.writeInt(jsonBytes.length);         // 4B Big-Endian 长度
out.write(jsonBytes);                   // JSON 数据
out.flush();
socket.close();                         // ← 每次请求后关闭连接
```

**重要**：每次请求后 **连接被关闭**（try-with-resources）。

### phonefast 实现

```go
// session/ui.go GetUIElements()
func (s *Session) GetUIElements() ([]protocol.UIElement, error) {
    if s.uiConn == nil {
        return nil, fmt.Errorf("ui socket not available")
    }
    protocol.WriteUIDumpRequest(s.uiConn)     // 发 "dump\0"
    resp, err := protocol.ReadUIDumpResponse(s.uiConn)  // 读响应
    return resp.Elements, nil
    // ← uiConn 未关闭！下次调用会读到 EOF
}
```

**严重错误**：`s.uiConn` 在服务端关闭后持续被复用，第二次调用必然失败（读 EOF / broken pipe）。

test_e2e.py 的正确用法（每次新建连接）：
```python
def dump_ui():
    ui = socket.socket()        # 每次新建
    ui.connect(("localhost", UI_PORT))
    ui.send(b"dump\x00")
    l = ui.recv(4)
    data = ui.recv(ulen)
    ui.close()                  # 每次关闭
```

**建议修复**：`GetUIElements()` 每次新建连接，或改造 UISocketHandler 支持持久连接。

---

## 5. ADB 层对比

### phonefast 实现（internal/adb/deploy.go）

```go
// StartServer 构建命令行
func StartServer(serial string, info *ScrcpyServerInfo, args ScrcpyArgs) error {
    serverParts := []string{"3.3.4"}
    serverParts = append(serverParts, fmt.Sprintf("scid=%d", args.Scid))
    ...
    argStr := strings.Join(serverParts, " ")

    shellCmd := fmt.Sprintf(
        "CLASSPATH=%s nohup app_process / %s %s > /dev/null 2>&1 &",
        info.DevicePath, info.ServerClass, argStr)

    cmd := exec.Command(adbPath, "-s", serial, "shell", shellCmd)
    ...
}
```

**⚠️ 问题：参数含空格时 shell 解析会出错**

如果某个参数值包含空格（如 `video_encoder=some encoder name`），`strings.Join` 拼出的字符串在 shell 展开时会被错误分割。scrcpy 原版用数组传参，phonefast 降级为字符串拼接。

**✅ 正确之处**：
- 使用 `nohup ... &` 确保后台运行
- 传 `cleanup=false` 防止 jar 被删

**⚠️ 问题：ScrcpyServerInfo.JarPath 查找逻辑**

```go
func findScrcpyJar() string {
    candidates := []string{
        filepath.Join(projectRoot(), "android", "scrcpy-server.jar"),
        // 依赖 runtime.Caller(0) 获取源码路径 ← 编译后二进制中失效！
        filepath.Join(projectRoot(), "..", "code", "scrcpy", ...),
    }
}

func projectRoot() string {
    _, file, _, ok := runtime.Caller(0)  // ← 获取源码文件路径
    return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}
```

`runtime.Caller(0)` 获取的是**编译时**的源码绝对路径，在二进制部署后失效（路径不存在）。只有 `android/scrcpy-server.jar` 相对于二进制的位置才是可靠的。

---

## 6. MCP 层对比

### phonefast 实现（internal/mcp/server.go）

```go
// JSON-RPC 2.0 消息处理
func handleJSONRPC(server *Server, line string) string {
    var req jsonrpcRequest
    json.Unmarshal([]byte(line), &req)
    ...
}
```

**✅ 协议基本正确**：实现了 `initialize`、`tools/list`、`tools/call`。

**⚠️ 问题：SSE 实现不符合 MCP 规范**

```go
// server.go handleSSE()
func handleSSE(server *Server, w http.ResponseWriter, r *http.Request) {
    scanner := bufio.NewScanner(r.Body)
    for scanner.Scan() {
        result := handleJSONRPC(server, line)
        fmt.Fprintf(w, "data: %s\n\n", result)
        flusher.Flush()
    }
}
```

MCP SSE 规范要求：
- 客户端通过 `POST /messages` 发请求
- 服务端通过 SSE 连接推送响应
- phonefast 把两者混在了一个 HTTP handler 里，不符合规范

**⚠️ 问题：tools.go 有手写字符串工具函数**

```go
func toLower(s string) string { return string(toLowerBytes([]byte(s))) }
func contains(s, substr string) bool { ... }
func indexOf(s, substr string) int { ... }
func lastIndex(s, substr string) int { ... }
func repeatString(s string, n int) string { ... }
func joinStrings(parts []string, sep string) string { ... }
```

这些功能 `strings` 标准库全部提供，手写容易出 bug，且性能差。

---

## 7. 整体差距总结

| 维度 | scrcpy 原版 | phonefast 当前 | 严重度 |
|------|------------|---------------|--------|
| **启动时序** | 精确的 accept 顺序，CleanUp 机制 | 基本正确，forward UI 时序有误 | 🟡 中 |
| **版本号** | 运行时校验 | 硬编码 "3.3.4" | 🟡 中 |
| **视频帧解析** | BigEndian，精确 | BigEndian 正确，SPS 解析有边界 bug | 🟡 中 |
| **截图性能** | 内嵌解码，<5ms | ffmpeg 子进程，~100ms | 🟡 中 |
| **pressure 编码** | u16 fixed-point (2B) | IEEE float32 (4B) ❌ | 🔴 严重 |
| **scroll 编码** | i16 fixed-point (2B) | IEEE float32 (4B) ❌ | 🔴 严重 |
| **UI socket 复用** | 服务端每次关闭连接 | 客户端复用已关闭连接 ❌ | 🔴 严重 |
| **并发安全** | 线程安全设计 | drainFrames 存在竞态 | 🟡 中 |
| **MCP SSE** | N/A | 不符合 MCP 规范 | 🟡 中 |
| **ADB 参数** | 数组传参 | 字符串拼接，有注入风险 | 🟡 中 |
| **测试覆盖** | 无 | 18 个单元测试 | ✅ |

---

## 8. 修复状态

### ✅ 已修复（2026-06-15）

| Bug | 文件 | 修复内容 |
|-----|------|---------|
| pressure 编码 | `pkg/protocol/control.go` | `math.Float32bits` (4B) → `floatToU16Fixed()` (2B u16 fixed-point) |
| scroll 编码 | 同上 | `math.Float32bits` (4B) → `int16(v*2048)` (2B i16 fixed-point) |
| UI socket 复用 | `internal/session/ui.go` | 每次调用新建连接，`defer conn.Close()` |
| forward 时序 | `internal/session/session.go` | 连 video+control 后 sleep 600ms 再 forward UI socket |
| drainFrames 竞态 | `internal/session/session.go` | `conn` 在 mu 锁内取出 |
| SPS/PPS 解析 bug | `pkg/h264/decoder.go` | 改为直接使用 `configRaw`，移除有边界问题的 NAL 解析 |
| 新增测试 | `pkg/protocol/control_test.go` | `TestTouchMsgFullLayout`、`TestTouchPressureEncoding`、`TestScrollMsgLayout` |

---

## 9. 优先修复建议（剩余）

### P0 必须修复（功能性错误）

**Fix 1: pressure 编码 (control.go)**
```go
// 修改前
pressure := math.Float32bits(m.Pressure)
buf = binary.BigEndian.AppendUint32(buf, pressure)  // 4 字节

// 修改后
pressureU16 := uint16(m.Pressure * 65535.0)
buf = binary.BigEndian.AppendUint16(buf, pressureU16)  // 2 字节
```

**Fix 2: scroll hScroll/vScroll 编码 (control.go)**
```go
// 修改前
buf = binary.BigEndian.AppendUint32(buf, math.Float32bits(m.HScroll))  // 4 字节

// 修改后
// i16 fixed-point: [-16, 16] → [-32768, 32767]
hScrollI16 := int16(m.HScroll / 16 * 32767)
buf = binary.BigEndian.AppendUint16(buf, uint16(hScrollI16))  // 2 字节
```

**Fix 3: UI socket 每次新建连接 (ui.go)**
```go
func (s *Session) GetUIElements() ([]protocol.UIElement, error) {
    // 每次新建连接
    conn, err := net.DialTimeout("tcp",
        fmt.Sprintf("localhost:%d", s.uiPort), 2*time.Second)
    if err != nil {
        return nil, err
    }
    defer conn.Close()

    protocol.WriteUIDumpRequest(conn)
    resp, err := protocol.ReadUIDumpResponse(conn)
    ...
}
```

**Fix 4: forward UI socket 时序 (session.go)**
```go
// 当前错误顺序：启动→forward→连接→UISocketHandler初始化
// 正确顺序：
// 1. 启动服务端
// 2. forward video/control socket
// 3. 连接 video socket（解 accept #1）
// 4. 连接 control socket（解 accept #2，服务端跑到 UISocketHandler）
// 5. Sleep 500ms 等 UISocketHandler bind 完成
// 6. forward UI socket
// 7. 连接 UI socket
```

### P1 建议改进

**Fix 5: 使用 configRaw 而非 SPS/PPS 解析**
```go
func (d *Decoder) buildKeyframe(idr []byte) []byte {
    var buf bytes.Buffer
    if d.configRaw != nil {
        buf.Write(d.configRaw)  // 直接用整个 config 数据包
    }
    buf.Write(idr)
    return buf.Bytes()
}
```

**Fix 6: 版本号从 jar manifest 读取**
```go
// 从 META-INF/MANIFEST.MF 或嵌入文件读取版本
func getScrcpyVersion(jarPath string) string {
    // 解压 jar，读取 BuildConfig.class 或写入独立 .version 文件
}
```

**Fix 7: drainFrames 竞态修复**
```go
func (s *Session) drainFrames() {
    for {
        s.mu.Lock()
        if s.closed { s.mu.Unlock(); return }
        conn := s.videoConn  // 在锁内取 conn
        if conn == nil { s.mu.Unlock(); return }
        s.mu.Unlock()

        _, err := s.decoder.ReadFrame(conn)
        if err != nil { return }
    }
}
```
