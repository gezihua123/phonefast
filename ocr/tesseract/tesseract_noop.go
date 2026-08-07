//go:build windows

// Package tesseract provides the Tesseract OCR engine backend for phonefast.
// Windows is not supported (tesseract CLI detection via exec.LookPath is
// unreliable on Windows; use Linux/macOS or the ONNX backend).
package tesseract

import (
	"fmt"

	pkgocr "github.com/gezihua123/phonefast/ocr"
)

// NewEngine returns ErrNotAvailable on Windows.
func NewEngine() (pkgocr.Engine, error) {
	return nil, fmt.Errorf("%w: Tesseract OCR not supported on Windows (use the ONNX backend)", pkgocr.ErrNotAvailable)
}
