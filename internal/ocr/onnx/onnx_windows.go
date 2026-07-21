//go:build windows

package onnx

import (
	"fmt"

	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

// NewEngine returns ErrNotAvailable on Windows.
//
// onnxruntime-purego's runtime.go:65 calls purego.Dlopen(), which has a build
// constraint excluding Windows (dlfcn.go: //go:build darwin || freebsd || linux || netbsd).
// The rest of the library (RegisterLibFunc, RegisterFunc, session/value APIs) IS
// Windows-compatible — only the DLL loading call needs replacing with
// syscall.LoadLibrary. This is a ~3 line upstream fix. Until then, Windows OCR
// is disabled and callers fall back to accessibility-only or LLM-vision approaches.
//
// To enable Windows OCR in the future:
//   Option A: PR upstream to replace purego.Dlopen with a platform-aware loader
//   Option B: Use yalue/onnxruntime_go (CGO) for Windows only while keeping purego on Unix
func NewEngine(_ bool) (pkgocr.Engine, error) {
	return nil, fmt.Errorf("%w: ONNX OCR not supported on Windows (purego.Dlopen missing, see docs/DEV.md#待办)", pkgocr.ErrNotAvailable)
}
