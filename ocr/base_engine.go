package ocr

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"

	"github.com/gezihua123/phonefast/ocr/common"
	"github.com/gezihua123/phonefast/ocr/detect"
)

// BaseEngine is the shared OCR engine skeleton. It owns the full Recognize
// flow (PNG/JPEG decode → detection → crop → recognition → filter) and delegates
// the engine-specific recognition step to a common.Recognizer.
//
// Detection is the shared detect.Detector (macOS Vision fast-path with an ONNX
// det fallback), so every engine gets cross-platform detection for free —
// engines only differ in how they recognize the cropped text boxes.
//
// Concrete engines (onnx, ncnn, tesseract) construct a BaseEngine with their own
// Recognizer; they do not implement Recognize themselves.
type BaseEngine struct {
	Det *detect.Detector
	Rec common.Recognizer
}

// Recognize performs OCR: decode → detect → crop → recognize → filter.
//
// Accepts PNG or JPEG bytes — image.Decode auto-detects the format via the
// blank-imported decoders. The raw bytes are also passed to Vision detection
// (which needs the original encoded PNG/JPEG), so both formats flow through.
//
// Single-goroutine contract: not safe for concurrent use (the detector's ONNX
// session and most recognizers are not concurrency-safe; the Service serializes).
func (b *BaseEngine) Recognize(imgBytes []byte) ([]TextResult, error) {
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	boxes, err := b.Det.Detect(img, imgBytes)
	if err != nil {
		return nil, fmt.Errorf("text detection: %w", err)
	}
	if len(boxes) == 0 {
		return nil, nil
	}

	// Crop all boxes — copies pixel data so the full image can be freed.
	crops := make([]image.Image, len(boxes))
	for i, box := range boxes {
		crops[i] = common.CropBox(img, box)
	}
	// Release the full decoded image (~10 MB for 1080×2400) before
	// recognition allocates tensors — reduces peak memory by ~10 MB.
	img = nil

	texts, err := b.Rec.RecognizeBoxes(crops)
	if err != nil {
		return nil, fmt.Errorf("text recognition: %w", err)
	}

	results := make([]TextResult, 0, len(boxes))
	for i, t := range texts {
		if i >= len(boxes) || t.Text == "" {
			continue
		}
		results = append(results, TextResult{
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
