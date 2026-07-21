//go:build windows

// detect_windows.go — Windows stub for the detect package. Windows OCR is not
// supported (the onnx engine's onnx_windows.go returns ErrNotAvailable because
// onnxruntime-purego uses dlopen, unavailable on Windows), so detection is
// also unavailable here. The Detector type exists (so engine.BaseEngine and
// others compile on Windows) but NewDetector always fails.
package detect

import (
	"fmt"
	"image"

	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

// Detector is a no-op placeholder on Windows. Detect and Close exist only to
// satisfy engine.BaseEngine's references; they are never called (NewDetector
// fails first). Runtime()/Env() are NOT needed — those are only called by
// onnx.go (!windows).
type Detector struct{}

func NewDetector(useVision bool) (*Detector, error) {
	return nil, fmt.Errorf("%w: OCR detection not supported on Windows (onnxruntime-purego uses dlopen)", pkgocr.ErrNotAvailable)
}

func (d *Detector) Detect(img image.Image, pngData []byte) ([][4][2]float64, error) {
	return nil, fmt.Errorf("%w: unavailable", pkgocr.ErrNotAvailable)
}

func (d *Detector) Close() error { return nil }
