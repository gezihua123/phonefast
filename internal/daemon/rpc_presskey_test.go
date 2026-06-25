package daemon

import (
	"encoding/json"
	"testing"
)

// TestHandlePressKeyUnknownKeyName verifies that an unrecognized key name is
// rejected with ErrInvalid BEFORE any device interaction — matching the MCP
// handler's behavior. Previously daemon returned "Key 0 pressed" for unknown
// names, silently sending keycode 0 to the device.
func TestHandlePressKeyUnknownKeyName(t *testing.T) {
	params, _ := json.Marshal(map[string]any{"key": "TOTALLY_BOGUS_KEY"})
	req := &Request{
		JSONRPC: "2.0",
		Method:  "press_key",
		Params:  params,
		ID:      1,
	}

	// nil session is fine — the kc==0 check must fire before session use.
	resp := handlePressKey(nil, req)

	if resp.Error == nil {
		t.Fatal("expected error for unknown key name, got nil error")
	}
	if resp.Error.Code != ErrInvalid {
		t.Errorf("error code = %d, want %d (ErrInvalid)", resp.Error.Code, ErrInvalid)
	}
	if resp.Error.Message == "" {
		t.Error("expected non-empty error message")
	}
}

// TestHandlePressKeyNumericKeycodeRequiresSession verifies that a numeric
// keycode passes validation but fails at the session layer (nil session),
// proving the kc==0 check does not falsely reject valid numeric keycodes.
func TestHandlePressKeyNumericKeycodeRequiresSession(t *testing.T) {
	params, _ := json.Marshal(map[string]any{"keycode": float64(66)})
	req := &Request{
		JSONRPC: "2.0",
		Method:  "press_key",
		Params:  params,
		ID:      2,
	}

	resp := handlePressKey(nil, req)

	// Must NOT be the kc==0 validation error — it should be the no-device error.
	if resp.Error == nil {
		t.Fatal("expected no-device error, got success (nil session should fail)")
	}
	if resp.Error.Code != ErrNoDevice {
		t.Errorf("error code = %d, want %d (ErrNoDevice)", resp.Error.Code, ErrNoDevice)
	}
}

// TestHandlePressKeyMissingParams verifies the missing-parameter path.
func TestHandlePressKeyMissingParams(t *testing.T) {
	req := &Request{
		JSONRPC: "2.0",
		Method:  "press_key",
		Params:  json.RawMessage(`{}`),
		ID:      3,
	}

	resp := handlePressKey(nil, req)
	if resp.Error == nil {
		t.Fatal("expected error for missing keycode/key, got nil")
	}
	if resp.Error.Code != ErrInvalid {
		t.Errorf("error code = %d, want %d (ErrInvalid)", resp.Error.Code, ErrInvalid)
	}
}
