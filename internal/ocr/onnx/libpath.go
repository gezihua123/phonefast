//go:build !windows

package onnx

import (
	"github.com/gezihua123/phonefast/internal/ocr/detect"
)

// writeTempFile delegates to detect.WriteTempFile (the single implementation).
// The onnx package no longer loads its own ORT runtime — it reuses the
// detector's runtime via det.Runtime()/det.Env() — so loadRuntimeLib/
// findSystemLib/systemLibPaths/runtimeLibName were dead code and removed.
func writeTempFile(data []byte, pattern string) (string, error) {
	return detect.WriteTempFile(data, pattern)
}
