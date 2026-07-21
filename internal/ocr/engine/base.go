package engine

import (
	"bytes"
	"fmt"
	"image"
	"image/png"

	"github.com/gezihua123/phonefast/internal/ocr/common"
	"github.com/gezihua123/phonefast/internal/ocr/detect"
	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

// BaseEngine is the shared OCR engine skeleton. It owns the full Recognize
// flow (PNG decode → detection → crop → recognition → filter) and delegates
// the engine-specific recognition step to a common.Recognizer.
//
// Detection is the shared detect.Detector (macOS Vision fast-path with an ONNX
// det fallback), so every engine gets cross-platform detection for free —
// engines only differ in how they recognize the cropped text boxes.
//
// Concrete engines (onnx, ncnn) construct a BaseEngine with their own
// Recognizer; they do not implement Recognize themselves.
type BaseEngine struct {
	Det *detect.Detector
	Rec common.Recognizer
}

// Recognize performs OCR: decode → detect → crop → recognize → filter.
//
// Single-goroutine contract: not safe for concurrent use (the detector's ONNX
// session and most recognizers are not concurrency-safe; the Service serializes).
func (b *BaseEngine) Recognize(pngImage []byte) ([]pkgocr.TextResult, error) {
	img, err := png.Decode(bytes.NewReader(pngImage))
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}

	boxes, err := b.Det.Detect(img, pngImage)
	if err != nil {
		return nil, fmt.Errorf("text detection: %w", err)
	}
	if len(boxes) == 0 {
		return nil, nil
	}

	// Crop all boxes, then recognize in one call (the recognizer decides
	// batch vs per-box internally).
	crops := make([]image.Image, len(boxes))
	for i, box := range boxes {
		crops[i] = common.CropBox(img, box)
	}
	texts, err := b.Rec.RecognizeBoxes(crops)
	if err != nil {
		return nil, fmt.Errorf("text recognition: %w", err)
	}

	results := make([]pkgocr.TextResult, 0, len(boxes))
	for i, t := range texts {
		if i >= len(boxes) || t.Text == "" {
			continue
		}
		results = append(results, pkgocr.TextResult{
			Text:       t.Text,
			Box:        boxes[i],
			Confidence: t.Confidence,
		})
	}
	return results, nil
}

// Close releases the detector + recognizer resources.
func (b *BaseEngine) Close() error {
	recErr := b.Rec.Close()
	detErr := b.Det.Close()
	if recErr != nil {
		return recErr
	}
	return detErr
}
