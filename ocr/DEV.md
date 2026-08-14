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
