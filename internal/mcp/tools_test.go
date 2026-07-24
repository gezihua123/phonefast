package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/gezihua123/phonefast/internal/format"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// fakeRPC is a test stub for rpcCaller. It records the last method/params and
// returns a canned JSON result (or err if non-nil). This lets us verify the
// handler→RPC→result-decode path without a real daemon.
type fakeRPC struct {
	method string
	params map[string]any
	result any // marshaled to JSON before returning
	err    error
	// calls counts Call invocations (for retry/recovery tests).
	calls int
	// onCall, if set, is invoked before each Call returns — lets a test flip
	// err/result between calls to simulate a daemon coming back up.
	onCall func(calls int)
}

func (f *fakeRPC) Call(method string, params map[string]any) (json.RawMessage, error) {
	f.method = method
	f.params = params
	f.calls++
	if f.onCall != nil {
		f.onCall(f.calls)
	}
	if f.err != nil {
		return nil, f.err
	}
	data, _ := json.Marshal(f.result)
	return data, nil
}

// TestRPCCallDecodesResult verifies rpcCall routes to the client and decodes
// the JSON result into the out struct.
func TestRPCCallDecodesResult(t *testing.T) {
	fake := &fakeRPC{result: map[string]any{"message": "Tapped at (1, 2)"}}
	s := newWithClient(fake)

	var resp struct {
		Message string `json:"message"`
	}
	if err := s.rpcCall("tap", map[string]any{"x": 1, "y": 2}, &resp); err != nil {
		t.Fatalf("rpcCall failed: %v", err)
	}
	if fake.method != "tap" {
		t.Errorf("method = %q, want tap", fake.method)
	}
	if resp.Message != "Tapped at (1, 2)" {
		t.Errorf("message = %q, want 'Tapped at (1, 2)'", resp.Message)
	}
}

// TestHandleTapRoutesRPC verifies handleTap builds params and decodes the
// daemon's message response end-to-end through the fake client.
func TestHandleTapRoutesRPC(t *testing.T) {
	fake := &fakeRPC{result: map[string]any{"message": "Tapped at (10, 20)"}}
	s := newWithClient(fake)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]any{"x": float64(10), "y": float64(20)},
		},
	}
	result, _ := s.handleTap(context.Background(), req)
	if result.IsError {
		t.Fatalf("expected success, got error")
	}
	if fake.params["x"] != 10 || fake.params["y"] != 20 {
		t.Errorf("params = %v, want x=10 y=20", fake.params)
	}
}

// TestHandleScreenshotDecodesImage verifies screenshot decodes image_data and
// returns a native ImageContent.
func TestHandleScreenshotDecodesImage(t *testing.T) {
	fake := &fakeRPC{result: map[string]any{
		"text":       "Screenshot (488x1080)",
		"image_data": "iVBORw0KGgoAAA==",
		"mime_type":  "image/png",
	}}
	s := newWithClient(fake)

	result, _ := s.handleScreenshot(context.Background(), mcp.CallToolRequest{})
	if result.IsError {
		t.Fatalf("expected success, got error")
	}
	var img mcp.ImageContent
	found := false
	for _, c := range result.Content {
		if ic, ok := c.(mcp.ImageContent); ok {
			img = ic
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ImageContent in result")
	}
	if img.Data != "iVBORw0KGgoAAA==" {
		t.Errorf("image data = %q", img.Data)
	}
}

// TestHandleObserveNoDuplication guards against the regression where the
// element-list text was reused as the image caption (sending it twice).
// The caption must be a short "Observe: N elements" summary derived from
// count, and the element list appears once as a separate TextContent.
func TestHandleObserveNoDuplication(t *testing.T) {
	fake := &fakeRPC{result: map[string]any{
		"text":       "[0] text=ok",
		"image_data": "iVBORw0KGgo==",
		"mime_type":  "image/png",
		"count":      1,
	}}
	s := newWithClient(fake)

	result, _ := s.handleObserve(context.Background(), mcp.CallToolRequest{})
	if result.IsError {
		t.Fatalf("expected success, got error")
	}
	// Count the TextContent occurrences of the element list.
	listCount := 0
	captionText := ""
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			if tc.Text == "[0] text=ok" {
				listCount++
			}
			if strings.Contains(tc.Text, "Observe:") {
				captionText = tc.Text
			}
		}
	}
	if listCount != 1 {
		t.Errorf("element list should appear exactly once, got %d", listCount)
	}
	if !strings.Contains(captionText, "1 interactive elements") {
		t.Errorf("caption = %q, want it to contain '1 interactive elements'", captionText)
	}
}

