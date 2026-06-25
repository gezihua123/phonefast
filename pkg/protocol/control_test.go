package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestTouchMsgEncode(t *testing.T) {
	msg := NewTouchMsg(ActionDown, 100, 200, 1080, 2400)
	data := msg.Encode()

	buf := bytes.NewReader(data)

	// Read type byte
	typeByte, err := buf.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	if typeByte != TypeInjectTouchEvent {
		t.Errorf("expected type %d, got %d", TypeInjectTouchEvent, typeByte)
	}

	// Read action
	action, err := buf.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	if action != ActionDown {
		t.Errorf("expected action %d, got %d", ActionDown, action)
	}
}

func TestKeycodeMsgEncode(t *testing.T) {
	msg := NewKeycodeMsg(KeyEventActionDown, KeycodeBack)
	data := msg.Encode()

	if data[0] != TypeInjectKeycode {
		t.Errorf("expected type %d, got %d", TypeInjectKeycode, data[0])
	}
	if data[1] != KeyEventActionDown {
		t.Errorf("expected action %d, got %d", KeyEventActionDown, data[1])
	}
}

func TestTextMsgEncode(t *testing.T) {
	msg := NewTextMsg("hello")
	data := msg.Encode()

	if data[0] != TypeInjectText {
		t.Errorf("expected type %d, got %d", TypeInjectText, data[0])
	}
}

func TestKeycodeFromName(t *testing.T) {
	tests := []struct {
		name     string
		expected int
	}{
		// Common navigation keys
		{"back", KeycodeBack},
		{"home", KeycodeHome},
		{"enter", KeycodeEnter},
		{"tab", KeycodeTab},
		{"delete", KeycodeDelete},
		{"backspace", KeycodeDelete},
		{"space", KeycodeSpace},
		// Volume keys
		{"volume_up", KeycodeVolumeUp},
		{"volume_down", KeycodeVolumeDown},
		{"volume_mute", 164},
		// System keys
		{"power", KeycodePower},
		{"menu", KeycodeMenu},
		{"search", KeycodeSearch},
		{"camera", 27},
		// Escape
		{"escape", 111},
		{"esc", 111},
		// Media keys
		{"media_play_pause", 85},
		{"media_stop", 86},
		{"media_next", 87},
		{"media_previous", 88},
		// D-pad
		{"dpad_up", 19},
		{"dpad_down", 20},
		{"dpad_left", 21},
		{"dpad_right", 22},
		{"dpad_center", 23},
		// Page keys
		{"page_up", 92},
		{"page_down", 93},
		// Numeric string (passthrough)
		{"123", 123},
		{"287", 287},
		// Edge cases
		{"unknown", 0},
		{"", 0},
		{"  ", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := KeycodeFromName(tt.name)
			if result != tt.expected {
				t.Errorf("KeycodeFromName(%q) = %d, want %d", tt.name, result, tt.expected)
			}
		})
	}
}

// TestKeycodeFromNameCaseInsensitive verifies key name lookup is
// case-insensitive (callers are expected to ToLower).
func TestKeycodeFromNameCaseInsensitive(t *testing.T) {
	// KeycodeFromName itself is case-sensitive; callers lowercase.
	// Verify the lowercase version works and uppercase does not.
	if KeycodeFromName("enter") != 66 {
		t.Error("lowercase 'enter' should return 66")
	}
	if KeycodeFromName("ENTER") == 66 {
		t.Error("KeycodeFromName is case-sensitive; 'ENTER' should not match 'enter'")
	}
}

func TestStartAppMsgEncode(t *testing.T) {
	msg := NewStartAppMsg("com.example.app")
	data := msg.Encode()

	if data[0] != TypeStartApp {
		t.Errorf("expected type %d, got %d", TypeStartApp, data[0])
	}
}

