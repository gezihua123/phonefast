# phonefast 构建手册

> 本文档说明 phonefast 每一种构建版本的命令、产物与依赖。
> 构建踩坑/体积分析/FFmpeg 链路排查等内部细节见 [docs/DEV.md](DEV.md)。

phonefast 有**三类构建产物**，各自独立：

| 产物 | 构建脚本 | 用途 |
|---|---|---|
| Go 二进制 `phonefast-*` | `scripts/build.sh`（→ `build.py`） | 主程序（daemon / CLI / MCP serve） |
| scrcpy-server.jar | `scripts/build-server.sh` | 推送到 Android 设备的 server（UISocket 补丁版） |
| OCR 引擎（onnx/tesseract/apple/ncnn） | `ocr/scripts/build.sh` | OCR 能力变体（通常内嵌进主程序，不单独产出） |

---

## 一、Go 主程序构建

入口：`scripts/build.sh`（薄封装）→ `scripts/build.py`（实际逻辑）→ `scripts/pfbuild/builder.py`。变体定义在 `scripts/pfbuild/variants.py`，脚本内部机制（变体表结构、调用链、cgo1 不降级原理、产物命名取舍）见 [docs/DEV.md → OCR 变体构建系统](DEV.md#ocr-变体构建系统数据驱动变体表)。

### 1.1 四种变体

所有变体通过 `--variant` 统一入口选择，定义在 `scripts/pfbuild/variants.py`（单一真相来源，加新变体只改这一张表）：

| 变体 | 命令 | build tag | CGO | Apple Vision | ORT 运行时 | 产物名 | 适用 |
|---|---|---|---|:---:|---|---|---|
| **plain**（默认） | `bash scripts/build.sh` | 无 | 1 | ✓（macOS） | 系统库 `libonnxruntime` | `phonefast-<os>-<arch>` | 已装 onnxruntime 的环境 |
| **cgo1** | `bash scripts/build.sh --variant cgo1` | 无 | 1（**不降级**） | ✓ | 系统库 `libonnxruntime` | `phonefast-cgo1` | macOS 全功能（见 §3.2） |
| **apple** | `bash scripts/build.sh --variant apple` | `NO_OCR_MODELS` | 1 | ✓ | 无（不嵌 ORT） | `phonefast-apple` | macOS 仅 Apple Vision |
| **full** | `bash scripts/build.sh --variant full` | `ocr_embed` | 1 | ✓（macOS） | 内嵌 ORT 库（零依赖） | `phonefast-<os>-<arch>-full` | 无 onnxruntime 的环境 |

> `--full` / `--full-only` 是 `--variant full` 的旧别名（等价），保留向后兼容。一次调用只构建一个变体；要多变体就多次运行。

**纯 Go 降级路径**（非命名变体，逃逸口）：`CGO_ENABLED=0 go build ./cmd/phonefast/` 产出无 astiav、无 Apple Vision 的纯 Go 二进制（~21MB），用系统 `ffmpeg` CLI 降级解码。等价于 `CGO_ENABLED=0 bash scripts/build.sh --variant plain`。

**说明**：
- plain / cgo1 / full 都内嵌 PP-OCR v3 模型（`ocr/assets/ppocr-det.onnx` + `ppocr-rec.onnx`，由 `embed_default.go` 的 `!NO_OCR_MODELS` tag 自动嵌入）。apple 变体用 `NO_OCR_MODELS` 不内嵌模型。
- cgo1 / apple 是 `macos_only` 变体：非 macOS 平台自动 skip（log 提示，不报错），产物名不带 `<os>-<arch>` 前缀。
- full 额外内嵌 ONNX Runtime 共享库。**仅 darwin/arm64 实际可用**：`platform.py` 的 `embeddable` 判定只认 `darwin/arm64`，其它目标直接 skip full（避免产出与 plain 字节相同的废二进制）。
- full 需要 ORT 库文件存在于 `ocr/assets/<embed_name>`，缺失时警告并提示用 `bash ocr/scripts/download.sh <goos> <goarch>` 下载。

### 1.2 平台选择

```bash
bash scripts/build.sh                  # 当前平台（自动探测 GOOS/GOARCH）
bash scripts/build.sh --macos          # 仅 macOS
bash scripts/build.sh --linux          # 仅 Linux
bash scripts/build.sh --windows        # 仅 Windows
bash scripts/build.sh --all            # 全平台
```

- 当前平台用原生工具链；交叉编译用 **zig cc**（`builder.py` 调 `ffmpeg.setup_cross_cgo` 注入 `CC/CXX=zig cc`）。
- `--all` 构建所有 `default_release=True` 的目标（darwin/arm64、linux/amd64、linux/arm64、windows/amd64），**排除 darwin/amd64**（Intel Mac，`platform.py` 标记 `default_release=False`）。CLAUDE.md 的 Step 1 纯 Go 交叉验证仍包含 darwin/amd64（只验证语法，不产出 release）。
- `--all` 等价于 `scripts/build_local.sh`（后者是 `build.py --all --ensure-ffmpeg` 的封装，自动检测环境 + 编 FFmpeg + 编二进制 + 打 `.tar.gz`）。

### 1.3 CGO 降级机制

`builder.py` → `ffmpeg.setup_cross_cgo` 在以下情况自动降级 `CGO_ENABLED=0`（会打印警告）：

| 触发条件 | 结果 |
|---|---|
| `CGO_ENABLED=0` 已设 | 保持，跳过 astiav CGO |
| 交叉编译且 zig 未安装 | 降级 `CGO_ENABLED=0` |
| FFmpeg 静态库缺失（`build/cross-ffmpeg/<target>/`） | 降级 `CGO_ENABLED=0` |

降级后：H.264 截图解码从 astiav CGO 进程内解码 → ffmpeg CLI subprocess 降级路径（`internal/session/video.go`，需系统有 `ffmpeg`）。OCR 的 onnxruntime 不受影响（走 purego dlopen，不依赖 CGO）。

**避免降级**：先编译静态 FFmpeg 库：

```bash
bash scripts/cross-build-ffmpeg.sh aarch64-darwin    # 当前平台
bash scripts/build_env.sh install                     # 自动装 nasm/zig/go
# 或一键：bash scripts/build_local.sh（= build.py --all --ensure-ffmpeg）
```

### 1.4 版本号与产物

- 版本号：`--version` 显式 > `$VERSION` 环境变量 > `git describe --tags` > `dev`。
- 产物目录：`dist/dev/`。命名按变体：plain/full 带 `<os>-<arch>`（`phonefast-<os>-<arch>[-full][.exe]`），cgo1/apple 不带（`phonefast-cgo1` / `phonefast-apple`，因 `macos_only` 单平台不冲突）。
- `--all` / `--macos` / `--linux` / `--windows` 会额外打 tar.gz：plain/full 为 `phonefast-<version>-<os>-<arch>[-full].tar.gz`，cgo1/apple 为 `phonefast-<version>-cgo1.tar.gz` 等。
- LDFLAGS 注入 `main.Version` / `main.BuildTime` / `main.GitCommit`。
- jar（`assets/scrcpy-server.jar`）在构建前由 `assets.sync_all` 自动同步，**单文件部署**（jar 已内嵌）。

---

## 二、scrcpy-server.jar 构建

入口：`scripts/build-server.sh`。基于 scrcpy v3.3.4 + phonefast 的 UISocketHandler 补丁。

### 2.1 前置依赖

- `ANDROID_HOME` 指向 Android SDK（需 platform android-30+，脚本会尝试自动装 android-36）
- Java 17+
- Git

### 2.2 构建命令

```bash
bash scripts/build-server.sh                    # 全新克隆 scrcpy v3.3.4 再构建
bash scripts/build-server.sh /path/to/scrcpy    # 用已有的 scrcpy 克隆
bash scripts/build-server.sh --skip-build       # 仅打补丁不构建
```

### 2.3 流程

1. 克隆 scrcpy `v3.3.4` → `.build-tmp/scrcpy`（或用传入路径）
2. 应用 `android/patches/0001-phonefast-uisocket.patch`
3. 用 `android/phonefast-agent/UISocketHandler.java` 覆盖（保持最新协议修复）
4. `./gradlew :server:assembleRelease` 编译
5. 产物 `server-release-unsigned.apk` 拷为 jar，同步到三处

### 2.4 产物与同步

| 路径 | 用途 |
|---|---|
| `android/scrcpy-server.jar` | 主副本（`install.sh` / `deploy` 读取） |
| `assets/scrcpy-server.jar` | Go embed 嵌入主二进制（单文件分发） |
| `android/scrcpy-server.version` + `assets/scrcpy-server.version` | 版本 sidecar（当前 `3.3.4`） |
| `dist/dev/scrcpy-server.version` | 若 `dist/dev/` 存在则同步 |

> **注意**：`android/patches/` 是基线 patch，最新代码在 `android/phonefast-agent/`。构建脚本自动覆盖，无需手动同步。

---

## 三、OCR 引擎变体

入口：`scripts/build.sh --variant <name>`（统一入口，定义见 `scripts/pfbuild/variants.py`）。**通常不需要单独执行**——主程序构建默认已内嵌 onnx 引擎。此节用于单独产出不同 OCR 配置的二进制。

> `ocr/scripts/build.sh <name>` 仍可用，是 `build.py --variant <name>` 的薄封装（保留旧调用习惯）。`default` 映射到 `CGO_ENABLED=0 --variant plain`（纯 Go，无 Apple Vision），与旧脚本行为一致。

### 3.1 四个变体

| 变体 | 命令（统一入口） | 旧别名（等价） | CGO | build tag | 引擎 | 适用 |
|---|---|---|---|---|---|---|
| **plain** | `bash scripts/build.sh --variant plain` | — | 1 | 无 | ONNX + Tesseract +（Apple Vision on macOS） | 标准构建 |
| **cgo1** | `bash scripts/build.sh --variant cgo1` | `ocr/scripts/build.sh cgo1` | 1（不降级） | 无 | ONNX + Tesseract + Apple Vision | macOS 全功能 |
| **apple** | `bash scripts/build.sh --variant apple` | `ocr/scripts/build.sh apple` | 1 | `NO_OCR_MODELS` | 仅 Apple Vision | macOS 无 ONNX 依赖 |
| **full** | `bash scripts/build.sh --variant full` | `ocr/scripts/build.sh full` / `--full` | 1 | `ocr_embed` | 全引擎 + 内嵌 ORT 库 | 自包含单文件 |

> 无 `all`：build.py 是"单次调用一个变体"模型。要批量编多变体用 shell 循环：`for v in plain cgo1 apple full; do bash scripts/build.sh --variant $v; done`

### 3.2 构建 Apple Vision 版本（cgo1）

CGO=1 时 macOS 上 `apple` 引擎的 build tag（`darwin && cgo`）自动激活，编入 Apple Vision 全功能 OCR（ANE 加速）。用 **cgo1 变体**：

```bash
bash scripts/build.sh --variant cgo1    # 产物 dist/dev/phonefast-cgo1（~26M）
# 等价：bash ocr/scripts/build.sh cgo1
```

**cgo1 不降级（关键差异）**：
- plain 变体在 `build/cross-ffmpeg` 缺失时会**降级 `CGO_ENABLED=0`**（见 §1.3），降级后 `darwin && cgo` tag 失效，apple 引擎静默丢失。
- cgo1 / apple 变体 `macos_only=True`，`builder.py` 传 `force_cgo=True` 给 `ffmpeg.setup_cross_cgo`：FFmpeg 缺失时**警告但不降级**，保持 CGO=1 用系统 FFmpeg（pkg-config）链接 astiav——这正是用户在 plain 上遇到的坑，cgo1 规避它。
- 显式 `CGO_ENABLED=0` 环境变量仍被尊重（用户明确要关 CGO 时服从）。

**cgo1 构建前置**：系统需有 FFmpeg 开发库（`brew install ffmpeg`，提供 pkg-config `libavcodec` 等）供 astiav CGO 链接。注意系统 FFmpeg 8.0.1 移除了 `AVFMT_FLAG_SHORTEST`，但 go-astiav v0.40.0 已不引用该宏，实测可链接。生产构建建议用自编译 FFmpeg（`bash scripts/cross-build-ffmpeg.sh aarch64-darwin`）保证 ABI 与构建一致，此时 cgo1 与 plain 行为一致（都不降级）。

**运行时选 Apple Vision 引擎**：

```bash
phonefast-cgo1 ocr --ocr-engine apple        # 命令行参数
PHONEFAST_OCR_ENGINE=apple phonefast-cgo1 …  # 环境变量（默认 onnx）
```

> 验证 apple 引擎是否编入：`go tool nm dist/dev/phonefast-cgo1 | grep phonefast/ocr/apple`，应见 `EngineApple.Recognize` 等符号。

### 3.3 引擎启用条件

| 引擎 | 依赖 | 启用方式 |
|---|---|---|
| **onnx**（默认） | `libonnxruntime`（系统或内嵌）+ PP-OCR 模型 | 默认（`blank import ocr/onnx`） |
| **tesseract** | 系统 `tesseract` CLI | 默认 |
| **apple** | macOS Vision 框架（CGO） | `cgo1` / `apple` / `full` 变体 |
| **ncnn** | `libncnn`（`brew install`）+ ncnn 模型 | `-tags ncnn`（opt-in，28% 更快） |

### 3.4 build tag 语义（`ocr/assets/*.go`）

| 文件 | tag | 作用 |
|---|---|---|
| `embed_default.go` | `!NO_OCR_MODELS` | 内嵌 `ppocr-det.onnx` + `ppocr-rec.onnx`（默认） |
| `embed_no_models.go` | `NO_OCR_MODELS` | 模型不内嵌（`apple` 变体用） |
| `lib_darwin_arm64.go` | `darwin && arm64 && ocr_embed` | 内嵌 `libonnxruntime-darwin-arm64.dylib` |
| `lib_linux_amd64.go` | `linux && amd64 && ocr_embed` | 内嵌 `libonnxruntime-linux-amd64.so` |
| `lib_nolib.go` | `!ocr_embed` | ORT 库不内嵌，运行时加载系统库 |

> onnxruntime-purego（官方 v23-only）通过 ORT 的 `GetApi(23)` 版本协商工作于新版 libonnxruntime（如 1.27.1）——v23 binding 调用的老函数指针在新库中仍可用，无需 v27 adapter。

---

## 四、测试构建

入口：`scripts/test.sh`（wrapper）。

```bash
bash scripts/test.sh                    # 全仓库测试
bash scripts/test.sh ./pkg/h264/ -race  # 指定包 + 参数转发
bash scripts/test.sh --no-cgo          # 强制 CGO_ENABLED=0
```

**两条路径**：
- 自编译 FFmpeg 存在（`build/cross-ffmpeg/<host>/lib/pkgconfig`）→ `CGO_ENABLED=1` + `PKG_CONFIG_PATH` 指向自编译 FFmpeg 8.0（与生产构建同源）。
- 不存在 → 降级 `CGO_ENABLED=0` + `AVCODEC_SKIP_TEST=1`（跳过 avcodec CGO 测试）。

> 裸 `go test ./...` 会用系统 FFmpeg，其 ABI 可能与构建链接版本不同（如移除 `AVFMT_FLAG_SHORTEST`），故必须用 wrapper。

---

## 五、环境准备

`scripts/build_env.sh` 是统一的环境检测/安装入口：

```bash
bash scripts/build_env.sh            # 报告当前环境
bash scripts/build_env.sh check      # 检测，缺依赖返回非 0
bash scripts/build_env.sh install    # brew 自动装 nasm/zig/go
```

**全平台依赖清单**：

| 依赖 | 用途 | 安装 |
|---|---|---|
| Go 1.21+ | 编译主程序 | `brew install go` |
| FFmpeg 静态库 | astiav CGO 解码 | `bash scripts/cross-build-ffmpeg.sh <target>` 或 `--ensure-ffmpeg` |
| zig | 交叉编译 CGO | `brew install zig` |
| nasm | x86_64 FFmpeg asm（SSE/AVX） | `brew install nasm` |
| libonnxruntime | OCR onnx 引擎（plain 变体） | `brew install onnxruntime` |
| Android SDK + Java 17+ | 构建 server jar | 见 `build-server.sh` |

---

## 六、快速参考

```bash
# 最常用：当前平台标准构建（plain）
bash scripts/build.sh

# 当前平台 + Apple Vision（macOS，不降级 CGO）
bash scripts/build.sh --variant cgo1

# 当前平台 + 自包含 -full（内嵌 ORT 库）
bash scripts/build.sh --variant full      # 旧写法 --full 等价

# 全平台交叉编译（自动编 FFmpeg）
bash scripts/build_local.sh

# 重建 server jar（改了 UISocketHandler 后）
bash scripts/build-server.sh

# 全仓库测试（与生产构建同源 FFmpeg）
bash scripts/test.sh

# CI 发版（推 tag 触发 GitHub Actions 全平台原生构建）
git tag v1.0.x && git push origin v1.0.x
```

构建验证三步曲（合并前必跑）见 [CLAUDE.md → 构建验证](../CLAUDE.md#构建验证)。
