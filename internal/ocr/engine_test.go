package ocr

import (
	"os"
	"path/filepath"
	"testing"

	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

// TestOCRSmoke verifies the OCR engine can initialize and recognize text
// from a real screenshot. Uses a small set of test images from the benchmark
// corpus; skips gracefully when no image or onnxruntime is available.
func TestOCRSmoke(t *testing.T) {
	// Find a test image — try a few known paths relative to repo root.
	imagePaths := []string{
		filepath.Join("..", "..", "tests", "ocr-benchmark", "images", "02_settings.png"),
		filepath.Join("tests", "ocr-benchmark", "images", "02_settings.png"),
	}
	var pngData []byte
	for _, p := range imagePaths {
		if data, err := os.ReadFile(p); err == nil {
			pngData = data
			t.Logf("using test image: %s", p)
			break
		}
	}
	if len(pngData) == 0 {
		t.Skip("no test image found (run from repo root or internal/ocr/)")
	}

	svc := NewService(Config{Engine: pkgocr.EngineONNX, UseVision: false})
	defer svc.Close()

	results, err := svc.Recognize(pngData)
	if err != nil {
		t.Fatalf("OCR recognize failed: %v", err)
	}

	// A Settings screenshot should contain recognizable text.
	if len(results) == 0 {
		t.Error("expected at least one text box, got 0")
	}
	t.Logf("recognized %d text boxes", len(results))
	for _, r := range results {
		cx, cy := r.Center()
		t.Logf("  text=%q conf=%.2f center=(%.0f,%.0f)", r.Text, r.Confidence, cx, cy)
	}
}