// TestTouchMsgFullLayout verifies the exact binary layout matches
// scrcpy's ControlMessageReader.parseInjectTouchEvent():
//   1B action + 8B pointerId + 12B position + 2B pressure(u16) + 4B actionBtn + 4B buttons
func TestTouchMsgFullLayout(t *testing.T) {
	msg := NewTouchMsg(ActionDown, 540, 960, 1080, 1920)
	msg.Pressure = 1.0 // full pressure

	data := msg.Encode()
	// total bytes: 1(type) + 1(action) + 8(ptr) + 12(pos) + 2(pressure) + 4(actionBtn) + 4(buttons) = 32
	if len(data) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(data))
	}

	r := bytes.NewReader(data)
	readByte := func() byte { b, _ := r.ReadByte(); return b }
	readU64 := func() uint64 {
		var v [8]byte; r.Read(v[:]); return binary.BigEndian.Uint64(v[:])
	}
	readU32 := func() uint32 {
		var v [4]byte; r.Read(v[:]); return binary.BigEndian.Uint32(v[:])
	}
	readU16 := func() uint16 {
		var v [2]byte; r.Read(v[:]); return binary.BigEndian.Uint16(v[:])
	}

	if typ := readByte(); typ != TypeInjectTouchEvent {
		t.Errorf("type: got %d want %d", typ, TypeInjectTouchEvent)
	}
	if act := readByte(); act != ActionDown {
		t.Errorf("action: got %d want %d", act, ActionDown)
	}
	if ptr := readU64(); ptr != 0xFFFFFFFFFFFFFFFF {
		t.Errorf("pointerId: got %d want 0xFFFFFFFFFFFFFFFF", ptr)
	}
	if x := readU32(); x != 540 {
		t.Errorf("x: got %d want 540", x)
	}
	if y := readU32(); y != 960 {
		t.Errorf("y: got %d want 960", y)
	}
	if sw := readU16(); sw != 1080 {
		t.Errorf("screenW: got %d want 1080", sw)
	}
	if sh := readU16(); sh != 1920 {
		t.Errorf("screenH: got %d want 1920", sh)
	}
	// pressure=1.0 → 0xffff (u16 fixed-point special case)
	if p := readU16(); p != 0xffff {
		t.Errorf("pressure: got 0x%04x want 0xffff", p)
	}
	readU32() // actionBtn
	readU32() // buttons
	if r.Len() != 0 {
		t.Errorf("unexpected trailing bytes: %d", r.Len())
	}
}

func TestTouchPressureEncoding(t *testing.T) {
	cases := []struct {
		pressure float32
		expected uint16
	}{
		{0.0, 0x0000},
		{1.0, 0xffff}, // special case
		{0.5, uint16(0.5 * 65536)},
		{0.25, uint16(0.25 * 65536)},
	}
	for _, c := range cases {
		got := floatToU16Fixed(c.pressure)
		if got != c.expected {
			t.Errorf("floatToU16Fixed(%v) = 0x%04x, want 0x%04x", c.pressure, got, c.expected)
		}
	}
}

// TestScrollMsgLayout verifies scroll encoding matches scrcpy:
//   position(12B) + hScroll(2B i16) + vScroll(2B i16) + buttons(4B) = 20B payload
func TestScrollMsgLayout(t *testing.T) {
	msg := NewScrollMsg(540, 960, 1080, 1920, 0, -3.0)
	data := msg.Encode()
	// 1(type) + 4+4+2+2(pos) + 2(h) + 2(v) + 4(buttons) = 21
	if len(data) != 21 {
		t.Fatalf("expected 21 bytes, got %d", len(data))
	}

	// vScroll=-3.0 → int16(-3.0 * 2048) = int16(-6144) = 0xe800
	vScrollOffset := 1 + 12 + 2 // type + pos + hScroll
	rawV := binary.BigEndian.Uint16(data[vScrollOffset : vScrollOffset+2])
	gotV := int16(rawV)
	wantV := int16(-3.0 * 2048)
	if gotV != wantV {
		t.Errorf("vScroll: got %d want %d", gotV, wantV)
	}
}

func TestBackOrScreenOnEncode(t *testing.T) {
	msg := &ControlMessage{
		Type:       TypeBackOrScreenOn,
		ActionBack: 0,
	}
	data := msg.Encode()

	if data[0] != TypeBackOrScreenOn {
		t.Errorf("expected type %d, got %d", TypeBackOrScreenOn, data[0])
	}
	if data[1] != 0 {
		t.Errorf("expected action 0, got %d", data[1])
	}
}
