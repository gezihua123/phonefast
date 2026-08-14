# OCR 开发笔记

> 从 phonefast docs/DEV.md 提取的 OCR 相关开发记录

## OCR 识别方案调研与选型

### 候选方案实测对比

在同一台设备（macOS arm64）、同一张截图（720×1600）上测试：

| 方案 | 推理速度 | 中文准确率 | 模型体积 | Go 集成方式 |
|---|---|---|---|---|
| PaddleOCR v6 (Python) | 3500ms | ✅ 最好 (conf 0.93) | ~200MB | 子进程 Python |
| RapidOCR (ONNX, Python) | 330ms | ✅ 好 (conf 0.81) | ~13MB | 子进程 Python |
| Tesseract (C, 子进程) | 142ms | ❌ 白字渐变失败 | ~40MB | 子进程 / CGO |
| Go CGO + ONNX Runtime | 37.5ms (det) | ✅ 同 RapidOCR | ~13MB | CGO |
| Go purego + ONNX Runtime | 35.7ms (det) | ✅ 同 RapidOCR | ~13MB | 纯 Go (CGO_ENABLED=0) |

### 选定方案：Go purego + ONNX Runtime + PP-OCR v3

### 性能基准

测试设备：macOS arm64 (M-series)，真机 Samsung SM-A325F (488×1080)
测试条件：屏幕内容固定（设置页，15 text boxes），50 轮平均

| 路径 | 纯 OCR 引擎 | 真机端到端 |
|---|---|---|
| macOS Vision ON | ~70ms | ~100ms (稀疏) ~ 200ms (密集 34 box) |
| 纯 ONNX | ~120ms | ~294ms |

### 优化历程

| 阶段 | 优化 | 端到端 | 提升 |
|---|---|---|---|
| 0 | 纯 ONNX 基线 (det 1088×2400 全分辨率) | ~330ms | 1.0× |
| 1 | maxSide=1024 降采样 | 203ms | 1.6× |
| 2 | resize 快路径 | 186ms | 1.8× |
| 6 | macOS Vision ANE detect | 110ms | 3.0× |
| 7 | 去死代码 | 108ms | 3.1× |

### 引擎横向评测

| 引擎 | det 推理 | rec 推理 | 结论 |
|---|---|---|---|
| Go purego ONNX CPU | 17.5ms | 4.2ms/box | 最优 |
| Python ORT CPU | 31ms | 5.0ms | 慢 1.8× |
| macOS Vision API | <1ms | — | 仅坐标,中文内容翻译化 |
| Tesseract 5.5 | 115ms | — | 中文 0 结果 |

### macOS Vision ANE 加速

VNDetectTextRectanglesRequest 运行在 Apple Neural Engine (16 核 NPU)上:

- Vision detect: <1ms (ANE 硬件, 比 ONNX det 快 37×)
- ONNX rec: 63ms (CPU)

---

## ONNX Runtime 动态库版本对比

### 两种 dylib 来源

phonefast OCR 引擎可通过两种方式获取 ONNX Runtime 动态库：

| | brew 安装版 | GitHub Release 版 |
|---|---|---|
| **安装方式** | `brew install onnxruntime` | `bash ocr/scripts/download.sh lib`（从 GitHub 下载） |
| **大小** | 18MB | 37MB |
| **链接方式** | 动态链接 → 依赖 brew 包 (abseil, protobuf, onnx, re2 等 10+ 个) | 静态链接 → 仅依赖 macOS 系统框架 (CoreML, Foundation, Metal, IOKit) |
| **适用场景** | 本地开发（已装 brew 的环境） | **嵌入发布**（`ocr_embed` build tag，零外部依赖） |
| **CoreML/Metal** | ✅ 支持 | ✅ 支持 |
| **API 版本** | v1.27.1 (API v23) | v1.27.1 (API v23) |

### API v23 兼容性说明

`onnxruntime-purego` (上游) 绑定 ONNX Runtime C API v23。ORT 的 `GetApi(23)` 版本协商机制确保 v23 绑定可无缝兼容更高版本的 libonnxruntime（如 1.27.1），返回的 `OrtApi` 结构体保持 v23 偏移量的原有布局和签名。

### 实际 A/B 测试对比

测试环境：macOS arm64 (Apple M4 Pro)，同一套 PP-OCR v3 模型 (det + rec)，20 张 benchmark 截图。

#### 准确率（完全一致）

运行 `TestOCRBenchmarkAccuracy`：

| 指标 | brew 版 | GitHub 版 |
|---|---|---|
| 总体准确率 | **97%** (86/89) | **97%** (86/89) |
| TestOCRSmoke | 20 text boxes | 20 text boxes（文本、置信度完全一致） |
| TestOCRRealScreenshot | 100% (10/10) | 100% (10/10) |

#### 性能（误差范围内一致）

运行 `BenchmarkOCREngines` (benchtime=3s)：

| 指标 | brew 版 | GitHub 版 | 差异 |
|---|---|---|---|
| ns/op | 147,313,818 | 143,499,593 | **-2.6%**（误差范围） |
| boxes/op | 26.08 | 26.08 | 完全一致 |
| B/op | 43,957,005 | 43,839,026 | -0.3% |
| allocs/op | 1221 | 1220 | -0.1% |

### 结论

**GitHub Release 静态链接版本与 brew 动态链接版本在推理结果和性能上无实质差异。** ONNX Runtime 纯 CPU 推理路径不受动态/静态链接方式影响——计算图执行完全一致，输入输出字节同级相同。

推荐发布构建使用 GitHub Release 版本 + `ocr_embed` build tag：
- 产物自包含，不依赖用户安装 brew
- 37MB 的静态链接库比 18MB 的 brew 版本大，但省去了外部依赖管理的复杂度
- 本地开发可直接使用 `brew install onnxruntime`，无需 embed（`RuntimeLib` 为 nil 时自动 fallback 到系统路径）
