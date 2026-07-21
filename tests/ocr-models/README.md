# OCR 模型存档

## 获取测试模型

测试模型（v3 备份、v4 mobile 评测用）**不随仓库提交**（.gitignore'd，大且可复现）。
按需下载：

```bash
bash scripts/download-ocr-test-models.sh          # v3 + v4 mobile
bash scripts/download-ocr-test-models.sh --v3     # 仅 v3
bash scripts/download-ocr-test-models.sh --v4     # 仅 v4 mobile
```

> 生产模型（`ppocr-det.onnx` / `ppocr-rec.onnx`）在 `assets/ocr/`，由
> `scripts/download-ocr-models.sh --models` 下载并 embed 进二进制，与此处测试模型不同。

## 已测试模型矩阵

| 文件 | 来源 | 大小 | 类型 | 召回率 | 速度 | 结论 |
|---|---|---|---|---|---|---|
| v3_det.onnx | RapidOCR HF | 2.3MB | det | — | 33ms | ✅ 当前使用 |
| v3_rec.onnx | RapidOCR HF | 10MB | rec | 92% | 5.0ms | ✅ 当前使用 |
| v4_mobile_det.onnx | RapidOCR HF | 4.5MB | det | — | 37ms | ⚠️ 2×大,同速 |
| v4_mobile_rec.onnx | RapidOCR HF | 10MB | rec | 96% | 4.0ms | ⚠️ 更准但批量退化 |
| v4_server_det.onnx | RapidOCR HF | 108MB | det | — | 1194ms | ❌ 35×慢 |
| v4_server_rec.onnx | RapidOCR HF | 86MB | rec | 92% | 49.7ms | ❌ 10×慢 |
| v2_det.onnx | RapidOCR HF | 2.2MB | det | — | 33ms | ❌ 搭配v2_rec |
| v2_rec.onnx | RapidOCR HF | 8.0MB | rec | 0% | 2.9ms | ❌ 中文全丢 |
| v1_mobile_det.onnx | RapidOCR HF | 2.2MB | det | — | — | ❌ 崩溃 |
| v1_mobile_rec.onnx | RapidOCR HF | 4.2MB | rec | — | 1.6ms | ❌ 崩溃 |
| v1_server_det.onnx | RapidOCR HF | 47MB | det | — | — | ❌ 崩溃 |
| v1_server_rec.onnx | RapidOCR HF | 106MB | rec | — | — | ❌ 崩溃 |
| v6_small_rec.onnx | PaddlePaddle HF | 20MB | rec | — | 3.5ms | ❌ 字典缺失 |
| omni_yolo.onnx | Microsoft OmniParser | 77MB | icon det | — | 144ms | ❌ 太重 |
| omni_yolo.pt | Microsoft OmniParser | 39MB | icon det | — | 165ms | ❌ PyTorch |

## 来源

- **RapidOCR HF**: https://huggingface.co/SWHL/RapidOCR
- **PaddlePaddle HF**: https://huggingface.co/PaddlePaddle
- **Microsoft OmniParser**: https://github.com/microsoft/OmniParser

## HuggingFace 下载地址

### 当前默认

```bash
# PP-OCR v3 (当前 phonefast 使用)
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv3/ch_PP-OCRv3_det_infer.onnx
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv3/ch_PP-OCRv3_rec_infer.onnx
```

### PP-OCR v4

```bash
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv4/ch_PP-OCRv4_det_infer.onnx
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv4/ch_PP-OCRv4_rec_infer.onnx
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv4/ch_PP-OCRv4_det_server_infer.onnx
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv4/ch_PP-OCRv4_rec_server_infer.onnx
```

### PP-OCR v2

```bash
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv2/ch_PP-OCRv2_det_infer.onnx
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv2/ch_PP-OCRv2_rec_infer.onnx
```

### PP-OCR v1

```bash
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv1/ch_ppocr_mobile_v2.0_det_infer.onnx
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv1/ch_ppocr_mobile_v2.0_rec_infer.onnx
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv1/ch_ppocr_server_v2.0_det_infer.onnx
https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv1/ch_ppocr_server_v2.0_rec_infer.onnx
```

### PP-OCR v6 small rec

```bash
https://huggingface.co/PaddlePaddle/PP-OCRv6_small_rec_onnx/resolve/main/inference.onnx
```

### OmniParser YOLO (Microsoft)

```bash
# ONNX 导出 (ultralytics 导出, 非官方发布)
# 原始 PyTorch:
https://huggingface.co/microsoft/OmniParser-v2.0/resolve/main/icon_detect/model.pt
```

### 一键下载

```bash
BASE="https://huggingface.co/SWHL/RapidOCR/resolve/main"
curl -L "$BASE/PP-OCRv3/ch_PP-OCRv3_det_infer.onnx" -o v3_det.onnx
curl -L "$BASE/PP-OCRv3/ch_PP-OCRv3_rec_infer.onnx" -o v3_rec.onnx
curl -L "$BASE/PP-OCRv4/ch_PP-OCRv4_det_infer.onnx" -o v4_mobile_det.onnx
curl -L "$BASE/PP-OCRv4/ch_PP-OCRv4_rec_infer.onnx" -o v4_mobile_rec.onnx
```

### 可用模型完整列表

RapidOCR HuggingFace 仓库共有 19 个 ONNX 模型:
https://huggingface.co/SWHL/RapidOCR/tree/main
