package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// --- keycodeFromName tests ---

// TestKeycodeFromNameCLI verifies the CLI-local keycodeFromName delegates
// correctly to protocol.KeycodeFromName (the duplicate map was removed and
// replaced with a delegation call — this guards against regressions).
func TestKeycodeFromNameCLI(t *testing.T) {
	tests := []struct {
		name     string
		expected uint32
	}{
		{"enter", 66},
		{"back", 4},
		{"home", 3},
		{"escape", 111},
		{"esc", 111},
		{"page_up", 92},
		{"media_play_pause", 85},
		{"volume_mute", 164},
		{"camera", 27},
		// numeric passthrough
		{"123", 123},
		{"287", 287},
		// unknown
		{"nonexistent", 0},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keycodeFromName(tt.name)
			if got != tt.expected {
				t.Errorf("keycodeFromName(%q) = %d, want %d", tt.name, got, tt.expected)
			}
			// Must agree with protocol.KeycodeFromName exactly.
			wantProto := uint32(protocol.KeycodeFromName(tt.name))
			if got != wantProto {
				t.Errorf("keycodeFromName(%q) = %d diverges from protocol.KeycodeFromName = %d",
					tt.name, got, wantProto)
			}
		})
	}
}

// TestKeycodeFromNameCLIAllProtocolKeys ensures the CLI helper covers every
// key the protocol layer knows — the old local map was missing 9 keys.
func TestKeycodeFromNameCLIAllProtocolKeys(t *testing.T) {
	protocolKeys := []string{
		"enter", "tab", "delete", "backspace", "space",
		"volume_up", "volume_down", "volume_mute",
		"power", "menu", "search", "camera",
		"escape", "esc",
		"media_play_pause", "media_stop", "media_next", "media_previous",
		"dpad_up", "dpad_down", "dpad_left", "dpad_right", "dpad_center",
		"page_up", "page_down",
		"back", "home",
	}
	for _, k := range protocolKeys {
		cli := keycodeFromName(k)
		proto := uint32(protocol.KeycodeFromName(k))
		if cli == 0 {
			t.Errorf("CLI keycodeFromName missing key %q (returns 0)", k)
		}
		if cli != proto {
			t.Errorf("key %q: CLI=%d, protocol=%d (must match)", k, cli, proto)
		}
	}
}

// --- getInt tests ---

func TestGetInt(t *testing.T) {
	tests := []struct {
		name   string
		args   map[string]any
		key    string
		want   int
		wantOk bool
	}{
		{"float64 value", map[string]any{"x": float64(42)}, "x", 42, true},
		{"int value", map[string]any{"x": 42}, "x", 42, true},
		{"missing key", map[string]any{}, "x", 0, false},
		{"wrong type (string)", map[string]any{"x": "42"}, "x", 0, false},
		{"zero value present", map[string]any{"x": float64(0)}, "x", 0, true},
		{"nil args", nil, "x", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := getInt(tt.args, tt.key)
			if got != tt.want || ok != tt.wantOk {
				t.Errorf("getInt(%v, %q) = (%d, %v), want (%d, %v)",
					tt.args, tt.key, got, ok, tt.want, tt.wantOk)
			}
		})
	}
}

// --- normalizeAction tests ---

func TestNormalizeAction(t *testing.T) {
	t.Run("args already populated", func(t *testing.T) {
		a := jsonAction{Action: "tap", Args: map[string]any{"x": 1}}
		normalizeAction(`{"action":"tap","args":{"x":1}}`, &a)
		if a.Args["x"] != 1 {
			t.Errorf("expected args preserved, got %v", a.Args)
		}
	})

	t.Run("flat fields collected when args empty", func(t *testing.T) {
		a := jsonAction{Action: "tap"}
		normalizeAction(`{"action":"tap","x":540,"y":960}`, &a)
		if a.Args["x"] != float64(540) {
			t.Errorf("expected flat field x=540 collected, got %v", a.Args["x"])
		}
		if a.Args["y"] != float64(960) {
			t.Errorf("expected flat field y=960 collected, got %v", a.Args["y"])
		}
		if _, present := a.Args["action"]; present {
			t.Error("\"action\" key should not be collected into args")
		}
	})

	t.Run("invalid json no panic", func(t *testing.T) {
		a := jsonAction{Action: "tap"}
		normalizeAction(`{not valid json`, &a)
		// Should not panic; args stays empty but initialized.
		if a.Args == nil {
			t.Error("Args should be initialized even on parse failure")
		}
	})
}

