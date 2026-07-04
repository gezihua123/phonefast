# phonefast 开发笔记

> 开发过程中的排查记录、架构决策和踩坑经验。

## 目录

- [LocalSocket 4字节读取限制（Android 14）](#localsocket-4字节读取限制android-14)
- [构建与发布](#构建与发布)

---

## LocalSocket 4字节读取限制（Android 14）

### 问题

`UISocketHandler` 的 `handleClient` 中，使用 `DataInputStream.readByte()` 逐字节从 `LocalSocket` 读取请求。当请求超过 **4个字符**（即调用 `readByte()` 超过4次）时，Android 14 设备会**静默重置连接**：

```
dump\0    (4次 readByte + \0) → ✓ 正常
dump.\0   (5次 readByte + \0) → ✗ 连接被重置
dump:5\0  (6次 readByte + \0) → ✗ 连接被重置
dumpp\0   (5次 readByte + \0) → ✗ 连接被重置
```

### 排查过程

1. **初步观察**：用户反馈 `get ui elements` 报 `exit status 137`（ADB uiautomator dump 被 OOM 杀死）。但 daemon 状态显示 `ui: true`，说明快速 socket 通道已建立。

2. **定位到 fallback**：分析代码发现 `handleGetUIElements` 先尝试快速 socket，失败后 fallback 到 ADB uiautomator dump。错误只显示 ADB 的失败信息，快速 socket 的原始错误被吞掉了。

3. **确认 socket 可用**：用 Python 直接连接 ADB 转发的端口 (`localhost:27246`)，发现 `dump\0` 能正常返回 10KB+ 的 UI 数据。

4. **缩小范围**：对比测试发现：
   - `dump\0` → OK
   - `dump:5\0` → 连接立即关闭
   - `dump:500\0` → 连接立即关闭
   - 甚至 `dump.\0`（5个字符） → 连接立即关闭

5. **关键发现**：**任何超过4字节的请求都会失败**。确认是 `readByte()` 调用次数的问题，而非内容。

6. **根因**：Android 14 的 `LocalSocket` + `DataInputStream.readByte()` 存在兼容性 BUG。连续 `readByte()` 超过4次后，底层 native `read()` 调用会导致 socket 连接被静默重置。这与 `read(byte[], int, int)` 批量读取不同——后者使用不同的 native 路径。

### 修复方案

**核心思路**：用 `InputStream.read(byte[], int, int)` 批量读取前4字节（1次 native call），之后才用 `read()` 逐字节读取剩余部分。

```java
// Before (有问题的逐字节读取):
byte[] req = readUntilNull(in); // 内部循环调用 readByte()

// After (批量读前4字节 + 逐字节读后续):
byte[] prefix = new byte[4];
in.read(prefix, 0, 4);        // 批量读取，1次 native call
int sep = in.read();           // 分隔符（':' 或 '\0'）
if (sep == ':') {
    // 逐字节解析数字
    int b = in.read();
    ...
}
```

**协议保持兼容**：`dump:N\0` 格式不变，Go 侧照常发送 `:N` 参数。Java 侧解析时用新的读取方式。

**涉及文件**：
- `android/phonefast-agent/UISocketHandler.java` — Java 侧修复
- `pkg/protocol/ui.go` — Go 侧写请求（格式不变）
- `internal/session/ui.go` — 客户端兜底截断
- `scripts/build-server.sh` — 构建时自动用最新源文件覆盖

### 验证

| 请求 | 预期 | 结果 |
|---|---|---|
| `dump\0` | 默认 500 元素 | ✓ |
| `dump:5\0` | 5 个元素 | ✓ |
| `dump:500\0` | 500 个元素 | ✓ |
| `dump:10000\0` | 解析到 10000 → cap 500 | ✓ |
| `dump:-5\0` | 非法字符 → 默认 500 | ✓ |
| `dump:5a\0` | 部分解析 → 默认 500 | ✓ |
| `sum\0` | 50 元素 (summary) | ✓ |
| `sum:3\0` | 3 元素 (summary) | ✓ |

### 经验教训

- **不要假设 `DataInputStream.readByte()` 在所有设备上行为一致**。Android 碎片化严重，底层 `SocketInputStream` 的实现因厂商定制而异。
- **快速 socket 的错误被 fallback 路径吞掉了**，导致用户只看到 `exit 137`。应该在 fallback 时同时日志记录原始错误。
- **本地用 Python/nc 直连 socket 是极佳的调试手段**，能绕过 Go 代码的 fallback 逻辑，直接看到 Java 服务器的原始行为。

---

## 构建与发布

### 构建 server jar

```bash
bash scripts/build-server.sh
```

流程：
1. 克隆 scrcpy v3.3.4
2. 应用 `android/patches/0001-phonefast-uisocket.patch`
3. 用 `android/phonefast-agent/UISocketHandler.java` 覆盖（保持最新）
4. Gradle 构建 server APK
5. 复制到 `android/scrcpy-server.jar` 和 `assets/scrcpy-server.jar`

**注意**：`android/patches/` 中的 patch 是基线版本，最新代码在 `android/phonefast-agent/` 中。构建脚本会自动覆盖。

### 全平台构建

```bash
bash scripts/build.sh --all
```

构建产物输出到 `dist/dev/`。

### GitHub Release

```bash
# dry-run 试运行（只构建不发布）
bash scripts/release.sh --dry-run

# 自动版本自增 + 发布
bash scripts/release.sh

# 指定版本
bash scripts/release.sh 1.0.4
```

前置条件：
- `gh` CLI 已登录（`gh auth login`）
- Go 工具链
- Git

Release 流程：
1. 检查工作区干净
2. 自动 patch 版本号自增
3. 全平台交叉编译
4. 创建 Git tag 并推送
5. 创建 GitHub Release，上传所有构建产物
