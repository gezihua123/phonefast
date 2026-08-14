package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gezihua123/phonefast/internal/session"
	"github.com/gezihua123/phonefast/pkg/avcodec"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// TestHandleGetUIElementsFallbackADB verifies the fast→ADB fallback chain:
// GetUISummary error → GetUIElementsFallbackADB succeeds (result from the
// fallback), and both failing yields ErrDevice.
func TestHandleGetUIElementsFallbackADB(t *testing.T) {
	fallbackCalled := 0
	dev := newFakeDevice("dev-A")
	dev.getUISummaryFn = func(int) ([]protocol.UIElement, error) {
		return nil, errors.New("ui socket down")
	}
	dev.uiFallbackADBFn = func(int) ([]protocol.UIElement, error) {
		fallbackCalled++
		return []protocol.UIElement{{Index: 0, Text: "from-adb"}}, nil
	}

	resp := handleGetUIElements(dev, newReq("get_ui_elements", nil))
	if resp.Error != nil {
		t.Fatalf("get_ui_elements error: %v", resp.Error)
	}
	if fallbackCalled != 1 {
		t.Errorf("fallback called %d times, want 1", fallbackCalled)
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if n, _ := out["count"].(float64); n != 1 {
		t.Errorf("count = %v, want 1 (fallback elements)", out["count"])
	}

	// Both fail → ErrDevice.
	dev.getUISummaryFn = func(int) ([]protocol.UIElement, error) { return nil, errors.New("fast down") }
	dev.uiFallbackADBFn = func(int) ([]protocol.UIElement, error) { return nil, errors.New("adb down") }
	resp = handleGetUIElements(dev, newReq("get_ui_elements", nil))
	if resp.Error == nil || resp.Error.Code != ErrDevice {
		t.Fatalf("both-fail error = %+v, want ErrDevice", resp.Error)
	}
}

// TestHandleObserveFullTimeoutMapsToErrTimeout verifies the task-flagged
// mapping: session.ErrObserveTimeout → RPC ErrTimeout.
func TestHandleObserveFullTimeoutMapsToErrTimeout(t *testing.T) {
	dev := newFakeDevice("dev-A")
	dev.observeFullFn = func(int, avcodec.ImageFormat) (*session.ObserveFullResult, error) {
		return nil, session.ErrObserveTimeout
	}

	resp := handleObserve(dev, newReq("observe", map[string]any{"format": "jsonl"}))
	if resp.Error == nil || resp.Error.Code != ErrTimeout {
		t.Fatalf("observe timeout error = %+v, want ErrTimeout", resp.Error)
	}
}

// TestHandleObserveFullPNGConvertFallback verifies the PNG→JPEG conversion
// failure path: the response still carries image/jpeg mime and falls back
// to the original bytes.
func TestHandleObserveFullPNGConvertFallback(t *testing.T) {
	original := []byte("not-a-real-png")
	dev := newFakeDevice("dev-A")
	dev.observeFullFn = func(int, avcodec.ImageFormat) (*session.ObserveFullResult, error) {
		return &session.ObserveFullResult{Image: original, Mime: "image/png", Elements: nil}, nil
	}

	resp := handleObserve(dev, newReq("observe", map[string]any{"format": "jsonl"}))
	if resp.Error != nil {
		t.Fatalf("observe error: %v", resp.Error)
	}
	if !bytes.Equal(resp.streamPayload, original) {
		t.Errorf("payload = %q, want original bytes %q (conversion failure must fall back)", resp.streamPayload, original)
	}
	if !bytes.Contains(resp.streamPrefix, []byte(`"mime_type":"image/jpeg"`)) {
		t.Errorf("streamPrefix = %q, want mime_type image/jpeg", resp.streamPrefix)
	}
}

// TestHandleObserveFullJPEGPassthrough verifies a JPEG result is streamed
// as-is (no conversion attempted).
func TestHandleObserveFullJPEGPassthrough(t *testing.T) {
	jpegBytes := []byte("already-jpeg")
	dev := newFakeDevice("dev-A")
	dev.observeFullFn = func(int, avcodec.ImageFormat) (*session.ObserveFullResult, error) {
		return &session.ObserveFullResult{Image: jpegBytes, Mime: "image/jpeg", Elements: nil}, nil
	}

	resp := handleObserve(dev, newReq("observe", map[string]any{"format": "jsonl"}))
	if resp.Error != nil {
		t.Fatalf("observe error: %v", resp.Error)
	}
	if !bytes.Equal(resp.streamPayload, jpegBytes) {
		t.Errorf("payload = %q, want original JPEG bytes unchanged", resp.streamPayload)
	}
}

// TestHandleObserveFlatPath verifies the legacy flat branch (reached only
// via an explicit UNKNOWN format — the empty format defaults to "flatref"):
// dev.Observe's PNG result is streamed with image/png mime, and its error
// surfaces as ErrDevice.
func TestHandleObserveFlatPath(t *testing.T) {
	dev := newFakeDevice("dev-A")
	elements := []protocol.UIElement{{Index: 1, Text: "Settings"}}
	dev.observeFn = func(int, bool) ([]byte, []protocol.UIElement, error) {
		return []byte("flat-png"), elements, nil
	}

	req := newReq("observe", map[string]any{"format": "unknownfmt"})
	resp := handleObserve(dev, req)
	if resp.Error != nil {
		t.Fatalf("observe flat error: %v", resp.Error)
	}
	if !bytes.Equal(resp.streamPayload, []byte("flat-png")) {
		t.Errorf("payload = %q, want flat-png", resp.streamPayload)
	}
	if !bytes.Contains(resp.streamPrefix, []byte(`"mime_type":"image/png"`)) {
		t.Errorf("streamPrefix = %q, want mime_type image/png", resp.streamPrefix)
	}

	dev.observeFn = func(int, bool) ([]byte, []protocol.UIElement, error) {
		return nil, nil, errors.New("observe failed")
	}
	resp = handleObserve(dev, req)
	if resp.Error == nil || resp.Error.Code != ErrDevice {
		t.Fatalf("observe flat error = %+v, want ErrDevice", resp.Error)
	}
}

// TestHandleScreenshotMimeBranches verifies the JPEG passthrough and the
// PNG convert-failure fallback in handleScreenshot.
func TestHandleScreenshotMimeBranches(t *testing.T) {
	dev := newFakeDevice("dev-A")

	// JPEG passthrough.
	dev.screenshotFormatFn = func(avcodec.ImageFormat) ([]byte, int, int, string, error) {
		return []byte("jpeg-bytes"), 640, 480, "image/jpeg", nil
	}
	resp := handleScreenshot(dev, newReq("screenshot", nil))
	if resp.Error != nil {
		t.Fatalf("screenshot jpeg error: %v", resp.Error)
	}
	if !bytes.Equal(resp.streamPayload, []byte("jpeg-bytes")) {
		t.Errorf("payload = %q, want jpeg-bytes unchanged", resp.streamPayload)
	}

	// PNG with unconvertible bytes → fallback to original.
	dev.screenshotFormatFn = func(avcodec.ImageFormat) ([]byte, int, int, string, error) {
		return []byte("png-garbage"), 640, 480, "image/png", nil
	}
	resp = handleScreenshot(dev, newReq("screenshot", nil))
	if resp.Error != nil {
		t.Fatalf("screenshot png error: %v", resp.Error)
	}
	if !bytes.Equal(resp.streamPayload, []byte("png-garbage")) {
		t.Errorf("payload = %q, want original bytes after conversion failure", resp.streamPayload)
	}
	if !bytes.Contains(resp.streamPrefix, []byte(`"mime_type":"image/jpeg"`)) {
		t.Errorf("streamPrefix = %q, want mime_type image/jpeg", resp.streamPrefix)
	}
}

// newTapElementDevice builds a fake device with two UI elements and a
// tap recorder.
func newTapElementDevice() (*fakeDevice, *[]int) {
	dev := newFakeDevice("dev-A")
	var taps []int
	dev.getUISummaryFn = func(int) ([]protocol.UIElement, error) {
		return []protocol.UIElement{
			{Index: 5, Text: "Settings", Center: [2]int{100, 200}},
			{Index: 6, Text: "Wi-Fi", ContentDesc: "wifi icon", Center: [2]int{300, 400}},
		}, nil
	}
	dev.tapFn = func(x, y int) error {
		taps = append(taps, x, y)
		return nil
	}
	return dev, &taps
}

// TestHandleTapElementSearchPaths verifies index lookup, case-insensitive
// text lookup, content-desc lookup, and the not-found / empty-elements
// error branches.
func TestHandleTapElementSearchPaths(t *testing.T) {
	t.Run("index found", func(t *testing.T) {
		dev, taps := newTapElementDevice()
		resp := handleTapElement(dev, newReq("tap_element", map[string]any{"index": float64(5)}))
		if resp.Error != nil {
			t.Fatalf("tap_element error: %v", resp.Error)
		}
		if len(*taps) != 2 || (*taps)[0] != 100 || (*taps)[1] != 200 {
			t.Errorf("tapped at %v, want [100 200]", *taps)
		}
	})

	t.Run("index not found", func(t *testing.T) {
		dev, _ := newTapElementDevice()
		resp := handleTapElement(dev, newReq("tap_element", map[string]any{"index": float64(99)}))
		if resp.Error == nil || resp.Error.Code != ErrInvalid {
			t.Fatalf("not-found error = %+v, want ErrInvalid", resp.Error)
		}
	})

	t.Run("text case-insensitive", func(t *testing.T) {
		dev, taps := newTapElementDevice()
		resp := handleTapElement(dev, newReq("tap_element", map[string]any{"text": "settings"}))
		if resp.Error != nil {
			t.Fatalf("tap_element error: %v", resp.Error)
		}
		if len(*taps) != 2 || (*taps)[0] != 100 || (*taps)[1] != 200 {
			t.Errorf("tapped at %v, want [100 200]", *taps)
		}
	})

	t.Run("text via content desc", func(t *testing.T) {
		dev, taps := newTapElementDevice()
		resp := handleTapElement(dev, newReq("tap_element", map[string]any{"text": "wifi icon"}))
		if resp.Error != nil {
			t.Fatalf("tap_element error: %v", resp.Error)
		}
		if len(*taps) != 2 || (*taps)[0] != 300 || (*taps)[1] != 400 {
			t.Errorf("tapped at %v, want [300 400]", *taps)
		}
	})

	t.Run("text not found", func(t *testing.T) {
		dev, _ := newTapElementDevice()
		resp := handleTapElement(dev, newReq("tap_element", map[string]any{"text": "nonexistent"}))
		if resp.Error == nil || resp.Error.Code != ErrInvalid {
			t.Fatalf("not-found error = %+v, want ErrInvalid", resp.Error)
		}
	})

	t.Run("empty elements", func(t *testing.T) {
		dev := newFakeDevice("dev-A")
		dev.getUISummaryFn = func(int) ([]protocol.UIElement, error) { return nil, nil }
		resp := handleTapElement(dev, newReq("tap_element", map[string]any{"index": float64(0)}))
		if resp.Error == nil || resp.Error.Code != ErrDevice {
			t.Fatalf("empty-elements error = %+v, want ErrDevice", resp.Error)
		}
	})

	t.Run("ui dump failure", func(t *testing.T) {
		dev := newFakeDevice("dev-A")
		dev.getUISummaryFn = func(int) ([]protocol.UIElement, error) { return nil, errors.New("fast down") }
		dev.uiFallbackADBFn = func(int) ([]protocol.UIElement, error) { return nil, errors.New("adb down") }
		resp := handleTapElement(dev, newReq("tap_element", map[string]any{"index": float64(0)}))
		if resp.Error == nil || resp.Error.Code != ErrDevice {
			t.Fatalf("ui-failure error = %+v, want ErrDevice", resp.Error)
		}
	})

	t.Run("no selector", func(t *testing.T) {
		dev, _ := newTapElementDevice()
		resp := handleTapElement(dev, newReq("tap_element", nil))
		if resp.Error == nil || resp.Error.Code != ErrInvalid {
			t.Fatalf("no-selector error = %+v, want ErrInvalid", resp.Error)
		}
	})
}

// TestHandleOCRNoService verifies the nil-OCR-service branch maps to
// ErrInternal.
func TestHandleOCRNoService(t *testing.T) {
	dev := newFakeDevice("dev-A")
	resp := NewDispatcher(nil).handleOCR(dev, newReq("ocr", nil))
	if resp.Error == nil || resp.Error.Code != ErrInternal {
		t.Fatalf("nil-service ocr error = %+v, want ErrInternal", resp.Error)
	}
}

// TestHandleSwipeSuccess verifies the swipe handler response on a fake
// device.
func TestHandleSwipeSuccess(t *testing.T) {
	dev := newFakeDevice("dev-A")
	var got [5]int
	dev.swipeFn = func(x1, y1, x2, y2, d int) error {
		got = [5]int{x1, y1, x2, y2, d}
		return nil
	}
	resp := handleSwipe(dev, newReq("swipe", map[string]any{
		"start_x": float64(0), "start_y": float64(10),
		"end_x": float64(100), "end_y": float64(110),
	}))
	if resp.Error != nil {
		t.Fatalf("swipe error: %v", resp.Error)
	}
	// duration defaults to 500 when omitted; scale is identity on the fake.
	if got != [5]int{0, 10, 100, 110, 500} {
		t.Errorf("swipe args = %v, want [0 10 100 110 500]", got)
	}
}

// TestHandlePressKeySuccess verifies both the keycode and key-name paths.
func TestHandlePressKeySuccess(t *testing.T) {
	dev := newFakeDevice("dev-A")
	got := 0
	dev.pressKeyFn = func(k int) error { got = k; return nil }

	resp := handlePressKey(dev, newReq("press_key", map[string]any{"keycode": float64(66)}))
	if resp.Error != nil {
		t.Fatalf("press_key keycode error: %v", resp.Error)
	}
	if got != 66 {
		t.Errorf("pressKey called with %d, want 66", got)
	}

	resp = handlePressKey(dev, newReq("press_key", map[string]any{"key": "ENTER"}))
	if resp.Error != nil {
		t.Fatalf("press_key name error: %v", resp.Error)
	}
	if got != int(protocol.KeycodeEnter) {
		t.Errorf("pressKey called with %d, want ENTER (%d)", got, protocol.KeycodeEnter)
	}
}

// TestHandleLaunchAppSuccess verifies the launch-app handler response.
func TestHandleLaunchAppSuccess(t *testing.T) {
	dev := newFakeDevice("dev-A")
	launched := ""
	dev.launchAppFn = func(p string) error { launched = p; return nil }

	resp := handleLaunchApp(dev, newReq("launch_app", map[string]any{"package": "com.example.app"}))
	if resp.Error != nil {
		t.Fatalf("launch_app error: %v", resp.Error)
	}
	if launched != "com.example.app" {
		t.Errorf("launched = %q, want com.example.app", launched)
	}
}

// TestHandleStatusWithDevice verifies the device-dependent status branch.
func TestHandleStatusWithDevice(t *testing.T) {
	dev := newFakeDevice("dev-A")
	dev.isAliveFn = func() bool { return true }

	resp := handleStatus(dev, newReq("status", nil))
	if resp.Error != nil {
		t.Fatalf("status error: %v", resp.Error)
	}
	var status map[string]any
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status["connected"] != true {
		t.Errorf("connected = %v, want true", status["connected"])
	}
	if status["serial"] != "dev-A" {
		t.Errorf("serial = %v, want dev-A", status["serial"])
	}
}
