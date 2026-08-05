# TODO

## OCR 识别方案选型

**背景**: 华为应用市场、Google Play 等 Compose 页面，标题文字（如"小红书-你的生活兴趣"）渲染在 Canvas 上，不走 Android Accessibility API。phonefast 当前的 UISocketHandler 和 uiautomator dump 都无法获取这类文字。

**目标**: 寻找轻量、高效（目标 <500ms）、准确率高的 OCR 模型/方案，作为 Accessibility 的 fallback 路径。

**现状**:
- PaddleOCR v6 medium: 3.5s/张，太慢
- Tesseract: 白字渐变背景无法识别
- macOS Vision API: 未测试（PyObjC 未安装）

**候选方案**:

| 方案 | 预期耗时 | 体积 | 跨平台 | 中文 |
|---|---|---|---|---|
| RapidOCR (ONNX) | ~200-500ms | 小 | ✅ | ✅ |
| PaddleOCR Lite / mobile | ~1-2s | 中 | ✅ | ✅ |
| EasyOCR | ~1-3s | 中 | ✅ | ✅ |
| macOS Vision | ~100ms | 0 | ❌ | ✅ |
| Tesseract + 预处理 | ~500ms | 小 | ✅ | ⚠️ |
| 云端 OCR API | ~200ms | 0 | ✅ | ✅ |

**策略**: Accessibility 为主（191ms），OCR 仅在"节点存在但 text/desc 全空"的 Compose 盲区手动降级触发。

**负责人**: Team
