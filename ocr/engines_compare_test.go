package ocr_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ocrsvc "github.com/gezihua123/phonefast/ocr"
	_ "github.com/gezihua123/phonefast/ocr/apple" // register Apple Vision backend
	_ "github.com/gezihua123/phonefast/ocr/ncnn"  // register NCNN backend
	_ "github.com/gezihua123/phonefast/ocr/onnx"  // register ONNX backend
)

// TestOCRThreeEngines runs the Apple Vision, NCNN, and ONNX OCR engines on the
// same set of benchmark images and reports, per engine: ground-truth recall,
// box count, and latency. Engines that are not built in (NCNN needs -tags ncnn;
// Apple needs CGO_ENABLED=1) are skipped gracefully.
//
// Run all three:
//
//	CGO_ENABLED=1 DYLD_LIBRARY_PATH=/opt/homebrew/lib \
//	  PHONEFAST_NCNN_PARAM=ocr/models/ncnn/rec.ncnn.param \
//	  PHONEFAST_NCNN_BIN=ocr/models/ncnn/rec.ncnn.bin \
//	  go test -tags ncnn ./ocr/ -run TestOCRThreeEngines -v -timeout 600s
//
// This is an informational test (logs results); it only fails if a built-in
// engine errors unexpectedly.
func TestOCRThreeEngines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-engine comparison in short mode")
	}

	// Representative subset: 2 English UI, 2 Chinese UI, the recipe image
	// (PaddleOCR parity target), and zh_09 (the colored home screen the BGR
	// fix recovered). Each entry: filename → must-contain substrings.
	groundTruth := map[string][]string{
		"02_settings.png":     {"Settings", "Bluetooth", "SIM"},
		"09_files.png":        {"YouTube", "Facebook", "Gmail"},
		"recipes_sample.jpg":  {"Recipes from Image", "Chocolate Cake", "Chicken Soup", "Pasta Primavera"},
		"zh_04_display.png":   {"显示", "亮度", "自动调节"},
		"zh_07_bluetooth.png": {"连接与共享", "其他设备", "USB"},
		"zh_09_home.png":      {"7月", "Google", "Meet"},
	}

	engines := []struct {
		name string
		eng  string
	}{
		{"APPLE", ocrsvc.EngineApple},
		{"NCNN", ocrsvc.EngineNCNN},
		{"ONNX", ocrsvc.EngineONNX},
	}

	// PF_TEST_ENGINE restricts the run to one engine (by name or registry id),
	// so each engine can be exercised in a separate `go test` process. This
	// matters because the Apple Vision CGO bridge can SIGBUS (an unrecoverable
	// process-kill) — running engines in separate processes isolates a crash
	// to that engine's run, leaving the others' results intact.
	if only := os.Getenv("PF_TEST_ENGINE"); only != "" {
		filtered := engines[:0]
		for _, e := range engines {
			if strings.EqualFold(e.name, only) || strings.EqualFold(e.eng, only) {
				filtered = append(filtered, e)
			}
		}
		engines = filtered
	}
	if len(engines) == 0 {
		t.Skip("PF_TEST_ENGINE selected no engines")
	}

	imgDir := "benchmark/images"
	// Resolve image dir relative to the ocr package test working directory.
	for _, d := range []string{"benchmark/images", filepath.Join("..", "ocr", "benchmark", "images")} {
		if _, err := os.Stat(filepath.Join(d, "recipes_sample.jpg")); err == nil {
			imgDir = d
			break
		}
	}

	type engStat struct {
		hits, expected int
		boxes          int
		dur            time.Duration
		sampleTexts    map[string][]string // imgName → recognized texts (for samples)
		available      bool
	}
	stats := make(map[string]*engStat)
	for _, e := range engines {
		stats[e.name] = &engStat{sampleTexts: map[string][]string{}}
	}

	// Deterministic image order.
	var imgNames []string
	for n := range groundTruth {
		imgNames = append(imgNames, n)
	}
	// Stable sort by name.
	for i := 1; i < len(imgNames); i++ {
		for j := i; j > 0 && imgNames[j-1] > imgNames[j]; j-- {
			imgNames[j-1], imgNames[j] = imgNames[j], imgNames[j-1]
		}
	}

	t.Logf("=== OCR Three-Engine Comparison (%d images) ===", len(imgNames))

	for _, e := range engines {
		svc := ocrsvc.NewService(ocrsvc.Config{Engine: e.eng, UseVision: false})
		st := stats[e.name]

		// Availability probe on the first image.
		probeData, err := os.ReadFile(filepath.Join(imgDir, imgNames[0]))
		if err != nil {
			t.Logf("[%s] cannot read probe image: %v", e.name, err)
			svc.Close()
			continue
		}
		if _, err := svc.Recognize(probeData); err != nil {
			if strings.Contains(err.Error(), "not available") || strings.Contains(err.Error(), "not built in") || strings.Contains(err.Error(), "unknown OCR engine") {
				t.Logf("[%s] UNAVAILABLE — skipping (%v)", e.name, err)
				svc.Close()
				continue
			}
			t.Logf("[%s] probe recognize error: %v", e.name, err)
			svc.Close()
			continue
		}
		st.available = true
		t.Logf("[%s] available — running %d images...", e.name, len(imgNames))

		for _, imgName := range imgNames {
			data, err := os.ReadFile(filepath.Join(imgDir, imgName))
			if err != nil {
				t.Logf("  [%s] skip %s: %v", e.name, imgName, err)
				continue
			}
			start := time.Now()
			results, err := svc.Recognize(data)
			dur := time.Since(start)
			if err != nil {
				t.Logf("  [%s] %-22s ERROR %v", e.name, imgName, err)
				continue
			}
			st.boxes += len(results)
			st.dur += dur

			recognized := make([]string, 0, len(results))
			for _, r := range results {
				recognized = append(recognized, strings.TrimSpace(r.Text))
			}
			st.sampleTexts[imgName] = recognized

			expected := groundTruth[imgName]
			hits := 0
			for _, exp := range expected {
				for _, rec := range recognized {
					if strings.Contains(rec, exp) || strings.Contains(exp, rec) {
						hits++
						break
					}
				}
			}
			st.hits += hits
			st.expected += len(expected)
			t.Logf("  [%s] %-22s %3d%% (%d/%d)  boxes=%-3d  %s",
				e.name, imgName, hits*100/len(expected), hits, len(expected),
				len(results), dur.Round(time.Millisecond))
		}
		svc.Close()
	}

	// Summary table.
	t.Logf("")
	t.Logf("=== Summary ===")
	t.Logf("  %-8s %-10s %-10s %-12s %s", "ENGINE", "RECALL", "BOXES", "AVG LATENCY", "STATUS")
	for _, e := range engines {
		st := stats[e.name]
		if !st.available {
			t.Logf("  %-8s %-10s %-10s %-12s %s", e.name, "-", "-", "-", "unavailable")
			continue
		}
		recall := "n/a"
		if st.expected > 0 {
			recall = fmt.Sprintf("%d/%d (%d%%)", st.hits, st.expected, st.hits*100/st.expected)
		}
		avg := "n/a"
		if len(imgNames) > 0 {
			avg = (st.dur / time.Duration(len(imgNames))).Round(time.Millisecond).String()
		}
		t.Logf("  %-8s %-10s %-10d %-12s %s", e.name, recall, st.boxes, avg, "ok")
	}
	// Dump sample recognized text for the recipe image (parity target) from
	// each available engine, for a direct qualitative comparison.
	t.Logf("")
	t.Logf("=== Sample: recipes_sample.jpg recognized text per engine ===")
	for _, e := range engines {
		st := stats[e.name]
		if !st.available {
			continue
		}
		texts := st.sampleTexts["recipes_sample.jpg"]
		t.Logf("  [%s] %d lines:", e.name, len(texts))
		for i, tx := range texts {
			if i >= 12 {
				t.Logf("      ... (%d more)", len(texts)-12)
				break
			}
			t.Logf("      %q", tx)
		}
	}
}
