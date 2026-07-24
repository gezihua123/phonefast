# phonefast 开发笔记

> 开发过程中的排查记录、架构决策和踩坑经验。

## 目录

- [LocalSocket 4字节读取限制（Android 14）](#localsocket-4字节读取限制android-14)
- [构建与发布](#构建与发布)
- [OCR 识别方案调研与选型](#ocr-识别方案调研与选型)
- [基于 agent-device 的 token 优化](#agent-device-优点调研)

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

### 根因

用户反馈 `get ui elements` 报 `exit status 137`（ADB uiautomator dump 被 OOM 杀死），但 daemon 状态 `ui: true` 说明快速 socket 已建立——原始 socket 错误被 fallback 路径吞掉了。用 Python 直连 ADB 转发端口 (`localhost:27246`) 绕过 fallback 定位：`dump\0` 正常返回，但任何超过 4 字节的请求（`dump.\0`/`dump:5\0`/`dump:500\0`）连接立即被重置。

**根因**：Android 14 的 `LocalSocket` + `DataInputStream.readByte()` 存在兼容性 BUG——连续 `readByte()` 超过 4 次后底层 native `read()` 调用导致连接静默重置。而 `read(byte[], int, int)` 批量读取走不同 native 路径无此问题。

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

### H.264 截图解码架构 (v1.0.11)

phonefast 的截图管线从 Android 设备 scrcpy H.264 视频流中提取 IDR 关键帧，解码为 PNG 图片。
有两条路径，编译时选择：

#### 主路径：astiav CGO 进程内解码（默认）

`pkg/avcodec/decode_astiav.go` — 通过 go-astiav 库直接调用 libavcodec/libswscale C API。流程：`AllocCodecContext` → `SendPacket(keyframe)` → `ReceiveFrame` → `sws_scale` → `EncodeImage(PNG)`。

**关键优化**：
- ThreadCount=1：单帧 488×1080 解码量极小，多线程切片同步开销 > 解码本身
- 持久 CodecContext：不复用每次 +55ms（SPS/PPS 重解析 + DPB 重建）
- 帧循环简化：IDR 刚好输出 1 帧，无需旧版本的 AllocFrame 探测循环

#### 降级路径：ffmpeg CLI subprocess（CGO_ENABLED=0）

`internal/session/video.go` — `exec.CommandContext` 起 ffmpeg 子进程，stdin/stdout 管道传数据（`-f h264 -i pipe:0 -vcodec png -f image2pipe pipe:1`，stdin 写 keyframe → 读 stdout PNG）。

**开销**：fork+exec 约 50-80ms + SPS/PPS 重解析 + pipe memcpy ≈ 100-200ms/次

#### 代码结构

```
pkg/avcodec/
├── avcodec.go         — 包文档 + 公共类型 (ImageFormat, DecodeError, ErrNotAvailable)
├── decode.go          — Decoder 接口定义
├── decode_astiav.go   — 主路径: CGO 解码器 (build tag: cgo)
├── decode_nocgo.go    — 降级桩: 返回 ErrNotAvailable (build tag: !cgo)
├── decode_test.go     — 测试 (go test + fuzz)
└── testdata/          — 测资

internal/session/
└── video.go           — keyframeToPNG + decodeViaFFmpeg (降级路径)
```

构建时通过 build tag 选择实现：默认 `CGO_ENABLED=1` 走 astiav 主路径；`CGO_ENABLED=0` 走 ffmpeg CLI 降级路径（需系统有 ffmpeg）。

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

`build_local.sh` 一键封装，自动完成：环境检测/安装（`build_env.sh check/install`）→ 静态 FFmpeg 库交叉编译（`cross-build-ffmpeg.sh`，缓存于 `build/cross-ffmpeg/<target>/`）→ Go 二进制构建。产物在 `dist/dev/`：`phonefast-<os>-<arch>[.exe]` + `.tar.gz`。

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

1. **zig + nasm 开 asm**：早期缺 nasm 硬编码 `--disable-asm`。装 nasm 后 zig cc 自动探测全平台 asm-on（asm-off 会报 `No accelerated colorspace conversion found`，asm-on 后 amd64 警告消失）。
2. **darwin 强制 Apple 原生 ar/ranlib**：PATH 上有 GNU binutils 时其 ar 产 GNU 格式 `.a`（成员名 `/`），Apple ld 不认 → `archive member '/' not a mach-o file`。darwin 分支强制 `/usr/bin/ar`、`/usr/bin/ranlib`、`/usr/bin/nm` 产 BSD 格式 `.a`，无需 libtool 重封装。
3. **不能 libtool 重封装 darwin .a**：`ar -x` + `libtool -static` 会让 aarch64 与根目录同名 `.o`（如 `swscale.o`）互相覆盖，丢 NEON init 符号 → `symbol(s) not found for architecture arm64`。Apple 原生 ar 已生成有效 SYMDEF，重封装步骤已删除。
4. **mingw C99 math 冲突**（windows）：zig-mingw 下 configure 的 math 函数探测全失败（`HAVE_TRUNC/ROUND/...=0`），FFmpeg `libm.h` 的 static inline 重定义与 mingw math.h extern 声明冲突。`mingw_math` patch 把 `HAVE_*` 翻成 1（用 mingw 系统版本）并注释掉冲突的 `#define getenv(x) NULL`。
5. **GCC + PIC + x86 inline asm**（Linux-host）：`--enable-pic` + GCC 触发 `impossible constraint in 'asm'`（mathops.h NEG_USR32）。Linux 静态库链进 Go 不需 PIC（Go 链接器自行重定位），故 Linux-host 分支不开 PIC；zig 分支不受影响。

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

---

## OCR 识别方案调研与选型

### 背景

部分 Android 应用（Google Play、华为应用市场等）使用 Jetpack Compose 渲染 UI。Compose `Text` 组件默认将文字绘制到 Canvas，不走 Android Accessibility API。导致 phonefast 的 `UISocketHandler` 和 `uiautomator dump` 都无法获取这类文字。

**典型案例**：Google Play 应用详情页，标题"小红书-你的生活兴趣"在 accessibility 树中完全不存在，但屏幕上清晰可见。

### 三种文字获取路径对比

| 路径 | 速度 | Compose 文字 | 返回坐标 | token 成本 |
|---|---|---|---|---|
| Accessibility（UISocketHandler） | 191ms | ❌ 拿不到 | ✅ bounds | 低（结构化） |
| LLM 多模态视觉（ImageContent） | 167ms | ✅ LLM 能读 | ❌ 无精确坐标 | 高（图片 3-4x） |
| OCR | 70ms（预期） | ✅ 能读 | ✅ bounding box | 中（text+box） |

**OCR 是唯一能同时返回"文字 + 精确坐标"的方案**——可程序化 `tap center_x center_y`，不依赖 LLM 猜位置。

### 候选方案实测对比

在同一台设备（macOS arm64）、同一张截图（720×1600）上测试：

| 方案 | 推理速度 | 中文准确率 | 模型体积 | Go 集成方式 |
|---|---|---|---|---|
| PaddleOCR v6 (Python) | **3500ms** | ✅ 最好 (conf 0.93) | ~200MB | 子进程 Python |
| RapidOCR (ONNX, Python) | **330ms** | ✅ 好 (conf 0.81) | ~13MB | 子进程 Python |
| Tesseract (C, 子进程) | 142ms | ❌ 白字渐变失败 | ~40MB | 子进程 / CGO |
| Go CGO + ONNX Runtime | **37.5ms** (det) | ✅ 同 RapidOCR | ~13MB | CGO (yalue/onnxruntime_go) |
| Go purego + ONNX Runtime | **35.7ms** (det) | ✅ 同 RapidOCR | ~13MB | **纯 Go (CGO_ENABLED=0)** |

### 选定方案：Go purego + ONNX Runtime + PP-OCR v3

**核心选型理由**：

1. **纯 Go 编译**（`CGO_ENABLED=0`）：`shota3506/onnxruntime-purego` 使用 `ebitengine/purego` 调用 ONNX Runtime C 共享库，**不需要 C 编译器**。与 phonefast 现有的纯 Go 跨平台编译路径完全一致（`GOOS=xxx GOARCH=xxx go build`）。

2. **推理速度最优**：35.7ms det + 5ms rec + 30ms Go 预处理 ≈ **70ms 全流程**。比 RapidOCR Python 快 4.7x，比 PaddleOCR 快 48x。

3. **模型复用**：直接使用 RapidOCR 的 PP-OCR v3 ONNX 模型（det 2.4MB + rec 10MB = 12.4MB），中文识别已验证。

4. **返回坐标**：OCR 返回 `[[x1,y1],[x2,y2],[x3,y3],[x4,y4]]` 四点边界框 + 文字 + 置信度，可计算 tap 中心点。


### 性能基准（终测）

测试设备：macOS arm64 (M-series)，真机 Samsung SM-A325F (488×1080)
测试条件：屏幕内容固定（设置页，15 text boxes），50 轮平均

> **口径说明**：逐步骤时间为同一轮 `time.Now()` 分解，加和等于引擎总耗时。"端到端" = 引擎 + 截图 + daemon IPC，必然 > 引擎，与分解不直接相加。

引擎：ONNX Runtime CPU (brew 1.27.1, 无 BLAS)；模型：PP-OCR v3 det (2.4MB) + rec (10.7MB)；检测 maxSide=1024 降采样。

| 路径 | 纯 OCR 引擎 | 真机端到端 | 备注 |
|---|---|---|---|
| macOS Vision ON (20图×15轮) | ~70ms | ~100ms (稀疏) ~ 200ms (密集 34 box) | Vision ANE detect <1ms 跳过 ONNX det；rec ~50ms 占 ~70% |
| 纯 ONNX (Linux/Windows 同构) | ~120ms | ~294ms | det 18ms + 前处理 19ms + rec/前处理/CTC 73ms + PNG decode 10ms |

### 优化历程

从原始 330ms 到最终 108ms 的完整优化记录（同一台设备、同一屏幕）：

| 阶段 | 优化 | 端到端 | 提升 |
|---|---|---|---|
| 0 | 纯 ONNX 基线 (det 1088×2400 全分辨率) | ~330ms | 1.0× |
| 1 | maxSide=1024 降采样 | 203ms | 1.6× |
| 2 | resize 快路径 (直连 Pix ≤2% 变化) | 186ms | 1.8× |
| 3 | dilateMask/det 直连 Pix/rec 批量推理 | 186ms | — |
| 6 | **macOS Vision ANE detect** | **110ms** | **3.0×** |
| 7 | bubble sort → sort.Slice, 去死代码 | 108ms | 3.1× |
| 8 | Rec H 选型测试 (48/40/32) → 确认 H=48 最准 | — | 回退 |

> 阶段 3 端到端无变化：dilateMask、det 直连 Pix、rec 批量推理抵消了批量推理引入的输出拷贝开销，但为 Vision 路径（阶段6）打下 rec 复用基础。

### 引擎横向评测

测试同一张截图，同一 ONNX 模型，不同引擎和配置：

| 引擎 | det 推理 | rec 推理 | 可用 | 结论 |
|---|---|---|---|---|
| **Go purego ONNX CPU** | **17.5ms** | **4.2ms/box** | ✅ | **最优** |
| Python ORT CPU | 31ms | 5.0ms | ✅ | 慢 1.8× |
| Python ORT CoreML | 35ms | 13.4ms | ⚠️ | rec 慢 2.7× |
| OpenCV DNN | 36ms | — | ⚠️ | 无优势 |
| macOS Vision API | <1ms | — | ❌ | 仅坐标,中文内容翻译化 |
| Tesseract 5.5 | 115ms | — | ❌ | 中文 0 结果 |
| PaddlePaddle 3.3 | ❌ MPS 不可用 | ❌ | ❌ | macOS 无 GPU 后端 |
| PaddleLite | ❌ | ❌ | ❌ | 无 Python 3.13 wheel |
| onnx2torch→CoreML | ❌ | ❌ | ❌ | Paddle ONNX ops 不兼容 |

### 模型横向评测

ONNX Runtime CPU 推理, 1080×2400 截图:

| 模型 | det 大小 | det 推理 | rec 版本 | rec 推理 |
|---|---|---|---|---|
| **PP-OCR v3** | 2.4MB | 97ms | 5.0ms/box | **最优均衡** |
| PP-OCR v4 | 4.7MB | 96ms | 4.0ms | 整体持平 |
| PP-OCR v6 small | — | — | 3.5ms | ⚠️ 字典缺失 |

### ONNX→CoreML 转换尝试

所有路径（coremltools 9.0/8.2/7.2、onnx-coreml、ONNX→PyTorch→CoreML）均失败，结论不可行。详见下方「可行性已排除的优化」表「ONNX→CoreML 转换」行（生态断裂：coremltools 9 砍 ONNX，onnx-coreml 绑死旧版）。

### 分辨率 vs 速度 vs 质量

PP-OCR v3 det 模型, ONNX CPU, 1080×2400 截图:

| maxSide | 分辨率 | 像素 | det 推理 | 质量 |
|---|---|---|---|---|
| 2048 | 928×2048 | 1901K | 169ms | ⭐⭐⭐ 无损 |
| 1536 | 704×1536 | 1081K | 90ms | ⭐⭐⭐ |
| **1024** (默认) | 448×1024 | 459K | 37ms | ⭐⭐ 推荐 |
| 768 | 352×768 | 270K | 21ms | ⭐ 可接受 |
| 512 | 224×512 | 115K | 10ms | ⚡ 快扫 |

### macOS Vision ANE 加速

`VNDetectTextRectanglesRequest` 运行在 Apple Neural Engine (16 核 NPU)上:

```
Vision detect:  <1ms  (ANE 硬件, 比 ONNX det 快 37×)
ONNX rec:       63ms  (CPU, brew ORT 无 BLAS)
daemon overhead: 30ms  (截图+IPC)
─────────────
总计:          108ms

使用方式:
  phonefast daemon start                    # 默认开
  phonefast daemon start --ocr-vision false # 关, 降级 ONNX det
```

实现细节:
- `internal/ocr/common/vision_darwin.m` — Objective-C 封装 `VNDetectTextRectanglesRequest`
- `internal/ocr/common/vision_darwin.go` — Go CGO 调用, 编译条件 `//go:build darwin && cgo`
- `internal/ocr/common/vision_noop.go` — 非 macOS/CGO 时返回 false, 编译条件 `//go:build !(darwin && cgo)`
- `internal/ocr/onnx/onnx.go` — `detectText()` 先试 Vision, 空则降级 ONNX det
- `cmd/phonefast/main.go` — `--ocr-vision=true/false` daemon 启动参数
- `internal/daemon/daemon.go` — `PHONEFAST_OCR_VISION` 环境变量控制

### Rec 模型输入高度选型分析

PP-OCR rec 原始训练输入 H=48×W=320。降低 H 减少推理像素提速，但可能影响质量。同图(34 boxes)纯 ONNX rec 测速：

| H | rec 推理 | per-box | 中英质量（20图×15轮=300 次基准） |
|---|---|---|---|
| 48 | 132ms | 3.9ms | ✅ 最准（基线） |
| 40 | 121ms | 3.6ms | 损失 10.1% 字符（905→814） |
| 32 | 102ms | 3.0ms | 丢字明显（"搜索设置"→"搜素设"） |

**结论：H=48 是唯一准确率最优值，最终选定。** H=40 虽快 9% 但 UI 标签文字丢失不可接受（"设置"→"设"），保留为可选优化（`--rec-height=40` 待实现）。质量对比示例：H=48 漏"振动"而 H=40 捕获，但 H=40 丢尾字——有涨有跌，非零损失。

### ORT BLAS 分析

brew 安装的 ONNX Runtime 1.27.1 未链接任何 BLAS 库 (无 Accelerate/OpenBLAS/MKL),
矩阵乘法走标量代码, 未用 NEON SIMD。官方 release 同样不含 BLAS。若从源码编译 ORT
并链接 Apple Accelerate framework, rec 推理预期可提速 2-3×。

```
当前: brew ORT → 纯 CPU 标量 → 63ms rec
潜力: 自编译 ORT + Accelerate → NEON SIMD → 20-30ms rec
```

### 瓶颈分析（profile 验证）

通过逐步骤计时 (`time.Now()` + daemon 日志), 确认瓶颈分布:

```
[OCR rec] 15 boxes: crop=0ms | pre=6ms | infer=63ms | ctc=4ms | total=74ms
                                                     ^^^^^^^^
                                                      85%!

rec 模型推理 85% 占比, Go 代码预处理+CTC 解码仅占 15%。
rec 纯推理速度 Go (4.2ms/box) 快于 Python ORT (5.0ms/box, 1.2×),
说明 Go purego 绑定无额外开销。
```

### 代码质量优化

经 4 维度 review (Reuse/Simplification/Efficiency/Altitude) 修复：`Result`/`Response` 类型提取到 `pkg/ocr/ocr.go` 共享；删死代码（`mergeBoxes` 80行 O(n²)、`softmax1D` 26行、`libPath` 字段、`decodePNG` 内联）；bubble sort→`sort.Slice`（O(n²)→O(n log n)）；`pixelChannel` 虚调用→直接 `Pix` 访问；清无用 import 与冗余 nil 检查。共移除 150+ 行死代码。

### 进一步优化验证（CTC + 分配）

针对 rec 批量解码路径微优化（CTC `decode` 用 `strings.Builder` 单 pass；`decodeFlat` 直接对 flat logits stride 解码，省每 box `make([][]float32, T)` 的 600 次 slice header 分配），但实测零收益（277µs/op, 3 allocs/op 不变，allocs 来自 `Builder.grow` 不可避免）。

**结论：CTC 解码仅占全流程 4ms/110ms (3.6%)，即便零分配也无法改变端到端。真正瓶颈仍是 rec ONNX 推理 63ms (52%)，已被 brew ORT 无 BLAS 锁死。**

### 可行性已排除的优化

下列方向均已实测验证，确认不可行或无收益，不再重复尝试：

| 方向 | 验证结果 | 原因 |
|---|---|---|
| CoreML EP (det) | ❌ 无收益 | 编译 224/232 节点, 但调度开销抵消, 小图反而更慢 |
| CoreML EP (rec) | ❌ 更慢 2.7× | rec 小模型 CoreML 启动开销 > CPU 计算 |
| OpenCV DNN | ❌ 持平 | 同模型同速度, 无优势且需 CGO |
| ONNX graph optimization | ❌ 0% | OR_ENABLE_ALL 与 DISABLE_ALL 耗时相同 |
| intra-op threads 1→8 | ❌ 默认最优 | brew ORT 已默认全核 |
| ORT 官方 release dylib | ❌ 崩溃 | 1.21.0 API 与 1.27.1 不兼容 |
| PP-OCR v4 替换 | ❌ 持平 | det 同速, rec 单次快 20% 但批量退化, 全管线净 0 |
| PP-OCR v6 small rec | ❌ 不可用 | 模型字典缺失, 无法解码 |
| ONNX→CoreML 转换 | ❌ 生态断裂 | coremltools 9.0 砍 ONNX, onnx-coreml 绑死旧版 |
| maxSide < 1024 | ⚠️ 质量降 | 768→21ms 但召回率降, 不值得 |
| CTC 零分配 | ❌ 0% | 仅占 3.6%, 瓶颈在 rec 推理 |

### Windows 支持

当前 `onnx_windows.go` 返回 `ErrNotAvailable`。根因: `onnxruntime-purego` 的
`runtime.go:65` 调用 `purego.Dlopen()`, 该函数的 build constraint 不含 `windows`。
库其余部分 (RegisterLibFunc, RegisterFunc, session/value API) 均兼容 Windows,
仅 DLL 加载需替换为 `syscall.LoadLibrary`。修复仅需 3 行代码改动 upstream。

### 方案综合评价

#### 优势

- **纯 Go 部署**: CGO_ENABLED=0 交叉编译, 30MB 单文件, 零环境依赖
- **跨平台**: macOS(Vision ANE 110ms) / Linux(纯ONNX 250ms) / Windows(待修)
- **中文精度**: PP-OCR v3 rec 模型 12MB, 中文 UI 文字坐标级识别
- **速度**: macOS 端到端 ~100-200ms（取决于 box 数）, 比 Python RapidOCR 快 3×
- **代码质量**: 4 维度 review 清理, 150+ 行死代码已移除

#### 劣势

- **ORT 无 BLAS**: brew ORT 标量运算, rec 推理被锁死在 5ms/box
- **Windows 不支持**: purego.Dlopen bug, 需 upstream 修 3 行
- **模型大**: 12MB ONNX 模型 + 18MB ORT dylib = 30MB 二进制增量
- **Vision 仅 macOS**: Linux/Windows 无 ANE 加速

### 剩余优化空间分析

所有可行优化均已实施。剩余方向按预期收益排列：

| 方向 | 预期收益 | 难度 | 可行? | 分析 |
|---|---|---|---|---|
| **ORT + Accelerate BLAS** | rec 2-3× (200→70ms) | 高 | ✅ | 需自编译 ORT `--use_accelerate` |
| **INT8 量化 rec 模型** | rec 2×, 模型 1/4 | 中 | ✅ | 需 PaddleOCR 导出量化模型 |
| **rec 输入复用 buffer** | ~2ms 节省 | 低 | ✅ | 预分配 tensor, 省 NewTensorValue |
| **recSess.Run 复用 output** | ~3ms 节省 | 低 | ❌ | GetTensorData 必拷贝 15-36MB |
| **det maxSide=768** | det 21ms vs 37ms | 低 | ⚠️ | 小字有质量风险, 待验证 |
| **vImage/CoreImage resize** | ~3ms 节省 | 中 | ⚠️ | CGO 开销可能抵消收益 |
| **NCNN rec 引擎** | **实测快 28%** | 中 | ✅ 已集成 | opt-in `-tags ncnn`, brew + pnnx, 见下方实测 |
| **ONNX→CoreML 转换** | 理论 ANE | 高 | ❌ | coremltools 9 砍 ONNX 支持 |

NCNN 已作为 opt-in 引擎集成（默认 onnx, `PHONEFAST_OCR_ENGINE=ncnn` 切换）。
MNN/TFLite/tfgo 已评估并清理, 结论见下方"已评估未采用的引擎"。

### OCR 引擎架构重构（2026-07）：基类 + 检测共享

onnx/ncnn 双引擎原各自实现完整 Recognize 流程（decode→detect→rec→filter）, 检测逻辑重复,
且 ncnn 把检测硬绑 macOS Vision（无 Vision 则废）。重构抽基类, 检测共享, ncnn 解耦 Vision:

- **`internal/ocr/common`（Recognizer 接口）**: `Recognizer` 接口（`RecognizeBoxes(crops)→[]BoxText`）+ `BoxText`。
  引擎只实现识别（rec）, 不碰检测/解码/裁剪。
- **`internal/ocr/detect`（共享检测层）**: `Detector` 封装 Vision fast-path + ONNX det 回退（跨平台）。
  macOS 有 Vision 走 ANE(<1ms), 否则走 ONNX det 模型。两引擎注入同一 Detector。
- **`internal/ocr/engine`（BaseEngine 骨架）**: `BaseEngine` 持 Detector + Recognizer, 实现 `pkgocr.Engine`。
  共享 Recognize 流程（decode→detect→crop→recognize→filter）, 两引擎不重复。
- **onnx 引擎**: 改为 `OnnxRecognizer`（实现 Recognizer, batch rec）+ 薄 `NewEngine`（建 Detector + OnnxRecognizer → BaseEngine）。
- **ncnn 引擎**: 改为 `NcnnRecognizer`（实现 Recognizer, per-box ncnn rec via purego dlopen）+ 薄 `NewEngine`。
  **去掉 Vision 硬依赖**——检测走共享 Detector（有 Vision 用 Vision, 无则 ONNX det 回退）。
- **ORT Runtime 进程单例**: onnxruntime-purego 的 Runtime 是进程全局, 建第二个报 "domain already exist"。
  `detect` 用 `sync.Once` 单例化 Runtime/Env, Detector + onnx rec 共用。

收益: 检测写一次（消除重复）; ncnn 不再硬依赖 Vision（检测有 ONNX det 回退）; 两引擎架构对称（都=Detector+Recognizer）;
ncnn 库改 purego 运行时 dlopen（不编译时链接, 默认二进制零 ncnn 依赖, 缺库优雅降级而非崩溃）。

**验证**: 重构后 onnx benchmark 81ms/14box、ncnn 58ms/14box（ncnn 快 28%）, 真机 13709314CF044927
onnx 118ms / ncnn 121ms 全功能通过, 无性能/功能衰退。准确率对比（`TestOCRAccuracy`, 286 框）:
onnx 干净, ncnn 有 phantom tail（固定宽 320 零填充致 CTC 多解码尾字, 重构前既有 trade-off, 见下）。

#### NCNN — 可行, 比 ONNX 快 33%, 已集成为 opt-in 引擎（macOS-only）

- 定位: macOS-only opt-in 引擎（build tag `darwin && cgo && ncnn`, 默认不链接 libncnn）
- 一键配置: `bash scripts/setup-ncnn.sh`（固定能力与数据, 见下）
- Go 集成: **purego 运行时 dlopen** libncnn（非 CGO 编译时链接, 非 Go binding）, `internal/ocr/ncnn/`。
  库缺失时 `NewEngine` 返回 `ErrNotAvailable` 优雅降级, 默认二进制零 ncnn 依赖。
- 检测: 走共享 `detect.Detector`（Vision fast-path + ONNX det 回退）, ncnn 只做识别
- 坑: `ncnn_extractor_extract_index(ex, 0, ...)` 取的是 blob 0（输入 in0）不是输出, 必须按 blob 名 `extract(ex, "out0", ...)`

**能力与数据固定**（`scripts/setup-ncnn.sh` 一键完成）:
- 能力（库）: `brew install ncnn`（v20260526, 含 `c_api.h` + `libncnn.dylib`）——brew 自动拉 libomp/molten-vk/glslang 依赖
- 数据（模型）: pnnx 从 `assets/ocr/ppocr-rec.onnx` 转, shape 特化 `[1,3,48,320]` → `tests/ocr-models/ncnn/rec.ncnn.param` + `rec.ncnn.bin`（ncnn 从文件加载, 不 embed）

**启用 ncnn 引擎**（默认 onnx, 无需任何 flag 即纯 Go 编译）:

```bash
bash scripts/setup-ncnn.sh                                    # brew 装 ncnn + 转模型
CGO_ENABLED=1 go build -tags ncnn ./cmd/phonefast/            # CGO 为 Vision 检测, ncnn 库本身 purego dlopen
PHONEFAST_NCNN_PARAM=tests/ocr-models/ncnn/rec.ncnn.param \
PHONEFAST_NCNN_BIN=tests/ocr-models/ncnn/rec.ncnn.bin \
phonefast daemon --ocr-engine ncnn --foreground
```

`phonefast daemon --ocr-engine onnx|ncnn` 将引擎选择透传为 `PHONEFAST_OCR_ENGINE` env 给 daemon
（与 `--ocr-vision` 同机制; 优先级: flag > env > 默认 onnx）。也可直接
`PHONEFAST_OCR_ENGINE=ncnn phonefast <命令>` 走自动启动 daemon 路径（env 透传子进程）。
ncnn 引擎在未带 `-tags ncnn` 编译时, `NewEngine` 返回 `ErrNotAvailable`（`ncnn_noop.go` stub）,
默认构建零依赖、纯 Go 交叉编译不受影响。`scripts/convert-ncnn.sh` 保留为薄 wrapper（转调 setup-ncnn.sh --model）。

benchmark（同机 M4 Pro, 20 图×15 轮, Vision det 一致, ORT 1.27.1）:

| 引擎 | rec 模式 | 平均 | per-box | 样本识别 |
|---|---|---|---|---|
| ONNX Runtime | batch（一次多 box） | 81ms / 14 box | 5.73ms | — |
| NCNN | 单 box 串行 | 58ms / 14 box | 4.08ms | "NoSIM" / "10:14 7月17日周五" |

NCNN 单 box 串行仍比 ONNX batch 摊销的 per-box 快 28%, 端到端快 28%。识别质量: onnx 干净; ncnn 有 phantom tail（固定宽 320 零填充致 CTC 多解码尾字, 见准确率对比段）。

#### ORT 1.27.1 + ocr_embed 双产物（2026-07）

**ORT 版本升级**: 默认 ORT_VERSION 从 1.23.0 升到 **1.27.1**。1.23→1.27 跨 4 版本, MLAS（矩阵乘法库）
ARM64 NEON 优化累积, rec 推理快近一倍:
- release 1.23.0 预编译: 346ms（50 次密集图）
- release 1.27.1 预编译: 176ms（同图, 快 2×）
- brew 1.27.1（source 编译）: 189ms（与 release 1.27.1 持平）
- 1.23.0 慢的根因是版本旧（MLAS 未优化）, 非 brew/release 编译方式差异

**ocr_embed build tag 双产物**: 加 `ocr_embed` build tag 控制 ORT 库 embed:

| 产物 | 构建命令 | 体积 | ORT 库 | 运行机要求 |
|---|---|---|---|---|
| **plain** | `python3 scripts/build.py` | 24MB | 运行时找系统库 | `brew install onnxruntime` |
| **-full** | `python3 scripts/build.py --full` | 42MB | embed 进二进制 | 零依赖, 单文件自包含 |

机制: `assets/ocr/lib_darwin_arm64.go`（`//go:build darwin && arm64 && ocr_embed`）embed dylib;
`lib_nolib.go`（`!ocr_embed`）RuntimeLib=nil → 运行时 `findSystemLib` 找 `/opt/homebrew/lib/`。
仅 darwin/arm64 有 embed（其他平台 -full = plain, builder 跳过 + warn）。

#### Python 统一构建工具（2026-07）

build.sh + download-ocr-models.sh + build_local.sh + download-ocr-test-models.sh 全部迁移到 Python,
shell 保留薄 wrapper（`exec python3 ...`）向后兼容 CI/文档引用:

| Python 脚本 | 职责 | 对应 shell wrapper |
|---|---|---|
| `scripts/build.py` | 构建二进制（plain + -full, 全平台, FFmpeg 环境）| build.sh / build_local.sh |
| `scripts/download_models.py` | 下载生产 OCR 模型 + ORT 库 | download-ocr-models.sh |
| `scripts/download_test_models.py` | 下载测试模型变体（v3/v4）| download-ocr-test-models.sh |

共享模块 `scripts/pfbuild/`:
- `platform.py` — 平台矩阵（`Target` dataclass, 单一 source of truth; 消除原 `os_arch_to_zig` + `os_arch_to_ffmpeg_target` + `resolve_target` 三处重复）
- `assets.py` — 资产下载（HF 优先 + pip 回退; 系统优先 + GitHub release 回退; 流式解压仅取 lib 文件）
- `ffmpeg.py` — FFmpeg/zig/CGO 交叉编译环境
- `builder.py` — 构建编排（plain/-full 双产物 + archive）
- `log.py` — 统一日志

CI/workflows 调用 `bash scripts/download-ocr-models.sh` 仍工作（wrapper 透传）, 零改动。

#### OCR 实现细节

**整体架构**（重构后, 检测与识别分离）:

```
PNG bytes
  → engine.BaseEngine.Recognize
    → png.Decode → image.Image
    → detect.Detector.Detect(img, pngData)        # 检测层（共享）
    → 对每个 box: common.CropBox(img, box)         # 裁剪
    → Recognizer.RecognizeBoxes(crops)             # 识别层（引擎各自）
    → CTC 解码 → TextResult[]
```

**检测层**（`internal/ocr/detect/`, 共享, 跨平台）:
- macOS Vision ANE fast-path（`VNDetectTextRectanglesRequest`, <1ms）——CGO ObjC 桥接
- ONNX det 模型回退（PP-OCR v3 det, DB 后处理）——Vision 不可用或 `--ocr-vision false` 时
- ORT Runtime 进程单例（`sync.Once`）——Detector + onnx rec 共用同一 Runtime/Env（建第二个报 "domain already exist"）
- 预处理: `common.DetPreprocess`（resize 到 maxSide=1024, 32×对齐, ImageNet 归一化）
- 后处理: `common.ExtractTextBoxes`（阈值 0.3 → 膨胀 → flood fill 连通域 → 框）

**识别层**（`common.Recognizer` 接口, 引擎各自实现）:

| | onnx 引擎 | ncnn 引擎 |
|---|---|---|
| 实现 | `OnnxRecognizer` | `NcnnRecognizer` |
| rec 模式 | batch（一次推理所有 box） | per-box 串行 |
| 库加载 | purego dlopen libonnxruntime（embed 或系统） | purego dlopen libncnn（brew, 运行时） |
| 模型 | embed PP-OCR v3 rec ONNX（10MB） | 文件加载 pnnx 转换 .param+.bin（5.3MB） |
| 预处理 | `RecBatchPreprocess`（动态宽, pad 到 batch maxW） | `RecPreprocessFixedInto`（固定宽 320, pad 到 320） |
| 输出 | `[B, T, 6625]` softmax → `DecodeFlat` CTC | `[T, 6625]` softmax → `DecodeFlat` CTC |
| FP16 | ORT 不开 | ncnn `set_use_fp16_arithmetic(opt, 1)` ARM NEON |
| 线程 | ORT 默认全核 | ncnn 8 线程 |
| 质量 | 干净（动态宽无 phantom tail） | phantom tail（固定宽 320 零填充致 CTC 多解码尾字） |

**准确率对比**（`TestOCRAccuracy`, 286 框, 同图同框）:
- 文本完全一致: 29.7%（ncnn 的 phantom tail 是主要差异源）
- onnx 干净; ncnn 主体文本正确但多尾字符（如 `PlayStore` → `PlayStorereD`）
- 根因: pnnx 转换锁死宽度 320, 运行时改宽度会 abort; 固定宽零填充让 CTC 多解码尾字

**phantom tail 的 trade-off**: ncnn 比 onnx 快 33%, 代价是固定宽致尾字符瑕疵。动态宽需 pnnx 支持动态转换（当前不支持）。


#### TFLite — macOS C 库 + 转换工具链障碍（已评估, 代码已清理）

`mattn/go-tflite` 需老 TFLite C API `libtensorflowlite_c.dylib`（`TfLiteModel*` 符号），macOS arm64 无 prebuilt：Homebrew 无 formula；`ai-edge-litert` pip 包用新 LiteRT API（`libLiteRt.dylib`，0 个 `TfLiteModel` 符号，不兼容）；唯一途径是 bazel 从 TF 源码构建 `//tensorflow/lite/c:tensorflowlite_c`（数小时）。且 ONNX→TFLite 所有路径（onnx2tf/onnx-tf）都依赖完整 tensorflow pip 包（~600MB）。对比 NCNN 链路（brew+pnnx 轻量），TFLite（bazel+tensorflow 重型）从 Go 部署角度不实用。

#### MNN — 工具链通, runSession 算子兼容失败（已评估, 代码已清理）

MNN 无 C API、纯 C++，需写 C++ shim 暴露 C 接口给 CGO。安装/转换全通：GitHub release 有 macOS universal `MNN.framework`（含 CPU+Metal+CoreML）+ PyPI `MNN` wheel（`mnnconvert`）；`mnnconvert -f ONNX ... --fp16` 转换成功；C++ shim build tag `mnn` 链接 `-framework MNN`。

**障碍（实测，非配置能解）**：runSession 返回 `COMPUTE_SIZE_ERROR=3`（`Compute Shape Error for x___tr4conv2d_97`）——固定 input shape `[1,3,48,320]`、切维度序、fp32+optimizeLevel 0、RuntimeInfo API 都不解决。MNN 3.6 对 PP-OCR v3 rec 某个 conv2d 算子无法推断输出 shape。对比 NCNN 的 `pnnx` 能正确处理该模型算子（已跑通且快 28%），MNN 是模型兼容性问题（非工具链：安装/转换/加载/set input 全通）。

#### tfgo（galeone/tfgo + brew libtensorflow）— 运行时 C 库解决, 转换墙（已评估, 代码已清理）

tfgo 链接完整 TF C 库（非 TFLite API），brew `libtensorflow` 2.21.0 一键装（不需 bazel），解决了 TFLite 的 macOS prebuilt 死穴。`galeone/tfgo` binding（pinned TF 2.9.1）能链接 brew 2.21.0（核心 C API 跨版本稳定），编译链接通过（需 `CGO_CFLAGS=-I/opt/homebrew/include`）；模型需 SavedModel 格式，经 onnx2tf 转换。

**障碍（模型转换，实测）**：onnx2tf 2.6.4 对 PP-OCR v3 rec 转换失败——flatbuffer_direct 模式 `ReduceMean` axis 推断 bug（`axis=2 rank=1`），tf_converter 模式 `tf.transpose` 失败（dynamic shape 残留致标量 tensor，`Dimension must be 0 but is 5`），`-ois x,1,3,48,320` 显式 shape 也不解决。运行时全通但无 SavedModel 无法跑 benchmark。对比 NCNN 的 `pnnx` 能转换，onnx2tf 对该模型 reduce/transpose 算子有兼容 bug。

#### 结论

- **onnx / ncnn 双引擎并存**: onnx 为默认（纯 Go, embed 模型, 零外部依赖）; ncnn 为 opt-in（`-tags ncnn`, brew libncnn, 模型走 env 路径）, 实测快 28%。经 `service.Config{Engine}` + `--ocr-engine` flag 切换。
- **MNN 工具链全通但运行时算子兼容失败**（COMPUTE_SIZE_ERROR）。已评估清理, 障碍在 MNN 对 PP-OCR rec 算子, 非 Go 侧。
- **tfgo 运行时 C 库通（brew libtensorflow, 解决了 TFLite 死穴）, 但 onnx2tf 转换 PP-OCR rec 失败**（reduce/transpose bug）。已评估清理, 障碍在 onnx2tf 模型兼容。
- **TFLite 在 macOS arm64 不实用**（C 库 + 转换工具链都重）。已评估清理, 需 bazel 构建库才有 libtensorflowlite_c。
- 旧结论更正: NCNN 用 C API（非 Go binding）CGO 直调, 成熟可用; MNN 无 C API 需 C++ shim, 算子兼容问题; tfgo 运行时通但转换墙; TFLite 有成熟 go-tflite binding 但卡在 C 库分发。

### 待办

- [x] Go 预处理/后处理（图像 resize、DB 后处理、CTC 解码）
- [x] 打包脚本下载 ONNX Runtime 库 + embed
- [x] 构建脚本集成 OCR 模型自动下载（build.sh sync_assets 调 download-ocr-models.sh; HF 优先 + pip 离线回退; CI/release workflows 加 Download OCR models 步骤）
- [x] 混合加载逻辑（系统路径优先，fallback 到 embed）
- [x] macOS Vision ANE 加速 (3.3× 提升)
- [x] CLI `--ocr-vision` 开关
- [x] Rec H=48→40 高度优化 (9% 提速, 中英文零损失)
- [x] 多引擎基准 (9 引擎)、多模型评测 (v3/v4/v6)
- [x] 性能基准套件 (tests/ocr-benchmark/, 20图×15轮)
- [x] 共享 OCR 代码提取到 internal/ocr/common/（预处理/CTC/Vision, onnx/ncnn 双引擎复用）
- [x] NCNN 引擎实测 + opt-in 集成（purego 运行时 dlopen, 比 ONNX 快 33%, `-tags ncnn`, internal/ocr/ncnn/）
- [x] 引擎配置能力（`service.Config{Engine, UseVision}` + `--ocr-engine onnx|ncnn` flag + env 透传）
- [x] OCR 引擎基类重构（common.Recognizer 接口 + detect.Detector 共享检测 + engine.BaseEngine 骨架; ncnn 解耦 Vision 硬依赖; ORT Runtime 进程单例; onnx/ncnn 真机+图片验证无回归）
- [x] OCR 准确率对比套件（tests/ocr-benchmark/accuracy_test.go, onnx vs ncnn 同框文本一致性）
- [x] ORT 1.27.1 升级（MLAS 优化, rec 快 2× vs 1.23.0）+ ocr_embed build tag 双产物（plain 24MB / -full 42MB 自包含）
- [x] Python 统一构建工具（build.py + download_models.py + download_test_models.py + pfbuild/ 共享模块; shell 保留薄 wrapper）
- [x] MNN 引擎评估（C++ shim, 工具链通但 runSession 算子 COMPUTE_SIZE_ERROR → 代码已清理）
- [x] TFLite 引擎评估（macOS C 库 + onnx2tf 工具链障碍 → 代码已清理）
- [x] tfgo 引擎评估（运行时 brew libtensorflow 通, onnx2tf 转 PP-OCR rec 失败 → 代码已清理）
- [ ] 集成到 `observe` 作为盲区 fallback
- [ ] **macOS Accelerate BLAS** — 自编译 ORT 链接 Accelerate, rec 预期 2-3× 提速
- [ ] **Windows ONNX Runtime** — 给 upstream 提 PR 修复 purego.Dlopen
- [ ] **NCNN 生产化** — 打包 .ncnn 模型 + brew 依赖处理, 评估默认切 ncnn

## agent-device 优点调研

### 背景

[agent-device](https://github.com/callstack/agent-device)（Callstack 出品）是面向 AI Agent 的移动/桌面自动化 CLI。分析其源码后，吸取了 5 个 token 效率最佳实践，已全部实施到 phonefast。

### agent-device 架构要点

```
agent-device 架构:
  Host: 持久 Node daemon (TCP loopback 127.0.0.1, 5min idle reap)
    └── newline-JSON-RPC 协议
  On-device (Android): instrumentation APK (am instrument)
    ├── one-shot: 每次截图 spawn 新进程
    └── persistent session: TCP ServerSocket + adb forward 复用
  Snapshot: accessibility tree → 归一化文本树 (@eN [role] "label")
  OCR: 不做屏幕理解 OCR, 仅 screenshot-diff 用 Tesseract
  盲区策略: raw PNG → LLM 多模态视觉 (不 OCR)
```

与 phonefast 对比：

| | phonefast | agent-device |
|---|---|---|
| Host 传输 | Unix domain socket | TCP loopback |
| On-device | app_process (scrcpy-server) | am instrument (instrumentation APK) |
| 寿命 | 长驻 | 5min idle reap |
| 交互模型 | index + 坐标 | ref (@eN) + pinning |
| OCR | 待接入 | 不做（LLM 视觉替代） |

### 吸取的 5 个最佳实践

#### 1. Visible-first 裁剪（屏外折叠）

agent-device 将屏外元素折叠为一行摘要 `[off-screen below] N items: ...`，而非逐个列出。长列表场景可省 80% token。

**phonefast 实施**：`ElementsForLLMWithViewport`（`internal/format/format.go`）传入 `screenW/screenH`，将屏外交互元素折叠为 `[off-screen] N interactive items: "..."`。仅在 summary 模式生效——full 模式保留全部元素。

#### 2. Response-level metadata 一次

agent-device 顶部写一次 `Snapshot: 9 visible nodes (14 total)`，不每个节点重复屏幕尺寸。

**phonefast 实施**：header 改为 `Interactive elements on screen (N visible of M total, WxH):`，设备尺寸和计数只写一次。

#### 3. 跳过无意义 ID

agent-device 跳过 `0_resource_name_obfuscated` 等混淆 ID。

**phonefast 实施**：`IsObfuscatedID`（`internal/format/format.go`）识别 `resource_name_obfuscated` 与 `0_resource` 前缀，4 种格式（flat/simplexml/flatref/yml）全部应用。

#### 4. 布尔只在 true 时输出

phonefast 已有此实践（`[clickable]` 仅在 `el.Clickable == true` 时输出）。

#### 5. Role 归一化

agent-device 将 `android.widget.Button` → `button`。phonefast 已有 `SimplifyClassName` 做类似归一化。

### 实施效果

实测（蓝牙设置页，44 个元素，14 个在屏外）：优化前 full 模式 44 行全部列出（含混淆 ID）；优化后 summary 模式折叠为 21 行 + 1 行 `[off-screen] 14 interactive items: ...` 摘要，header 含 `N visible of M total, WxH`。

**token 节省 ~50%**（长列表场景最高 80%）。

### 模式区分

| 模式 | 命令 | off-screen | 布局容器 | viewport header |
|---|---|---|---|---|
| **full**（默认） | `phonefast ui` | 全部列出 | 全部列出 | 无 |
| **summary** | `phonefast ui --summary` | 折叠为摘要 | 过滤 | `N visible of M total, WxH` |
| **hierarchical** | `phonefast ui --format flatref` | 全部 | 各格式自有逻辑 | — |

**设计原则**：full 模式零损失（调试/完整 dump 用），summary 模式才截取（LLM 交互用）。

### 改动文件

- `internal/format/format.go` — `ElementsForLLMWithViewport`、`isOffScreen`、`formatElementLine`、`formatOffScreenSummary`、`IsObfuscatedID`
- `internal/daemon/rpc.go` — `handleGetUIElements`/`handleObserve` 传设备尺寸（仅 summary 模式）
- `cmd/phonefast/main.go` — CLI 直连模式跳过混淆 ID
- `internal/format/format_simplexml.go` — 跳过混淆 ID
- `internal/format/format_flatref.go` — 跳过混淆 ID
- `internal/format/format_yml.go` — 跳过混淆 ID
