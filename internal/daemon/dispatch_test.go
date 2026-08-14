package daemon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gezihua123/phonefast/pkg/avcodec"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// TestDispatchResultTap verifies the CLI direct-mode happy path: a tap
// dispatches to the device and the {"message":...} result comes back as
// plain result JSON (no JSON-RPC envelope).
func TestDispatchResultTap(t *testing.T) {
	dev := newFakeDevice("fake")
	dev.tapFn = func(x, y int) error { return nil }

	d := NewDispatcher(nil)
	raw, err := d.DispatchResult(dev, protocol.MethodTap, map[string]any{"x": 10, "y": 20})
	if err != nil {
		t.Fatalf("DispatchResult: %v", err)
	}
	var res struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("result is not result JSON: %v (%s)", err, raw)
	}
	if res.Message != "Tapped at (10, 20)" {
		t.Errorf("message = %q, want %q", res.Message, "Tapped at (10, 20)")
	}
}

// TestDispatchResultHandlerError verifies an error response is converted to
// a plain error carrying the response message text (what the CLI prints in
// direct mode).
func TestDispatchResultHandlerError(t *testing.T) {
	dev := newFakeDevice("fake")
	dev.tapErr = errors.New("tap boom")

	d := NewDispatcher(nil)
	_, err := d.DispatchResult(dev, protocol.MethodTap, map[string]any{"x": 10, "y": 20})
	if err == nil || !strings.Contains(err.Error(), "tap boom") {
		t.Fatalf("err = %v, want plain error containing 'tap boom'", err)
	}
}

// TestDispatchResultScreenshotStreamsBase64 exercises the streaming branch:
// a screenshot response's streamPayload must be materialized into the full
// result object with the base64 image_data field — identical to what the
// wire path writes, minus the JSON-RPC envelope.
func TestDispatchResultScreenshotStreamsBase64(t *testing.T) {
	payload := []byte("fake-jpeg-bytes")
	dev := newFakeDevice("fake")
	dev.screenshotFormatFn = func(format avcodec.ImageFormat) ([]byte, int, int, string, error) {
		return payload, 100, 200, "image/jpeg", nil
	}

	d := NewDispatcher(nil)
	raw, err := d.DispatchResult(dev, protocol.MethodScreenshot, nil)
	if err != nil {
		t.Fatalf("DispatchResult: %v", err)
	}
	var res struct {
		Text      string `json:"text"`
		MimeType  string `json:"mime_type"`
		ImageData string `json:"image_data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("streamed result is not result JSON: %v (%s)", err, raw)
	}
	if res.MimeType != "image/jpeg" {
		t.Errorf("mime_type = %q, want image/jpeg", res.MimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(res.ImageData)
	if err != nil {
		t.Fatalf("image_data is not valid base64: %v", err)
	}
	if string(decoded) != string(payload) {
		t.Errorf("decoded image_data = %q, want %q", decoded, payload)
	}
}

// TestDispatchResultUnknownMethod verifies an unknown method surfaces a plain
// error (never a nil-error nil-result pair the CLI would misrender).
func TestDispatchResultUnknownMethod(t *testing.T) {
	d := NewDispatcher(nil)
	_, err := d.DispatchResult(newFakeDevice("fake"), "no_such_method", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("err = %v, want 'unknown method' error", err)
	}
}
