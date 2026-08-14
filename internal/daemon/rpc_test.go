package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// newReq builds a JSON-RPC request for handler tests.
func newReq(method string, params map[string]any) *Request {
	raw, _ := json.Marshal(params)
	return &Request{JSONRPC: "2.0", Method: method, Params: raw, ID: 1}
}

// TestHandlerNilSessionNoDevice verifies the defensive nil-session guard on
// every device-dependent handler: a nil session must yield ErrNoDevice
// (never a panic, never a success).
func TestHandlerNilSessionNoDevice(t *testing.T) {
	cases := []struct {
		name string
		req  *Request
	}{
		{"screenshot", newReq("screenshot", nil)},
		{"get_ui_elements", newReq("get_ui_elements", nil)},
		{"observe", newReq("observe", nil)},
		{"tap", newReq("tap", map[string]any{"x": float64(1), "y": float64(2)})},
		{"tap_element", newReq("tap_element", nil)},
		{"swipe", newReq("swipe", map[string]any{"start_x": float64(0), "start_y": float64(0), "end_x": float64(1), "end_y": float64(1)})},
		{"type_text", newReq("type_text", map[string]any{"text": "hi"})},
		{"back", newReq("back", nil)},
		{"home", newReq("home", nil)},
		{"press_key", newReq("press_key", map[string]any{"keycode": float64(66)})},
		{"launch_app", newReq("launch_app", map[string]any{"package": "com.example"})},
		{"ocr", newReq("ocr", nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := NewDispatcher(nil).Dispatch(nil, tc.req)
			if resp.Error == nil {
				t.Fatalf("Dispatch(%s) with nil session returned no error", tc.name)
			}
			if resp.Error.Code != ErrNoDevice {
				t.Errorf("Dispatch(%s) error code = %d, want %d (ErrNoDevice)", tc.name, resp.Error.Code, ErrNoDevice)
			}
		})
	}
}

// TestHandlerParamValidation verifies required-parameter validation fires
// with ErrInvalid before any session use (nil session is fine here — the
// validation must come first).
func TestHandlerParamValidation(t *testing.T) {
	cases := []struct {
		name string
		req  *Request
	}{
		{"tap missing x", newReq("tap", map[string]any{"y": float64(2)})},
		{"tap missing y", newReq("tap", map[string]any{"x": float64(1)})},
		{"type_text empty", newReq("type_text", map[string]any{"text": ""})},
		{"type_text missing", newReq("type_text", nil)},
		{"launch_app missing", newReq("launch_app", nil)},
		{"press_key missing", newReq("press_key", nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := NewDispatcher(nil).Dispatch(nil, tc.req)
			if resp.Error == nil {
				t.Fatalf("Dispatch(%s) returned no error, want ErrInvalid", tc.name)
			}
			if resp.Error.Code != ErrInvalid {
				t.Errorf("Dispatch(%s) error code = %d, want %d (ErrInvalid)", tc.name, resp.Error.Code, ErrInvalid)
			}
		})
	}
}

// TestDispatchRouting verifies unknown-method routing and the defensive
// connect/disconnect fallbacks.
func TestDispatchRouting(t *testing.T) {
	resp := NewDispatcher(nil).Dispatch(nil, newReq("no_such_method", nil))
	if resp.Error == nil || resp.Error.Code != ErrMethod {
		t.Errorf("unknown method error = %+v, want ErrMethod", resp.Error)
	}

	for _, m := range []string{"connect", "disconnect"} {
		resp := NewDispatcher(nil).Dispatch(nil, newReq(m, nil))
		if resp.Error == nil || resp.Error.Code != ErrInternal {
			t.Errorf("%s error = %+v, want ErrInternal (defensive fallback)", m, resp.Error)
		}
	}

	// status is the only method valid without a session.
	if resp := NewDispatcher(nil).Dispatch(nil, newReq("status", nil)); resp.Error != nil {
		t.Errorf("status with nil session returned error: %v", resp.Error)
	}
}

// TestHandleStatusNilSession verifies the no-device status shape.
func TestHandleStatusNilSession(t *testing.T) {
	resp := handleStatus(nil, newReq("status", nil))
	if resp.Error != nil {
		t.Fatalf("handleStatus(nil) returned error: %v", resp.Error)
	}
	var status map[string]any
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status["connected"] != false {
		t.Errorf("status connected = %v, want false for nil session", status["connected"])
	}
	if pid, ok := status["pid"].(float64); !ok || pid <= 0 {
		t.Errorf("status pid = %v, want a positive number", status["pid"])
	}
}

