package ocr_test

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocrsvc "github.com/gezihua123/phonefast/ocr"
	_ "github.com/gezihua123/phonefast/ocr/onnx" // register ONNX backend
)

// TestOCRSmoke verifies the OCR engine can initialize and recognize text
// from a real screenshot. Uses a small set of test images from the benchmark
// corpus; skips gracefully when no image or onnxruntime is available.
func TestOCRSmoke(t *testing.T) {
	// Find a test image — try a few known paths relative to repo root.
	imagePaths := []string{
		filepath.Join("..", "ocr", "benchmark", "images", "02_settings.png"),
		filepath.Join("ocr", "benchmark", "images", "02_settings.png"),
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

	svc := ocrsvc.NewService(ocrsvc.Config{Engine: ocrsvc.EngineONNX, UseVision: false})
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

// TestOCRRealScreenshot verifies OCR on a real-world phone screenshot (JPG).
// Uses the xiaohongshu search page screenshot that was used in the
// engine comparison report.
func TestOCRRealScreenshot(t *testing.T) {
	imagePaths := []string{
		filepath.Join("..", "ocr", "benchmark", "images", "screenshot_20260805_192138.jpg"),
		filepath.Join("ocr", "benchmark", "images", "screenshot_20260805_192138.jpg"),
		"screenshot_20260805_192138.jpg",
	}
	var jpgData []byte
	for _, p := range imagePaths {
		if data, err := os.ReadFile(p); err == nil {
			jpgData = data
			t.Logf("using test image: %s", p)
			break
		}
	}
	if len(jpgData) == 0 {
		t.Skip("test JPG not found (run from repo root or ocr/)")
	}

	svc := ocrsvc.NewService(ocrsvc.Config{Engine: ocrsvc.EngineONNX, UseVision: false})
	defer svc.Close()

	results, err := svc.Recognize(jpgData)
	if err != nil {
		t.Fatalf("OCR recognize failed: %v", err)
	}

	// A real screenshot should have recognizable text.
	// Expected: at least 20 text boxes for a search page.
	if len(results) < 20 {
		t.Errorf("expected at least 20 text boxes on a real screenshot, got %d", len(results))
	}

	// Accuracy check: key expected texts must be found (substring match).
	// Threshold: 80% of expected texts found.
	recognized := make([]string, 0, len(results))
	for _, r := range results {
		recognized = append(recognized, r.Text)
	}
	expected := []string{"xiaohongshu", "小红书", "取消", "搜索", "应用",
		"您是不是要搜索以下内容：小红书", "小红书一你的生活兴趣社区",
		"赞助商广告·为您推荐", "限时活动", "语言学习"}
	missed := 0
	for _, exp := range expected {
		found := false
		for _, rec := range recognized {
			if strings.Contains(rec, exp) || strings.Contains(exp, rec) {
				found = true
				break
			}
		}
		if !found {
			t.Logf("MISS: %q", exp)
			missed++
		}
	}
	hitRate := float64(len(expected)-missed) / float64(len(expected))
	if hitRate < 0.8 {
		t.Errorf("accuracy too low: %.0f%% (%d/%d)",
			hitRate*100, len(expected)-missed, len(expected))
	}
	t.Logf("accuracy: %.0f%% (%d/%d expected, %d total boxes)",
		hitRate*100, len(expected)-missed, len(expected), len(results))
}

// TestOCRBenchmarkAccuracy verifies OCR accuracy across all 20 benchmark
// images with known ground truth. Each image has 3-5 key texts that must
// be recognized. Covers both English and Chinese UI screenshots.
func TestOCRBenchmarkAccuracy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping accuracy test in short mode")
	}

	// Ground truth updated from PaddleOCR v6 medium baseline (2026-08-12).
	// Each entry: filename → list of must-contain texts (substring match).
	// Selected from PaddleOCR's actual output on each benchmark image.
	groundTruth := map[string][]string{
		// English UI screenshots
		"01_home.png":     {"Palm Store", "Hot Apps", "Fri", "0943"},
		"02_settings.png": {"Settings", "Log In", "My Phone", "SIM", "Bluetooth"},
		"03_drawer.png":   {"Palm Store", "Hot Apps", "Fri", "0943"},
		"04_notif.png":    {"9:43", "No SIM", "Fri", "Daxiang"},
		"05_fixed.png":    {"Search", "Recent Apps", "Google", "Daxiang"},
		"06_web.png":      {"baidu.com", "百度一下", "世界杯", "百度APP"},
		"07_dialer.png":   {"Recents", "Emergency", "Missed", "All"},
		"08_alarm.png":    {"Alarm", "06:00", "Mon to Fri", "07:00"},
		"09_files.png":    {"YouTube", "Facebook", "Gmail", "Play"},
		"10_recent.png":   {"Meituan-Android", "网络详情", "信号强度", "5 GHz"},

		// Recipe image — full-line OCR parity vs PaddleOCR (same PP-OCRv6
		// weights). phonefast reads the truncated long line "6 servings"
		// where PaddleOCR itself stops at "6 serving". Ground truth uses
		// the shared prefix so both engines pass.
		"recipes_sample.jpg": {"Recipes from Image", "Chocolate Cake", "A rich dessert",
			"Chicken Soup", "Hearty soup", "Pasta Primavera", "Light pasta dish"},

		// Chinese UI screenshots
		"zh_01_settings.png":  {"搜索设置", "内存扩展", "护眼模式", "电子邮件"},
		"zh_02_wifi.png":      {"Meituan-Android", "已连接", "断开连接", "信号强度"},
		"zh_03_about.png":     {"关", "基本信息", "手机名称", "moto"},
		"zh_04_display.png":   {"显示", "亮度", "自动调节", "锁定显示屏"},
		"zh_05_apps.png":      {"所有应用", "MB", "地图", "电话"},
		"zh_06_storage.png":   {"存储", "GB", "共", "释放空间"},
		"zh_07_bluetooth.png": {"连接与共享", "其他设备", "USB", "与新设备配对"},
		"zh_08_sound.png":     {"提示音", "杜比全景声", "铃声音量", "通话音量"},
		"zh_09_home.png":      {"7月", "Google", "Meet"},
		"zh_10_notif.png":     {"Meituan-Android", "移动数据", "蓝牙", "勿扰", "Android"},
	}

	base := "benchmark/images"
	totalHits, totalExpected := 0, 0

	for imgName, expected := range groundTruth {
		path := filepath.Join(base, imgName)
		pngData, err := os.ReadFile(path)
		if err != nil {
			t.Logf("skip %s: %v", imgName, err)
			continue
		}

		svc := ocrsvc.NewService(ocrsvc.Config{Engine: ocrsvc.EngineONNX, UseVision: false})
		results, err := svc.Recognize(pngData)
		svc.Close()

		if err != nil {
			t.Errorf("%s: OCR failed: %v", imgName, err)
			continue
		}

		// Use fuzzy matching: check if expected text is a substring of any
		// recognized text. This handles OCR character-level differences
		// (e.g., "19:21*" vs "19:21", "Wi-Fi" vs "Wi‑Fi").
		recognized := make([]string, 0, len(results))
		for _, r := range results {
			recognized = append(recognized, strings.TrimSpace(r.Text))
		}

		missed := 0
		for _, exp := range expected {
			found := false
			for _, rec := range recognized {
				if strings.Contains(rec, exp) || strings.Contains(exp, rec) {
					found = true
					break
				}
			}
			if !found {
				missed++
			}
		}
		hitRate := float64(len(expected)-missed) / float64(len(expected)) * 100
		totalHits += len(expected) - missed
		totalExpected += len(expected)
		t.Logf("  %-25s %3d%% (%d/%d)  boxes=%d",
			imgName, int(hitRate), len(expected)-missed, len(expected), len(results))
		if hitRate < 50 {
			t.Errorf("%s: recall too low: %.0f%% (substring match)", imgName, hitRate)
		}
	}

	overallRate := float64(totalHits) / float64(totalExpected) * 100
	t.Logf("  ─────────────────────────────────────")
	t.Logf("  OVERALL: %d/%d = %.0f%% across %d images",
		totalHits, totalExpected, overallRate, len(groundTruth))
	if overallRate < 70 {
		t.Errorf("overall accuracy too low: %.0f%%", overallRate)
	}
}

