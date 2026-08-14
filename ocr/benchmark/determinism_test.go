//go:build darwin && cgo

package ocrbenchmark

import (
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/gezihua123/phonefast/ocr/onnx"
	pkgocr "github.com/gezihua123/phonefast/ocr"
)

// TestOCRDeterminism runs the same image through OCR many times and asserts
// the output is identical every iteration. This is the correctness guard for
// the input-tensor pooling optimization: if ORT's zero-copy
// CreateTensorWithDataAsOrtValue still referenced a reused scratch buffer
// during inference, results would corrupt non-deterministically. Stable output
// across 30 iterations proves the buffer is reused only after the input Value
// is closed.
//
// Run: go test -v -run TestOCRDeterminism -count 1 ./tests/ocr-benchmark/
func TestOCRDeterminism(t *testing.T) {
	matches := collectImages("images")
	if len(matches) == 0 {
		t.Skip("no test images")
	}
	sort.Strings(matches)

	eng, err := onnx.NewEngine(true)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	const iters = 30
	failed := 0
	for _, path := range matches {
		png, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		eng.Recognize(png) // warmup: grow scratch buffers to steady state

		var first string
		for i := 0; i < iters; i++ {
			r, err := eng.Recognize(png)
			if err != nil {
				t.Fatalf("%s iter %d: %v", path, i, err)
			}
			blob := serialize(r)
			if i == 0 {
				first = blob
				continue
			}
			if blob != first {
				failed++
				t.Errorf("%s iter %d: output drifted from iter 0 — non-deterministic, "+
					"likely a buffer-reuse race in tensor pooling", path, i)
				break
			}
		}
	}
	if failed == 0 {
		t.Logf("all images deterministic across %d iterations each", iters)
	}
}

// serialize flattens []TextResult into a stable string for comparison.
func serialize(r []pkgocr.TextResult) string {
	var b []byte
	for i := range r {
		// %#v of the struct captures Text, Box (all 8 floats), Confidence.
		b = fmt.Appendf(b, "%s|%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f|%.4f\n",
			r[i].Text,
			r[i].Box[0][0], r[i].Box[0][1], r[i].Box[1][0], r[i].Box[1][1],
			r[i].Box[2][0], r[i].Box[2][1], r[i].Box[3][0], r[i].Box[3][1],
			r[i].Confidence)
	}
	return string(b)
}
