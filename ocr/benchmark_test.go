package ocr_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/gezihua123/phonefast/ocr/onnx"      // register ONNX backend
	_ "github.com/gezihua123/phonefast/ocr/tesseract" // register Tesseract backend
	ocrsvc "github.com/gezihua123/phonefast/ocr"
)

// engineUnderTest is one benchmark target.
type engineUnderTest struct {
	Name   string
	Engine string
}

// findBenchImages finds PNG images in the benchmark corpus. Returns paths
// relative to the repo root.
func findBenchImages(t testing.TB) []string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "ocr", "benchmark", "images"),
		filepath.Join("ocr", "benchmark", "images"),
	}
	var dir string
	for _, d := range candidates {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			dir = d
			break
		}
	}
	if dir == "" {
		t.Skip("benchmark images not found (run from repo root or internal/ocr/)")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("cannot read benchmark dir: %v", err)
	}
	var paths []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".png") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	if len(paths) == 0 {
		t.Skip("no benchmark images found")
	}
	return paths
}

// loadImage reads a PNG into []byte.
func loadImage(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// BenchmarkOCREngines benchmarks all available OCR engines on real screenshots.
// Reports: ops/s, ns/op, and text boxes found.
//
// Run with: go test -bench=BenchmarkOCREngines -benchmem ./internal/ocr/ -v
func BenchmarkOCREngines(b *testing.B) {
	images := findBenchImages(b)
	if len(images) == 0 {
		return
	}

	// Pre-load images (I/O excluded from benchmark timing).
	type namedImg struct {
		Name string
		Data []byte
	}
	var loaded []namedImg
	for _, p := range images {
		data, err := loadImage(p)
		if err != nil {
			b.Logf("skip %s: %v", p, err)
			continue
		}
		loaded = append(loaded, namedImg{filepath.Base(p), data})
	}
	if len(loaded) == 0 {
		b.Skip("no images loaded")
	}

	engines := []engineUnderTest{
		{Name: "ONNX", Engine: ocrsvc.EngineONNX},
		{Name: "Tesseract", Engine: ocrsvc.EngineTesseract},
		{Name: "NCNN", Engine: ocrsvc.EngineNCNN},
	}

	for _, eng := range engines {
		b.Run(eng.Name, func(b *testing.B) {
			svc := ocrsvc.NewService(ocrsvc.Config{
				Engine:    eng.Engine,
				UseVision: false,
			})
			defer svc.Close()

			// Warmup: one call to trigger lazy init + model loading.
			_, _ = svc.Recognize(loaded[0].Data)

			totalBoxes := 0
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				img := loaded[i%len(loaded)]
				results, err := svc.Recognize(img.Data)
				if err != nil {
					if strings.Contains(err.Error(), "not available") ||
						strings.Contains(err.Error(), "not found") {
						b.Skipf("engine %s not available: %v", eng.Name, err)
					}
					b.Fatalf("Recognize failed: %v", err)
				}
				totalBoxes += len(results)
			}
			b.StopTimer()
			b.ReportMetric(float64(totalBoxes)/float64(b.N), "boxes/op")
		})
	}
}

// TestOCREngineComparison runs each available engine on all benchmark images
// and prints a comparison table. This is an informational test (not a pass/fail
// assertion) — use `-v` to see the results.
func TestOCREngineComparison(t *testing.T) {
	images := findBenchImages(t)
	if len(images) == 0 {
		return
	}

	// Limit to 3 images for speed.
	if len(images) > 3 {
		images = images[:3]
	}

	engines := []engineUnderTest{
		{Name: "ONNX", Engine: ocrsvc.EngineONNX},
		{Name: "Tesseract", Engine: ocrsvc.EngineTesseract},
	}

	t.Logf("=== OCR Engine Comparison (%d images) ===", len(images))

	for _, eng := range engines {
		svc := ocrsvc.NewService(ocrsvc.Config{
			Engine:    eng.Engine,
			UseVision: false,
		})
		defer svc.Close()

		// Pre-warm.
		if _, err := svc.Recognize(loadImageOrSkip(t, images[0])); err != nil {
			t.Logf("  %-12s: UNAVAILABLE (%v)", eng.Name, err)
			continue
		}

		var totalDur time.Duration
		totalBoxes := 0

		for _, imgPath := range images {
			data := loadImageOrSkip(t, imgPath)
			start := time.Now()
			results, err := svc.Recognize(data)
			if err != nil {
				t.Logf("  %-12s %-30s: ERROR %v", eng.Name, filepath.Base(imgPath), err)
				continue
			}
			dur := time.Since(start)
			totalDur += dur
			totalBoxes += len(results)
			t.Logf("  %-12s %-30s: %6s %2d boxes",
				eng.Name, filepath.Base(imgPath),
				dur.Round(time.Millisecond).String(), len(results))
		}

		avg := totalDur / time.Duration(len(images))
		t.Logf("  %-12s AVG: %s, %d boxes avg",
			eng.Name, avg.Round(time.Millisecond).String(),
			totalBoxes/len(images))
	}
}

func loadImageOrSkip(t *testing.T, path string) []byte {
	t.Helper()
	data, err := loadImage(path)
	if err != nil {
		t.Skipf("cannot load %s: %v", path, err)
	}
	return data
}

// TestOCRChineseEmpty verifies that Tesseract returns zero results on Chinese text.
// This documents the known limitation (see docs/DEV.md line 329).
func TestOCRChineseEmpty(t *testing.T) {
	imagePaths := []string{
		filepath.Join("..", "ocr", "benchmark", "images", "zh_01_settings.png"),
		filepath.Join("ocr", "benchmark", "images", "zh_01_settings.png"),
	}
	var pngData []byte
	for _, p := range imagePaths {
		if data, err := os.ReadFile(p); err == nil {
			pngData = data
			break
		}
	}
	if len(pngData) == 0 {
		t.Skip("Chinese test image not found")
	}

	svc := ocrsvc.NewService(ocrsvc.Config{
		Engine:    ocrsvc.EngineTesseract,
		UseVision: false,
	})
	defer svc.Close()

	results, err := svc.Recognize(pngData)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not available") {
			t.Skip("tesseract not available")
		}
		t.Fatalf("tesseract recognize: %v", err)
	}

	// Tesseract with default `eng` language cannot recognize Chinese.
	// It may find some boxes (misreading CJK as Latin) but should NOT
	// return actual Chinese characters. Count any CJK codepoint hit
	// as a false positive.
	cjkCount := 0
	for _, r := range results {
		for _, ch := range r.Text {
			if (ch >= 0x4E00 && ch <= 0x9FFF) || // CJK Unified
				(ch >= 0x3400 && ch <= 0x4DBF) || // CJK Extension A
				(ch >= 0xF900 && ch <= 0xFAFF) { // CJK Compatibility
				cjkCount++
			}
		}
	}
	if cjkCount > 0 {
		t.Errorf("Tesseract eng: expected 0 CJK chars, found %d (got %d results total)", cjkCount, len(results))
	}
	t.Logf("Tesseract on Chinese image: %d results, %d CJK chars (expected 0 CJK)", len(results), cjkCount)
	for _, r := range results {
		t.Logf("  text=%q conf=%.2f", r.Text, r.Confidence)
	}
}