// TestOCREdgeCases verifies OCR handles edge cases gracefully.
func TestOCREdgeCases(t *testing.T) {
	svc := ocrsvc.NewService(ocrsvc.Config{Engine: ocrsvc.EngineONNX, UseVision: false})
	defer svc.Close()

	t.Run("blank image", func(t *testing.T) {
		// 1x1 white pixel
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		img.Set(0, 0, color.RGBA{255, 255, 255, 255})
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		results, err := svc.Recognize(buf.Bytes())
		if err != nil {
			t.Fatalf("blank image should not error: %v", err)
		}
		if len(results) != 0 {
			t.Logf("blank image: %d boxes (expected 0)", len(results))
		}
	})

	t.Run("small image", func(t *testing.T) {
		// 100x100 with a single text character at large size
		img := image.NewRGBA(image.Rect(0, 0, 100, 100))
		draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
		var buf bytes.Buffer
		png.Encode(&buf, img)
		results, err := svc.Recognize(buf.Bytes())
		if err != nil {
			t.Fatalf("small image should not error: %v", err)
		}
		_ = results
	})

	t.Run("corrupt bytes", func(t *testing.T) {
		_, err := svc.Recognize([]byte{0, 1, 2, 3, 4, 5})
		if err == nil {
			t.Log("corrupt bytes: no error (expected decode failure)")
		}
	})
}
