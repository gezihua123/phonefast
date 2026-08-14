//go:build !NO_OCR_MODELS

package ocr

import _ "embed"

//go:embed ppocr-det.onnx
var DetModel []byte

//go:embed ppocr-rec.onnx
var RecModel []byte