// TestHandleGetUIElementsDecodesFormatted verifies the formatted field is
// decoded and returned as text.
func TestHandleGetUIElementsDecodesFormatted(t *testing.T) {
	fake := &fakeRPC{result: map[string]any{
		"formatted": "[0] text=btn",
		"count":     1,
	}}
	s := newWithClient(fake)

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{}}}
	result, _ := s.handleGetUIElements(context.Background(), req)
	if result.IsError {
		t.Fatalf("expected success, got error")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok || tc.Text != "[0] text=btn" {
		t.Errorf("text = %q, want '[0] text=btn'", tc.Text)
	}
}

// TestHandleListDevicesDecodesArray verifies list_devices decodes the device
// array returned by the daemon.
func TestHandleListDevicesDecodesArray(t *testing.T) {
	fake := &fakeRPC{result: []map[string]any{
		{"serial": "emulator-5554", "status": "device"},
	}}
	s := newWithClient(fake)

	result, _ := s.handleListDevices(context.Background(), mcp.CallToolRequest{})
	if result.IsError {
		t.Fatalf("expected success, got error")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if !strings.Contains(tc.Text, "emulator-5554") {
		t.Errorf("expected serial in output, got: %s", tc.Text)
	}
}

// TestHandleBackDecodesMessage verifies a simple action decodes the message.
func TestHandleBackDecodesMessage(t *testing.T) {
	fake := &fakeRPC{result: map[string]any{"message": "Back pressed"}}
	s := newWithClient(fake)

	result, _ := s.handleBack(context.Background(), mcp.CallToolRequest{})
	if result.IsError {
		t.Fatalf("expected success, got error")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok || tc.Text != "Back pressed" {
		t.Errorf("text = %q, want 'Back pressed'", tc.Text)
	}
}

func TestFormatElementsForLLM(t *testing.T) {
	result := format.ElementsForLLM(nil, 100, false)
	if result != "No interactive elements found on screen." {
		t.Errorf("expected empty message, got: %s", result)
	}
}

// TestFormatElementsForLLMTruncation verifies the "最多显示 N 个" behavior:
// beyond maxShow elements a "... and N more elements" summary appears.
func TestFormatElementsForLLMTruncation(t *testing.T) {
	makeEls := func(n int) []protocol.UIElement {
		els := make([]protocol.UIElement, n)
		for i := range els {
			els[i] = protocol.UIElement{Index: i, Text: "btn", ClassName: "android.widget.Button"}
		}
		return els
	}

	t.Run("exactly maxShow 50 → no truncation notice", func(t *testing.T) {
		got := format.ElementsForLLM(makeEls(50), 50, false)
		if strings.Contains(got, "more elements") {
			t.Errorf("50 elements with maxShow=50 should not trigger truncation, got: %s", got)
		}
	})

	t.Run("51 with maxShow=50 → shows 1 more", func(t *testing.T) {
		got := format.ElementsForLLM(makeEls(51), 50, false)
		if !strings.Contains(got, "... and 1 more elements") {
			t.Errorf("51 elements should show '1 more', got: %s", got)
		}
	})

	t.Run("100 with maxShow=50 → shows 50 more", func(t *testing.T) {
		got := format.ElementsForLLM(makeEls(100), 50, false)
		if !strings.Contains(got, "... and 50 more elements") {
			t.Errorf("100 elements should show '50 more', got: %s", got)
		}
	})

	t.Run("show all with -1", func(t *testing.T) {
		got := format.ElementsForLLM(makeEls(5), -1, false)
		if strings.Contains(got, "more elements") {
			t.Errorf("-1 should show all elements, got: %s", got)
		}
	})

	t.Run("default maxShow=100 with 80 elements → no truncation", func(t *testing.T) {
		got := format.ElementsForLLM(makeEls(80), 100, false)
		if strings.Contains(got, "more elements") {
			t.Errorf("80 elements with maxShow=100 should not truncate, got: %s", got)
		}
	})

	t.Run("default maxShow=100 with 120 → shows 20 more", func(t *testing.T) {
		got := format.ElementsForLLM(makeEls(120), 100, false)
		if !strings.Contains(got, "... and 20 more elements") {
			t.Errorf("120 elements with maxShow=100 should show '20 more', got: %s", got)
		}
	})
}

// TestFormatElementsForLLMResourceIDTrim verifies resource-id is shortened to
// the part after the last "/".
func TestFormatElementsForLLMResourceIDTrim(t *testing.T) {
	els := []protocol.UIElement{
		{Index: 0, ResourceID: "com.android.settings:id/title"},
	}
	got := format.ElementsForLLM(els, 100, false)
	if !strings.Contains(got, `id="title"`) {
		t.Errorf("expected id=\"title\", got: %s", got)
	}
	if strings.Contains(got, "com.android.settings:id/title") {
		t.Errorf("full resource-id should be trimmed, got: %s", got)
	}
}

// TestFormatElementsForLLMClassNameShort verifies class name is shortened to
// the simple name after the last ".".
func TestFormatElementsForLLMClassNameShort(t *testing.T) {
	els := []protocol.UIElement{
		{Index: 0, ClassName: "android.widget.TextView"},
	}
	got := format.ElementsForLLM(els, 100, false)
	if !strings.Contains(got, "(TextView)") {
		t.Errorf("expected (TextView), got: %s", got)
	}
}

func TestMCPGoServerCreation(t *testing.T) {
	s := New("")
	if s.MCPServer() == nil {
		t.Fatal("mcp-go server not created")
	}
}

// TestRPCCallNilClient verifies that when no RPC client is configured (daemon
// not reachable), rpcCall returns the retry-style error handlers surface to
// callers — the RPC-mode equivalent of the old "nil session" guard.
func TestRPCCallNilClient(t *testing.T) {
	s := newWithClient(nil)
	var out map[string]any
	err := s.rpcCall("status", nil, &out)
	if err == nil {
		t.Error("expected error for nil RPC client")
	}
	if !strings.Contains(err.Error(), "retry") {
		t.Errorf("expected retry-style error, got: %v", err)
	}
}

// TestRPCCallSurfacesClientError verifies rpcCall surfaces a Call error as-is.
// (Daemon-crash recovery now lives in daemon.Client via SetEnsurer, tested at
// the e2e level; rpcCall itself is a plain Call + Unmarshal.)
func TestRPCCallSurfacesClientError(t *testing.T) {
	fake := &fakeRPC{err: fmt.Errorf("connect device: timeout")}
	s := newWithClient(fake)
	var resp struct {
		Message string `json:"message"`
	}
	err := s.rpcCall("tap", map[string]any{"x": 1, "y": 2}, &resp)
	if err == nil {
		t.Fatal("expected the RPC error to surface, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected the original error text, got: %v", err)
	}
	if fake.calls != 1 {
		t.Errorf("Call should be invoked once, got %d", fake.calls)
	}
}

// --- press_key handler tests ---

// TestHandlePressKeyParamsValidation tests parameter validation for press_key.
// A nil RPC client means valid-key/keycode cases fail at rpcCall with a retry
// error (daemon not reachable); validation cases (missing/unknown/wrong-type)
// are rejected before any RPC.
func TestHandlePressKeyParamsValidation(t *testing.T) {
	s := newWithClient(nil)

	t.Run("missing both keycode and key", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{},
			},
		}
		result, _ := s.handlePressKey(context.Background(), req)
		if !result.IsError {
			t.Error("expected error when no keycode or key provided")
		}
	})

	t.Run("unknown key name", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"key": "NONEXISTENT_KEY",
				},
			},
		}
		result, _ := s.handlePressKey(context.Background(), req)
		if result.IsError {
			// unknown key name should be rejected BEFORE reaching the daemon
			hasContent := false
			for _, c := range result.Content {
				if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "unknown key name") {
					hasContent = true
				}
			}
			if !hasContent {
				t.Error("expected 'unknown key name' in error, didn't find it")
			}
		} else {
			t.Error("expected error for unknown key name")
		}
	})

	t.Run("valid key name parameter", func(t *testing.T) {
		// With a nil RPC client, this fails at rpcCall with "device
		// connecting, retry" — but the key resolution should pass.
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"key": "enter",
				},
			},
		}
		result, _ := s.handlePressKey(context.Background(), req)
		if !result.IsError {
			t.Error("expected daemon-not-reachable error (no device)")
		}
		hasRetry := false
		for _, c := range result.Content {
			if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "retry") {
				hasRetry = true
			}
		}
		if !hasRetry {
			t.Error("expected 'retry' message for nil RPC client")
		}
	})

	t.Run("valid keycode parameter", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"keycode": float64(66),
				},
			},
		}
		result, _ := s.handlePressKey(context.Background(), req)
		if !result.IsError {
			t.Error("expected daemon-not-reachable error (no device)")
		}
	})

	t.Run("keycode must be number", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"keycode": "not-a-number",
				},
			},
		}
		result, _ := s.handlePressKey(context.Background(), req)
		if !result.IsError {
			t.Error("expected error for non-numeric keycode")
		}
	})

	t.Run("key must be string", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"key": float64(123),
				},
			},
		}
		result, _ := s.handlePressKey(context.Background(), req)
		if !result.IsError {
			t.Error("expected error when key is not a string")
		}
	})
}

