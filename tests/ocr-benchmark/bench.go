// Package ocrbenchmark contains OCR performance benchmark tests.
// Run with: go test -v -run TestOCRBenchmark -count 1 ./tests/ocr-benchmark/
package ocrbenchmark

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

// runBenchmark runs the standard OCR benchmark (3 warmup + 15 timed iterations
// per image) over all images/*.png, logging a per-image table and an AVERAGE
// summary. Shared by TestOCRBenchmark (onnx) and TestNCNNBenchmark (ncnn) so
// the two engines are measured identically.
//
// header is printed as the table title; logSample additionally prints the first
// recognized text per image (handy for eyeballing rec quality across engines).
func runBenchmark(t *testing.T, eng pkgocr.Engine, header string, logSample bool) {
	t.Helper()

	matches := collectImages("images")
	if len(matches) == 0 {
		t.Skip("no test images in images/")
	}
	sort.Strings(matches)

	t.Logf("\n=== %s ===\n", header)
	t.Logf("%-22s %5s %6s %8s", "image", "boxes", "avg", "per-box")
	t.Logf("----------------------------------------------")

	type result struct {
		name  string
		boxes int
		avgMs int64
	}
	var results []result

	for _, path := range matches {
		pngData, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := filepath.Base(path)

		// warmup
		for i := 0; i < 3; i++ {
			eng.Recognize(pngData)
		}

		// 15 timed iterations
		const n = 15
		var total int64
		var lastBoxes int
		var sample string
		for i := 0; i < n; i++ {
			t0 := time.Now()
			r, _ := eng.Recognize(pngData)
			total += time.Since(t0).Milliseconds()
			lastBoxes = len(r)
			if i == 0 && len(r) > 0 {
				sample = r[0].Text
			}
		}

		avgMs := total / int64(n)
		perBox := float64(avgMs) / float64(max1(lastBoxes))
		if logSample {
			t.Logf("%-22s %5d %5dms %7.2fms  %q", name, lastBoxes, avgMs, perBox, sample)
		} else {
			t.Logf("%-22s %5d %5dms %7.2fms", name, lastBoxes, avgMs, perBox)
		}
		results = append(results, result{name, lastBoxes, avgMs})
	}

	// Summary
	var totMs, totBoxes int64
	for _, r := range results {
		totMs += r.avgMs
		totBoxes += int64(r.boxes)
	}
	t.Logf("----------------------------------------------")
	t.Logf("%-22s %5d %5dms %7.2fms",
		"AVERAGE", int(totBoxes)/len(results), int(totMs)/len(results),
		float64(totMs)/float64(totBoxes))
}

func collectImages(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	return matches
}

func max1(a int) int {
	if a < 1 {
		return 1
	}
	return a
}
