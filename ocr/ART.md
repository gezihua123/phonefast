# OCR 架构

## 架构总览

```
                   ┌─────────────────────┐
                   │    ocr.Service      │  ← 单例，mutex 序列化
                   │  (lazy init, warmup)│
                   └──────┬──────────────┘
                          │
              ┌───────────┴───────────┐
              │   Engine Registry     │  ← 引擎注册表 (init())
              │   RegisterEngine()    │
              └───────────┬───────────┘
                          │
        ┌─────────────────┼─────────────────┐
        │                 │                 │
   ┌────┴────┐      ┌────┴────┐      ┌────┴────┐
   │  ONNX   │      │  NCNN   │      │  Apple  │
   │ (purego)│      │ (dlopen)│      │ (Vision)│
   └────┬────┘      └────┬────┘      └─────────┘
        │                │
   ┌────┴────┐      ┌────┴────┐
   │ BaseEng │      │ BaseEng │
   │ +Detect │      │ +Detect │
   │ +OnnxRec│      │ +NcnnRec│
   └─────────┘      └─────────┘
        │                │
   ┌────┴────┐      ┌────┴────┐
   │ common/ │◄─────│ common/ │
   │ detect/ │      │ detect/ │
   └─────────┘      └─────────┘
```

## 代码结构

```
ocr/
├── engine.go              ← 公共 API (Engine, TextResult, ErrNotAvailable)
├── service.go             ← 单例服务 (lazy init, warmup, mutex)
├── registry.go            ← 引擎注册表
├── base_engine.go         ← BaseEngine 骨架 (decode→detect→crop→rec→filter)
├── vision_full_darwin.go  ← Apple VNRecognizeTextRequest 桥接
│
├── common/                ← 共享工具
│   ├── engine.go          ← Recognizer 接口
│   ├── ctc.go             ← CTC 解码 (embedded 中文字典)
│   ├── preprocess.go      ← 张量预处理
│   ├── postprocess.go     ← DB 后处理 + 框提取
│   ├── image.go           ← 图片缩放/裁剪
│   ├── vision_darwin.*    ← VNDetectTextRectangles 桥接
│   └── vision_noop.go     ← 非 macOS 占位
│
├── detect/                ← 检测层
│   ├── detect.go          ← Detector (Vision + ONNX det fallback)
│   └── libpath.go         ← ORT 库加载
│
├── onnx/                  ← ONNX 识别引擎 (默认)
│   └── onnx.go            ← OnnxRecognizer (batch rec)
│
├── ncnn/                  ← NCNN 识别引擎 (macOS opt-in)
│   └── ncnn.go            ← NcnnRecognizer (per-box rec)
│
├── tesseract/             ← Tesseract 引擎 (英文降级)
│   └── tesseract.go       ← subprocess tesseract CLI
│
├── apple/                 ← Apple Vision 引擎 (macOS)
│   └── apple.go           ← VNRecognizeTextRequest
│
└── assets/                ← 嵌入模型 + ORT 库
```

## 数据流

```
PNG/JPEG bytes
  → image.Decode       (Go 标准库)
  → Detect             (Vision ANE / ONNX det)
  → CropBox            (每个框截取)
  → RecognizeBoxes     (ONNX batch / NCNN per-box / Apple full)
  → CTC Decode         (PP-OCR 字典 + blank 去重)
  → []TextResult       (文字 + 坐标 + 置信度)
```
