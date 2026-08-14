package ocr_test

import (
	"os"
	"sort"
	"strings"
	"testing"

	ocrsvc "github.com/gezihua123/phonefast/ocr"
	_ "github.com/gezihua123/phonefast/ocr/onnx"
)

// TestLiveHarness runs the ONNX engine on a live screenshot at /tmp/live_empty.png
// and dumps every recognized box in reading order. It exists to eyeball the
// detection-resize parity work (maxSide 1024→960) against a real screen where
// the old setting fragmented intra-word text ("Fried Rice"→"F ri ed Rice").
// Skips if the live image is absent. Skipped under -short.
func TestLiveHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live harness in short mode")
	}
	path := os.Getenv("PHONEFAST_LIVE_IMG")
	if path == "" {
		path = "/tmp/live_empty.png"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("live image not found at %s: %v", path, err)
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

	t.Logf("LIVE TOTAL: %d boxes", len(items))
	lowConf := 0
	for i, it := range items {
		flag := ""
		if it.conf < 0.7 {
			flag = "  <-- LOW CONF (likely bad box)"
			lowConf++
		}
		t.Logf("  [%2d] text=%-44q conf=%.2f center=(%4.0f,%4.0f)%s", i, it.text, it.conf, it.cx, it.cy, flag)
	}
	t.Logf("LIVE low-conf (<0.7) boxes: %d/%d", lowConf, len(items))
}
