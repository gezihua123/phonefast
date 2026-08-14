# phonefast-ocr

Phonefast OCR 子模块 — 多引擎文字识别

## 引擎选型

| Engine | Boxes | Time | 中文 | 平台 | 依赖 |
|--------|-------|------|------|------|------|
| ONNX | 52 | 300ms | ✅ | 全平台 | 嵌入 12MB |
| Apple Vision | 45 | 246ms | ✅ | macOS | 系统内置 |
| NCNN | 31 | 157ms | ✅ | macOS | brew ncnn |
| Tesseract | 59 | 480ms | ❌ | 全平台 | 系统 |

## 快速开始

```go
import "github.com/gezihua123/phonefast/ocr"

svc := ocr.NewService(ocr.Config{Engine: ocr.EngineONNX})
results, _ := svc.Recognize(jpgBytes)
```

## 引擎选择

- `PHONEFAST_OCR_ENGINE=onnx` — 默认，全平台最准
- `PHONEFAST_OCR_ENGINE=apple` — macOS 无需模型
- `PHONEFAST_OCR_ENGINE=ncnn` — macOS 需 `-tags ncnn`
- `PHONEFAST_OCR_ENGINE=tesseract` — 纯英文降级

## 构建

```bash
# 默认
cd ocr && go build ./...

# Apple Vision
CGO_ENABLED=1 go build ./...

# NCNN
CGO_ENABLED=1 go build -tags ncnn ./...

# 全量 (嵌入 ORT 库)
go build -tags ocr_embed ./...
```

## 测试

```bash
go test ./... -run TestOCRSmoke -v
cd benchmark && go test -bench=. -benchmem
```

## 模型下载

```bash
bash scripts/download-ocr-models.sh       # 生产模型
bash scripts/download-ocr-test-models.sh   # 测试模型
bash scripts/setup-ncnn.sh                 # NCNN 环境
```

## 优化目标

- 从 ONNX / PaddleOCR / NCNN / Tesseract 中选出最快、最准、Go 接入最友好的方案
- 生成测试图片，多方案对比（识别效果、内存消耗、执行速度）
- 考虑 Tesseract 优化
- 单场景压测不低于 dev.md 中记录的基准水平