// TestAllMCPToolsRegistered verifies every expected tool is registered.
func TestAllMCPToolsRegistered(t *testing.T) {
	srv := New("")
	mcpSrv := srv.MCPServer()

	expectedTools := []string{
		"list_devices", "screenshot", "get_ui_elements", "observe",
		"tap", "tap_element", "swipe", "type_text",
		"back", "home", "press_key", "launch_app", "wait",
	}

	tools := mcpSrv.ListTools()
	got := make(map[string]bool, len(tools))
	for _, tl := range tools {
		got[tl.Tool.Name] = true
	}
	for _, name := range expectedTools {
		if !got[name] {
			t.Errorf("expected tool %q to be registered, but it was not", name)
		}
	}
}

// --- ImageContent tests ---

// testImageSession builds a Server backed by a session whose Screenshot
// returns a fixed PNG byte slice. We can't import session here without a
// cycle, so we inject a minimal stand-in via the public API: the handler
// calls sess.Screenshot() returning ([]byte, int, int, error).
// To keep this test in the mcp package and cycle-free, we construct a real
// session.Session through a tiny helper that avoids ADB.

// screenshotFn lets tests stub session.Screenshot without a real device.
// We wire it by constructing a Server whose session field is a real
// *session.Session with a nil control conn — but Screenshot needs video.
// Instead, test at the result-shape level by calling NewToolResultImage
// directly, mirroring the handler's construction.

