package ocr_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	ocrsvc "github.com/gezihua123/phonefast/ocr"
	_ "github.com/gezihua123/phonefast/ocr/onnx"
)

// TestRecipeHarness is a manual validation harness: runs the ONNX engine on
// the benchmark recipe image and prints every recognized box (sorted by
// reading order) so the PaddleOCR parity work can be eyeballed. The recipe
// image also has must-contain entries in TestOCRBenchmarkAccuracy; this test
// adds the full per-box dump for visual inspection. Skipped under -short.
func TestRecipeHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping recipe harness in short mode")
	}
	path := filepath.Join("benchmark", "images", "recipes_sample.jpg")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("recipe image not found at %s: %v", path, err)
	}

	svc := ocrsvc.NewService(ocrsvc.Config{Engine: ocrsvc.EngineONNX, UseVision: false})
	defer svc.Close()

	results, err := svc.Recognize(data)
	if err != nil {
		t.Fatalf("OCR failed: %v", err)
	}

	type item struct {
		text   string
		conf   float32
		cx, cy float64
	}
	items := make([]item, 0, len(results))
	for _, r := range results {
		cx, cy := r.Center()
		items = append(items, item{strings.TrimSpace(r.Text), r.Confidence, cx, cy})
	}
	sort.Slice(items, func(i, j int) bool {
		if absInt(items[i].cy-items[j].cy) > 15 {
			return items[i].cy < items[j].cy
		}
		return items[i].cx < items[j].cx
	})

	t.Logf("RECIPE TOTAL: %d boxes", len(items))
	for i, it := range items {
		t.Logf("  [%2d] text=%-42q conf=%.2f center=(%4.0f,%4.0f)", i, it.text, it.conf, it.cx, it.cy)
	}

	// PaddleOCR baseline must-contain set (PP-OCRv6 medium, use_doc_unwarping=True,
	// run on this same image — see /tmp/paddle_baseline.py). phonefast should
	// match all 7 (and on the long line it now reads "servings" where PaddleOCR
	// itself truncates to "serving").
	want := []string{
		"Recipes from Image", "Chocolate Cake", "A rich dessert",
		"Chicken Soup", "Hearty soup", "Pasta Primavera", "Light pasta dish",
	}
	missed := 0
	for _, w := range want {
		found := false
		for _, it := range items {
			if strings.Contains(strings.ToLower(it.text), strings.ToLower(w)) {
				found = true
				break
			}
		}
		if !found {
			t.Logf("  MISS: %q", w)
			missed++
		}
	}
	t.Logf("RECIPE RECALL: %d/%d PaddleOCR-baseline texts found", len(want)-missed, len(want))
}

func absInt(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
