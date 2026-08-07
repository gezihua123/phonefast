//go:build darwin && cgo

// Package apple provides an OCR engine backed by Apple Vision's
// VNRecognizeTextRequest — full OCR with Chinese/English support
// accelerated by the Apple Neural Engine (ANE).
//
// Unlike the ONNX engines, this does NOT use separate detection +
// recognition models. Vision handles both in one ANE call.
//
// Enable with: PHONEFAST_OCR_ENGINE=apple
// Languages:    PHONEFAST_VISION_LANGS=zh-Hans,zh-Hant,en-US
package apple

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"

	ocrsvc "github.com/gezihua123/phonefast/ocr"
)

func init() {
	ocrsvc.RegisterEngine(ocrsvc.EngineApple, func(useVision bool) (ocrsvc.Engine, error) {
		return NewEngine()
	})
}

// EngineApple is a full OCR engine backed by Apple Vision.
type EngineApple struct{}

// NewEngine returns an Apple Vision OCR engine. Always available
// on macOS with CGO (Vision framework is built-in).
func NewEngine() (ocrsvc.Engine, error) {
	if !ocrsvc.VisionFullOCRAvailable() {
		return nil, fmt.Errorf("%w: Apple Vision OCR requires macOS with CGO", ocrsvc.ErrNotAvailable)
	}
	return &EngineApple{}, nil
}

// Recognize runs VNRecognizeTextRequest on the image and returns
// text + bounding boxes. Accepts PNG or JPEG bytes.
func (e *EngineApple) Recognize(imgBytes []byte) ([]ocrsvc.TextResult, error) {
	// Decode to get dimensions (Vision API uses raw bytes directly).
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("apple ocr: decode image: %w", err)
	}
	bounds := img.Bounds()
	imgW, imgH := bounds.Dx(), bounds.Dy()
	img = nil

	results := ocrsvc.VisionFullOCR(imgBytes, imgW, imgH)
	return results, nil
}

func (e *EngineApple) Close() error {
	return nil
}