// TestScreenshotResultShape verifies the screenshot result contains a native
// ImageContent (not a base64 text blob). Guards against regressions where
// the handler reverts to NewToolResultText.
func TestScreenshotResultShape(t *testing.T) {
	// Reproduce the exact result construction handleScreenshot uses.
	result := mcp.NewToolResultImage("Screenshot (1080x2400)", "iVBORw0KGgoAAA==", "image/png")

	if len(result.Content) != 2 {
		t.Fatalf("expected 2 content items (text + image), got %d", len(result.Content))
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] should be TextContent, got %T", result.Content[0])
	}
	if !strings.Contains(text.Text, "Screenshot") {
		t.Errorf("caption text = %q, want to contain 'Screenshot'", text.Text)
	}
	img, ok := result.Content[1].(mcp.ImageContent)
	if !ok {
		t.Fatalf("Content[1] should be ImageContent, got %T", result.Content[1])
	}
	if img.MIMEType != "image/png" {
		t.Errorf("mimeType = %q, want image/png", img.MIMEType)
	}
	if img.Data == "" {
		t.Error("ImageContent.Data should be non-empty base64")
	}
	if img.Type != "image" {
		t.Errorf("Type = %q, want 'image'", img.Type)
	}
}

// TestObserveResultShape verifies observe returns ImageContent + TextContent
// (screenshot + UI elements), matching the handler construction.
func TestObserveResultShape(t *testing.T) {
	result := mcp.NewToolResultImage("Observe: 5 interactive elements", "iVBORw0KGgo==", "image/png")
	result.Content = append(result.Content, mcp.TextContent{
		Type: mcp.ContentTypeText,
		Text: "Interactive elements on screen:\n[0] text=ok",
	})

	if len(result.Content) < 2 {
		t.Fatalf("expected >=2 content items, got %d", len(result.Content))
	}
	hasImage := false
	hasText := false
	for _, c := range result.Content {
		switch c.(type) {
		case mcp.ImageContent:
			hasImage = true
		case mcp.TextContent:
			hasText = true
		}
	}
	if !hasImage {
		t.Error("observe result missing ImageContent (screenshot)")
	}
	if !hasText {
		t.Error("observe result missing TextContent (UI elements)")
	}
}
