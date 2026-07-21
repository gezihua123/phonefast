//go:build darwin && cgo && ncnn

package ocrbenchmark

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gezihua123/phonefast/internal/ocr/ncnn"
)

// TestNCNNBenchmark runs the OCR benchmark using the NCNN recognition backend.
// Compare against TestOCRBenchmark (ONNX).
//
// Run: CGO_ENABLED=1 go test -tags ncnn -v -run TestNCNNBenchmark -count 1 ./tests/ocr-benchmark/
//
// Requires the converted .ncnn model (run scripts/convert-ncnn.sh) + the
// PHONEFAST_NCNN_PARAM / PHONEFAST_NCNN_BIN env vars pointing at it.
func TestNCNNBenchmark(t *testing.T) {
	paramPath := filepath.Join("..", "ocr-models", "ncnn", "rec.ncnn.param")
	if _, err := os.Stat(paramPath); err != nil {
		t.Skipf("ncnn model not found at %s (run scripts/convert-ncnn.sh)", paramPath)
	}
	abs, err := filepath.Abs(filepath.Dir(paramPath))
	if err != nil {
		t.Fatal(err)
	}
	// ncnn.NewEngine reads PHONEFAST_NCNN_PARAM/BIN; point them at the converted model.
	t.Setenv("PHONEFAST_NCNN_PARAM", filepath.Join(abs, "rec.ncnn.param"))
	t.Setenv("PHONEFAST_NCNN_BIN", filepath.Join(abs, "rec.ncnn.bin"))

	eng, err := ncnn.NewEngine(true)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	runBenchmark(t, eng, "NCNN OCR Benchmark (single-box rec, W=320)", true)
}