// TestNewStreamedImageResponseRoundTrip serializes a streamed image response
// through the real write path and verifies the image_data field base64-
// decodes to the original payload.
func TestNewStreamedImageResponseRoundTrip(t *testing.T) {
	payload := []byte{0x00, 0x01, 0x7f, 0xfe, 0xff}
	resp := newStreamedImageResponse(7, map[string]any{
		"text":      "hello",
		"mime_type": "image/jpeg",
		"count":     3,
	}, "image_data", payload)

	client, server := net.Pipe()
	defer client.Close()
	go func() {
		writeStreamedResponse(server, resp)
		server.Close()
	}()
	data, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("read streamed response: %v", err)
	}

	var parsed struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Result  struct {
			Text      string `json:"text"`
			MimeType  string `json:"mime_type"`
			Count     int    `json:"count"`
			ImageData string `json:"image_data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal streamed response: %v\n%s", err, data)
	}
	if parsed.JSONRPC != "2.0" || parsed.ID != 7 {
		t.Errorf("envelope = jsonrpc %q id %d, want jsonrpc 2.0 id 7", parsed.JSONRPC, parsed.ID)
	}
	if parsed.Result.Text != "hello" || parsed.Result.MimeType != "image/jpeg" || parsed.Result.Count != 3 {
		t.Errorf("non-image fields = %+v, want text=hello mime=image/jpeg count=3", parsed.Result)
	}
	decoded, err := base64.StdEncoding.DecodeString(parsed.Result.ImageData)
	if err != nil {
		t.Fatalf("decode image_data: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Errorf("decoded image_data = %v, want %v", decoded, payload)
	}
}

// TestHandleTapWithFakeDevice proves the Device seam: a handler runs against
// a fake device (no ADB/scrcpy), asserting the full tap flow — coordinate
// scaling + tap call — is exercised without a physical device.
func TestHandleTapWithFakeDevice(t *testing.T) {
	dev := newFakeDevice("fake-serial")
	dev.tapErr = nil

	resp := handleTap(dev, newReq("tap", map[string]any{"x": float64(1), "y": float64(2)}))
	if resp.Error != nil {
		t.Fatalf("tap on fake device returned error: %v", resp.Error.Message)
	}
	// Fake has DeviceW==DeviceH==0 via the zero session, so ScaleToDevice(1,2)
	// computes 0,0 — the assertion is that the flow ran, not the math.
	if string(resp.Result) == "" {
		t.Fatal("expected non-empty result for successful tap")
	}

	// Error propagation: a failing device surfaces the tap error.
	dev.tapErr = errors.New("device gone")
	resp = handleTap(dev, newReq("tap", map[string]any{"x": float64(1), "y": float64(2)}))
	if resp.Error == nil || resp.Error.Code != ErrDevice {
		t.Fatalf("expected ErrDevice response, got %+v", resp.Error)
	}
}

// TestHandleGetUIElementsHierarchical covers the hierarchical-format branch
// (format.ByName != nil): the handler must route through GetUIFull, format
// the tree, and return elements/formatted/count/format.
func TestHandleGetUIElementsHierarchical(t *testing.T) {
	dev := newFakeDevice("fake-serial")
	dev.getUIFullFn = func(n int) ([]protocol.UIFullElement, error) {
		return []protocol.UIFullElement{{ID: 1, Text: "Settings", ClassName: "TextView"}}, nil
	}

	resp := handleGetUIElements(dev, newReq(protocol.MethodGetUIElements, map[string]any{
		"format":       "flatref",
		"max_elements": float64(100),
	}))
	if resp.Error != nil {
		t.Fatalf("hierarchical get_ui_elements returned error: %v", resp.Error.Message)
	}
	var m map[string]any
	if err := json.Unmarshal(resp.Result, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if m["format"] != "flatref" {
		t.Errorf("format = %v, want flatref", m["format"])
	}
	if m["count"] != float64(1) {
		t.Errorf("count = %v, want 1", m["count"])
	}
	formatted, _ := m["formatted"].(string)
	if formatted == "" || !containsStr(formatted, "Settings") {
		t.Errorf("formatted missing rendered element, got %q", formatted)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
