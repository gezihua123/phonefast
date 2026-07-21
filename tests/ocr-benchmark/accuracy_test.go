//go:build darwin && cgo && ncnn

package ocrbenchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gezihua123/phonefast/internal/ocr/ncnn"
	"github.com/gezihua123/phonefast/internal/ocr/onnx"
	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

// TestOCRAccuracy compares onnx vs ncnn recognition on the same images: how
// often the two engines agree (same text for the same detected box region) and
// where they diverge. onnx is the reference (cleaner output via dynamic-width
// batch rec); ncnn is the faster single-box path with known phantom-tail risk.
//
// Run: CGO_ENABLED=1 go test -tags ncnn -v -run TestOCRAccuracy -count 1 ./tests/ocr-benchmark/
func TestOCRAccuracy(t *testing.T) {
	matches := collectImages("images")
	if len(matches) == 0 {
		t.Skip("no test images")
	}
	sort.Strings(matches)

	// Build both engines with Vision detection (same boxes for both → fair
	// text comparison; only the rec step differs).
	onnxEng, err := onnx.NewEngine(true)
	if err != nil {
		t.Fatal(err)
	}
	defer onnxEng.Close()

	paramPath, _ := filepath.Abs(filepath.Join("..", "ocr-models", "ncnn", "rec.ncnn.param"))
	binPath, _ := filepath.Abs(filepath.Join("..", "ocr-models", "ncnn", "rec.ncnn.bin"))
	t.Setenv("PHONEFAST_NCNN_PARAM", paramPath)
	t.Setenv("PHONEFAST_NCNN_BIN", binPath)
	ncnnEng, err := ncnn.NewEngine(true)
	if err != nil {
		t.Fatal(err)
	}
	defer ncnnEng.Close()

	type pair struct{ onnx, ncnn pkgocr.TextResult }
	var totalBoxes, agreed, onnxOnly, ncnnOnly, bothEmpty int
	var diverge []pair

	for _, path := range matches {
		pngData, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		o, _ := onnxEng.Recognize(pngData)
		n, _ := ncnnEng.Recognize(pngData)

		// Index by box center (rounded) to match onnx boxes with ncnn boxes —
		// both use the same Vision detector so boxes should align 1:1, but
		// match defensively by center proximity.
		oi := indexByCenter(o)
		ni := indexByCenter(n)

		keys := unionKeys(oi, ni)
		var imgAgree int
		for _, k := range keys {
			totalBoxes++
			or, okO := oi[k]
			nr, okN := ni[k]
			switch {
			case okO && okN:
				if or.Text == nr.Text {
					agreed++
					imgAgree++
				} else {
					if or.Text == "" {
						ncnnOnly++
					} else if nr.Text == "" {
						onnxOnly++
					} else {
						diverge = append(diverge, pair{or, nr})
					}
				}
			case okO:
				if or.Text == "" {
					bothEmpty++
				} else {
					onnxOnly++
				}
			case okN:
				if nr.Text == "" {
					bothEmpty++
				} else {
					ncnnOnly++
				}
			}
		}
		t.Logf("%-22s onnx=%2d ncnn=%2d agree=%d", name, len(o), len(n), imgAgree)
	}

	t.Logf("----------------------------------------------")
	t.Logf("total boxes:        %d", totalBoxes)
	t.Logf("agreed (same text): %d (%.1f%%)", agreed, pct(agreed, totalBoxes))
	t.Logf("diverged (diff):    %d", len(diverge))
	t.Logf("onnx-only text:     %d", onnxOnly)
	t.Logf("ncnn-only text:     %d", ncnnOnly)
	t.Logf("both empty:         %d", bothEmpty)

	// Show up to 15 divergence samples.
	shown := 0
	for _, p := range diverge {
		if shown >= 15 {
			break
		}
		t.Logf("  DIVERGE  onnx=%-28q  ncnn=%q", p.onnx.Text, p.ncnn.Text)
		shown++
	}
}

func indexByCenter(rs []pkgocr.TextResult) map[uint64]pkgocr.TextResult {
	m := make(map[uint64]pkgocr.TextResult, len(rs))
	for _, r := range rs {
		cx, cy := r.Center()
		// round to ~10px buckets so tiny float diffs collapse
		k := uint64(int(cx/10))<<32 | uint64(int(cy/10))
		m[k] = r
	}
	return m
}

func unionKeys(a, b map[uint64]pkgocr.TextResult) []uint64 {
	seen := map[uint64]bool{}
	var ks []uint64
	for k := range a {
		if !seen[k] {
			ks = append(ks, k)
			seen[k] = true
		}
	}
	for k := range b {
		if !seen[k] {
			ks = append(ks, k)
			seen[k] = true
		}
	}
	return ks
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}

var _ = fmt.Sprintf
