# Benchmark 报告

## 引擎对比 (2026-08-05)

测试图片: screenshot_20260805_192138.jpg (1080×2400 JPEG, 小红书搜索页)
测试环境: macOS arm64 (M4 Pro), warm 10 轮后采样

| Engine | Boxes | Time | 中文 | 平台 | 依赖 |
|--------|-------|------|------|------|------|
| Go ONNX | 52 | 300ms | ✅ | 全平台 | 嵌入 12MB |
| Go Apple Vision | 45 | 246ms | ✅ | macOS | 无 |
| Python RapidOCR | 40 | 696ms | ✅ | Python | 运行时 |
| Go ONNX+Vision | 32 | 169ms | ✅ | macOS | 无 |
| Go NCNN v4 | 31 | 157ms | ✅ | macOS | brew ncnn |
| Go Tesseract | 59 | 480ms | ❌ | 全平台 | 系统 |

## 历史基准 (dev.md)

| 优化阶段 | 端到端 | 提升 |
|---------|--------|------|
| 纯 ONNX 基线 (全分辨率) | 330ms | 1.0× |
| maxSide=1024 降采样 | 203ms | 1.6× |
| resize 快路径 | 186ms | 1.8× |
| macOS Vision ANE detect | 110ms | 3.0× |
| 去死代码 | 108ms | 3.1× |

## 当前基准 (20 图 Benchmark)

| 指标 | 值 |
|------|-----|
| BenchmarkOCRBench | 169.6ms/op |
| 内存 | 44.1MB/op |
| 分配 | 2069 allocs/op |
| 峰度 | 1.12× (spike) |
| 确定性 | 30 轮一致 |

## 测试方法

```bash
go test -bench=BenchmarkOCRBench -benchmem ./benchmark/
go test -run TestOCRSpike ./benchmark/
go test -run TestOCRDeterminism ./benchmark/
```

## 引擎准确率对比

### 引擎间共同检出

4 个中文引擎（ONNX/Apple/Vision/NCNN）都正确检出的核心文本：

- xiaohongshu
- 人高效学外语
- 取消
- 赞助商广告·为您推荐
- Canva可画：设计、照片和视频
- 语言学习
- Discord-聊天，游戏，消磨时光
- 小红书一你的生活兴趣社区
- 您是不是要搜索以下内容：小红书
- 限时活动

### 与 Python 基线达标度

| Engine | 覆盖率 | 速度倍率 | 综合 |
|--------|--------|----------|------|
| Go ONNX | +30% | 2.3× | ✅✅ |
| Go Apple Vision | +12% | 2.8× | ✅✅ |
| Go NCNN v4 | -22% | 4.4× | ✅ |
| Go ONNX+Vision | -20% | 4.1× | ⚠️ |

## 推荐策略

```
默认场景     → ONNX (52 boxes, 300ms, 全平台最准)
macOS 快速   → ONNX+Vision (32 boxes, 169ms)
macOS 无模型 → Apple Vision (45 boxes, 246ms)
macOS 极速   → NCNN v4 (31 boxes, 157ms)
纯英文降级   → Tesseract (仅英文)
```

## NCNN 优化记录

| 版本 | 模型 | Go 代码 | 问题 | 结果 |
|------|------|---------|------|------|
| v1 | fixed-320, FP16 | pad到320 | phantom tail | ❌ |
| v4 | dynamic, FP32 | 实际宽度 | 无 phantom tail | ✅ |
