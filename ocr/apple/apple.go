//go:build darwin && cgo

// Package apple provides the "apple" OCR engine variant: the same PP-OCRv6
// det+rec pipeline as the ONNX engine, with the macOS Vision
// (VNDetectTextRectangles) detection fast-path as the default — accelerated by
// the Apple Neural Engine (ANE). Recognition is identical v6 ONNX; "apple"
// denotes the Apple-hardware detection path, not a separate recognition model.
//
// This routing replaced an earlier end-to-end Apple-Vision recognizer
// (VNRecognizeTextRequest via ocr.VisionFullOCR) for two reasons:
//   - Vision is Apple's own OCR, not a PaddleOCR model, so it could not be put
//     on PP-OCRv6 (the project's parity target). Routing through the v6
//     pipeline puts all three engines on the same v6 model+dict.
//   - The end-to-end Vision CGO bridge crashed (SIGBUS inside
//     [handler performRequests:]) under the Go test harness.
//
// useVision selects the detection path: true (default in the daemon) uses the
// ANE Vision fast-path; false falls back to the ONNX detector. The Vision
// fast-path works in the real daemon binary but SIGBUS-crashes under `go test`
// (the ephemeral test binary lacks the Vision/ANE runtime entitlements the
// signed daemon has), so tests pass useVision=false and exercise the ONNX det
// path — same v6 model, just a different detector.
//
// Enable with: PHONEFAST_OCR_ENGINE=apple
package apple

import (
	ocrsvc "github.com/gezihua123/phonefast/ocr"
	onnxeng "github.com/gezihua123/phonefast/ocr/onnx"
)

func init() {
	ocrsvc.RegisterEngine(ocrsvc.EngineApple, func(useVision bool) (ocrsvc.Engine, error) {
		return NewEngine(useVision)
	})
}

// NewEngine returns the Apple engine: v6 det+rec (the ONNX engine's pipeline)
// with useVision selecting the detection path. useVision=true uses the macOS
// Vision ANE detection fast-path; false uses the ONNX detector. The rec/det
// models and ONNX Runtime lifecycle are owned by the underlying ONNX engine.
func NewEngine(useVision bool) (ocrsvc.Engine, error) {
	return onnxeng.NewEngine(useVision)
}