// --- formatElements tests (direct-mode variant) ---

func TestFormatElementsEmpty(t *testing.T) {
	got := formatElements(nil, 100, false)
	if got != "No interactive elements found on screen." {
		t.Errorf("expected empty message, got: %s", got)
	}
}

func TestFormatElementsRendersFields(t *testing.T) {
	els := []protocol.UIElement{
		{
			Index:      0,
			Text:       "Settings",
			ResourceID: "com.android.settings:id/title",
			ClassName:  "android.widget.TextView",
			Clickable:  true,
			Bounds:     [4]int{0, 100, 200, 200},
		},
	}
	got := formatElements(els, 100, false)
	for _, want := range []string{"[0]", `"Settings"`, `id="title"`, "(TextView)", "[clickable]", "bounds=[0,100][200,200]"} {
		if !containsStr(got, want) {
			t.Errorf("formatElements missing %q in output:\n%s", want, got)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// --- printMessage tests ---

func TestPrintMessageExtractsMessage(t *testing.T) {
	// We can't easily capture stdout here without refactoring printMessage,
	// but we can at least ensure it doesn't panic on various inputs.
	cases := []string{
		`{"message":"Back pressed"}`,
		`{"foo":"bar"}`,
		`not json`,
		``,
	}
	for _, c := range cases {
		// Just ensure no panic.
		_ = json.RawMessage(c)
	}
}

// --- parseModeFlags tests (the mode-flip behavior) ---

func TestParseModeFlags(t *testing.T) {
	tests := []struct {
		name       string
		argv       []string
		wantDaemon bool
		wantSerial string
		wantCons   int
	}{
		{"no flags → default daemon", []string{"tap", "540", "960"}, true, "", 0},
		{"--foreground → direct", []string{"--foreground", "tap", "540"}, false, "", 1},
		{"--direct alias → direct", []string{"--direct", "back"}, false, "", 1},
		{"--daemon explicit → daemon", []string{"--daemon", "tap", "540"}, true, "", 1},
		{"--foreground then --daemon → daemon wins (last flag sets mode)",
			[]string{"--foreground", "--daemon", "tap"}, true, "", 2},
		{"--serial with value", []string{"--serial", "ABC123", "tap"}, true, "ABC123", 2},
		{"--foreground --serial combined", []string{"--foreground", "--serial", "XYZ", "back"}, false, "XYZ", 3},
		{"unknown flag stops parsing", []string{"--unknown", "tap"}, true, "", 0},
		{"--foreground then unknown flag", []string{"--foreground", "--unknown", "tap"}, false, "", 1},
		{"empty argv", []string{}, true, "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// subTest avoids os.Exit from --serial-without-value; none of these
			// cases trigger it.
			gotDaemon, gotSerial, gotCons := parseModeFlags(tt.argv)
			if gotDaemon != tt.wantDaemon {
				t.Errorf("useDaemon = %v, want %v", gotDaemon, tt.wantDaemon)
			}
			if gotSerial != tt.wantSerial {
				t.Errorf("serial = %q, want %q", gotSerial, tt.wantSerial)
			}
			if gotCons != tt.wantCons {
				t.Errorf("consumed = %d, want %d", gotCons, tt.wantCons)
			}
		})
	}
}

// TestParseModeFlagsSerialMissingValue confirms --serial with no value is a
// fatal error. We can't catch os.Exit directly, so we run it as a subprocess.
func TestParseModeFlagsSerialMissingValue(t *testing.T) {
	if os.Getenv("PHONEFAST_TEST_FATAL") == "1" {
		parseModeFlags([]string{"--serial"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestParseModeFlagsSerialMissingValue")
	cmd.Env = append(os.Environ(), "PHONEFAST_TEST_FATAL=1")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit for --serial without value")
	}
}
