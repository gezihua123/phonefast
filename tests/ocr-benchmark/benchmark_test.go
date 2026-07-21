//go:build !windows

package ocrbenchmark

import (
	"testing"

	"github.com/gezihua123/phonefast/internal/ocr/onnx"
)

// TestOCRBenchmark runs the full OCR performance benchmark on all test images
// using the default ONNX Runtime engine.
func TestOCRBenchmark(t *testing.T) {
	eng, err := onnx.NewEngine(true)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	runBenchmark(t, eng, "OCR Performance Benchmark (H=48, ONNX Runtime)", false)
}
