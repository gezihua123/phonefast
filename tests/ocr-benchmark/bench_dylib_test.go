//go:build darwin && cgo

package ocrbenchmark

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gezihua123/phonefast/internal/ocr/onnx"
)

// BenchmarkOCRBench is a testing.B benchmark for the onnx engine. Run with a
// fixed iteration count to compare ORT dylib variants (brew vs release-static):
//
//	CGO_ENABLED=1 go test -bench BenchmarkOCRBench -benchtime=50x -run ^$ -count 1 ./tests/ocr-benchmark/
//
// Each iteration runs a full Recognize on one representative image. The dylib
// variant is baked in at build time (assets/ocr/libonnxruntime-darwin-arm64.dylib),
// so to compare brew vs release: build+bench with one dylib, swap the asset,
// rebuild+bench with the other. The helper script scripts/bench-ort-dylib.sh
// automates this 50-iteration comparison.
func BenchmarkOCRBench(b *testing.B) {
	// Use the densest image (05_fixed.png, 34 boxes) — stresses rec the most.
	png, err := os.ReadFile(filepath.Join("images", "05_fixed.png"))
	if err != nil {
		b.Skip("no 05_fixed.png (run from tests/ocr-benchmark/)")
	}
	eng, err := onnx.NewEngine(true)
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()

	// Warmup (compile Vision/CoreML once).
	eng.Recognize(png)
	eng.Recognize(png)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eng.Recognize(png)
	}
}
