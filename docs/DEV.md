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

phonefast 把 FFmpeg 静态链接进 Go CGO 二进制，实现单文件分发（jar + FFmpeg 全部内嵌）。
有两条构建路径：

#### 方案 2：本地 zig 交叉编译（开发日常用）

在 macOS 上一条命令编出全部 4 个平台二进制。本机 darwin-arm64 用原生 clang，
其余目标用 zig cc 交叉编译（asm 全开，已验证）。`build_local.sh` 是一键封装：

```bash
# 一键全平台 (自动: 环境检查 → 编 FFmpeg 库 → 编 Go 二进制)
bash scripts/build_local.sh            # 全平台 4 目标
bash scripts/build_local.sh --macos    # 仅 darwin
bash scripts/build_local.sh --linux    # 仅 linux
bash scripts/build_local.sh --windows  # 仅 windows
bash scripts/build_local.sh --clean    # 构建前清理 dist/
```

底层步骤（build_local.sh 自动完成，也可手动）：

```bash
# 1. 检测/安装构建环境 (nasm/zig/clang/go)
bash scripts/build_env.sh check        # 检测
bash scripts/build_env.sh install      # 自动装缺失依赖 (brew)

# 2. 编静态 FFmpeg 库 (每目标一次，缓存于 build/cross-ffmpeg/<target>/)
bash scripts/cross-build-ffmpeg.sh aarch64-darwin    # mac arm64
bash scripts/cross-build-ffmpeg.sh x86_64-linux-gnu  # linux amd64
bash scripts/cross-build-ffmpeg.sh aarch64-linux-gnu # linux arm64
bash scripts/cross-build-ffmpeg.sh x86_64-windows-gnu # windows amd64

# 3. 构建
bash scripts/build.sh            # 仅本机 darwin-arm64 (默认)
bash scripts/build.sh --all      # 全平台 4 目标
bash scripts/build.sh --linux    # 仅 linux
```

产物在 `dist/dev/`：`phonefast-<os>-<arch>[.exe]` + `.tar.gz`。

#### 方案 3：CI 原生 runner（正式 release 用）

每个平台用对应架构的原生 GitHub Actions runner，零模拟、asm 全开、最稳。
推 `v*` tag 自动触发：`.github/workflows/release.yml`。

```bash
# 本地打 tag 推送即触发 CI 全平台构建 + 发版
git tag v1.0.8 && git push origin v1.0.8
# 或 Actions 页面手动 Run workflow (输入版本号)
```

CI matrix（每平台原生 runner）：

| 目标 | runner | FFmpeg 工具链 |
|---|---|---|
| darwin-arm64 | macos-14 | 原生 clang + nasm |
| darwin-arm64 | macos-14 | 原生 clang |
| linux-amd64 | ubuntu-latest | 原生 gcc + nasm |
| linux-arm64 | ubuntu-24.04-arm | 原生 gcc (NEON) |
| windows-amd64 | windows-latest | 原生 mingw + nasm |

> 公开仓库 CI 全免费（含 macOS runner 和 arm64 linux runner）。

### 构建环境与 asm 判定

`scripts/build_env.sh` 是统一的环境检测/安装入口：

```bash
bash scripts/build_env.sh           # 报告
bash scripts/build_env.sh check     # 检测，缺依赖返回非 0
bash scripts/build_env.sh install   # brew 自动装缺失 (nasm/zig/go)
```

**asm 判定逻辑**（`cross-build-ffmpeg.sh`）：
- `x86_64` 目标：需 nasm（SSE/AVX/AVX2/AVX-512）。有则开，无则降级 `--disable-asm`（纯 C 慢 2-4×）。
- `aarch64` 目标：NEON 走 assembler（zig 内置 / clang gas），无需 nasm，始终开。
- 装好 nasm 即全平台 asm-on：`bash scripts/build_env.sh install`。

### 静态 FFmpeg 编译的关键坑

`cross-build-ffmpeg.sh` 踩过并修复的坑（供后人排查）：

1. **zig + nasm 开 asm**：早期版本因没装 nasm 硬编码 `--disable-asm`。装 nasm 后 zig cc
   自动探测，全平台 asm-on。运行时验证：asm-off 报 `No accelerated colorspace conversion found`，
   asm-on 后 amd64 目标警告消失。

2. **darwin 强制 Apple 原生 ar/ranlib**：若 PATH 上有 GNU binutils（`brew install binutils`），
   其 ar 产出 GNU 格式 `.a`（符号表成员名为 `/`），Apple ld 不认 →
   `archive member '/' not a mach-o file`。darwin 分支强制 `/usr/bin/ar`、`/usr/bin/ranlib`、`/usr/bin/nm`
   产 BSD 格式 `.a`，无需 libtool 重封装。

3. **不能 libtool 重封装 darwin .a**：早期用 `ar -x` + `libtool -static *.o` 重封装修复 SYMDEF，
   但 `ar -x` 会让 aarch64 与根目录同名的 `.o`（如 `aarch64/swscale.o` vs `swscale.o`）互相覆盖，
   丢 NEON init 符号 → `symbol(s) not found for architecture arm64`。Apple 原生 ar 已生成有效 SYMDEF，
   重封装步骤已删除。

4. **mingw C99 math 冲突**（windows 目标）：zig-mingw 下 configure 的 math 函数探测全失败
   （`HAVE_TRUNC/ROUND/CBRT/...=0`），FFmpeg `libm.h` 用 static inline 重定义，与 mingw math.h 的
   extern 声明冲突 → `static declaration follows non-static declaration`。`mingw_math` patch 把这些
   `HAVE_*` 翻成 1（用 mingw 系统版本），并注释掉冲突的 `#define getenv(x) NULL`。

5. **GCC + PIC + x86 inline asm**（Linux-host 分支）：`--enable-pic` + GCC 会触发
   `impossible constraint in 'asm'`（mathops.h NEG_USR32）。Linux 静态库链进 Go 不需要 PIC
   （Go 链接器自行重定位），Linux-host 分支不开 PIC。zig 分支不受此影响（已验证 PIC 可用）。

### 为什么不用 docker

arm64 Mac 上用 docker 编 amd64 必走 qemu 模拟，gcc 编 FFmpeg 的 SIMD/asm 会
`internal compiler error: Segmentation fault`（qemu+gcc 已知不稳，无可靠 workaround）。
业界共识：能交叉编译就别用模拟。故 linux/windows 目标走 zig 交叉（方案2）或 CI 原生 runner（方案3），
docker 路线已移除。

### GitHub Release

`release.sh` 只负责触发 CI，不本地构建、不直接创建 Release。
推 `v*` tag 后，GitHub Actions（方案3）全平台原生编译并发布。

```bash
# dry-run 预览 (不打 tag, 不触发 CI)
bash scripts/release.sh --dry-run

# 自动版本自增 + 触发 CI 发版
bash scripts/release.sh

# 指定版本
bash scripts/release.sh 1.0.8
```

前置条件：
- Git（必须，推 tag 用）
- gh（可选，事后查看 CI/Release）

Release 流程：
1. 检查工作区干净
2. 自动 patch 版本号自增 + commit
3. 创建 Git tag `v${VERSION}`
4. push tag → **触发 CI** → CI 4 平台原生编译 → 发布 GitHub Release

产物最终位置：GitHub Release 的 Assets
`https://github.com/gezihua123/phonefast/releases/tag/vX.Y.Z`

> 本地手动出包（不发布）用 `bash scripts/build_local.sh`（方案2）。
> CI 产物与本地产物可交叉校验。
