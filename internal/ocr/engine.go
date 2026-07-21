package ocr

import (
	"fmt"

	"github.com/gezihua123/phonefast/internal/ocr/ncnn"
	"github.com/gezihua123/phonefast/internal/ocr/onnx"
	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

// newEngine constructs the configured OCR engine (detection + recognition).
//
// Both engines share the same detection layer (internal/ocr/detect.Detector:
// Vision fast-path + onnx det fallback) and differ only in recognition:
// EngineONNX batches rec via ONNX Runtime; EngineNCNN runs rec one box at a
// time via purego-dlopen'd libncnn (macOS-only, -tags ncnn). The ncnn
// package's real/stub pair (mirroring pkg/avcodec) returns ErrNotAvailable
// when the ncnn tag isn't set.
func newEngine(cfg Config) (pkgocr.Engine, error) {
	switch cfg.Engine {
	case "", pkgocr.EngineONNX:
		return onnx.NewEngine(cfg.UseVision)
	case pkgocr.EngineNCNN:
		return ncnn.NewEngine(cfg.UseVision)
	default:
		return nil, fmt.Errorf("%w: unknown OCR engine %q (want %q or %q)",
			pkgocr.ErrNotAvailable, cfg.Engine, pkgocr.EngineONNX, pkgocr.EngineNCNN)
	}
}
