//go:build darwin && cgo

package ocrbenchmark

import (
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/gezihua123/phonefast/internal/ocr/onnx"
)

// TestOCRSpike runs many iterations on the densest image and reports the
// timing distribution (min/p50/p90/p99/max) plus a spike factor (max/median)
// and GC pause totals. The goal: max should not show increasing spikes.
// Run:
//
//	go test -v -run TestOCRSpike -count 1 ./tests/ocr-benchmark/
func TestOCRSpike(t *testing.T) {
	png, err := os.ReadFile("images/05_fixed.png")
	if err != nil {
		t.Skip("no 05_fixed.png")
	}
	eng, err := onnx.NewEngine(true)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	for i := 0; i < 5; i++ {
		eng.Recognize(png)
	}

	var beforeGC, afterGC runtime.MemStats
	runtime.ReadMemStats(&beforeGC)

	const n = 100
	times := make([]int64, n)
	for i := 0; i < n; i++ {
		t0 := time.Now()
		eng.Recognize(png)
		times[i] = time.Since(t0).Microseconds()
	}

	runtime.ReadMemStats(&afterGC)

	sorted := make([]int64, n)
	copy(sorted, times)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	median := sorted[n/2]
	p90 := sorted[n*90/100]
	p99 := sorted[n*99/100]
	minT := sorted[0]
	maxT := sorted[n-1]
	spikeFactor := float64(maxT) / float64(median)

	spikes := 0
	for _, v := range times {
		if float64(v) > 1.5*float64(median) {
			spikes++
		}
	}

	gcPause := (afterGC.PauseTotalNs - beforeGC.PauseTotalNs) / 1e6
	gcCount := afterGC.NumGC - beforeGC.NumGC

	t.Logf("\n=== OCR Spike Analysis (%d iterations, 05_fixed.png 34 boxes) ===", n)
	t.Logf("  min:    %d µs (%.1f ms)", minT, float64(minT)/1000)
	t.Logf("  p50:    %d µs (%.1f ms)", median, float64(median)/1000)
	t.Logf("  p90:    %d µs (%.1f ms)", p90, float64(p90)/1000)
	t.Logf("  p99:    %d µs (%.1f ms)", p99, float64(p99)/1000)
	t.Logf("  max:    %d µs (%.1f ms)", maxT, float64(maxT)/1000)
	t.Logf("  spike factor (max/median): %.2fx", spikeFactor)
	t.Logf("  iterations >1.5x median:   %d / %d", spikes, n)
	t.Logf("  GC during run: %d cycles, %d ms total pause", gcCount, gcPause)
	t.Logf("  HeapAlloc: %d KB, HeapSys: %d KB", afterGC.HeapAlloc/1024, afterGC.HeapSys/1024)

	if spikeFactor > 3.0 {
		t.Errorf("spike factor %.2fx exceeds 3.0x threshold (max %dµs vs median %dµs)",
			spikeFactor, maxT, median)
	}
}
