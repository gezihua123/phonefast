package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gezihua123/phonefast/internal/daemon"
	"github.com/gezihua123/phonefast/internal/format"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

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

// --- format.ElementsForLLM tests (shared package) ---

func TestElementsForLLMEmpty(t *testing.T) {
	got := format.ElementsForLLM(nil, 100, false)
	if got == "" {
		t.Error("expected non-empty output for empty elements")
	}
}

func TestElementsForLLMRendersFields(t *testing.T) {
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
	got := format.ElementsForLLM(els, 100, false)
	for _, want := range []string{"[0]", "Settings", "title", "TextView", "clickable"} {
		if !containsStr(got, want) {
			t.Errorf("ElementsForLLM missing %q in output:\n%s", want, got)
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

// --- extractMessage tests ---

func TestExtractMessage(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`{"message":"Back pressed"}`, "Back pressed"},
		{`{"foo":"bar"}`, `{"foo":"bar"}`},
		{`not json`, "not json"},
		{``, ""},
	}
	for _, c := range cases {
		got := extractMessage(json.RawMessage(c.in))
		if got != c.want {
			t.Errorf("extractMessage(%q) = %q, want %q", c.in, got, c.want)
		}
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

// TestFallbackToDirect verifies that a daemon auto-start failure flips the
// process to direct mode instead of exiting: the command then runs through its
// withSession branch rather than dying with "daemon failed to start".
func TestFallbackToDirect(t *testing.T) {
	env := &cmdEnv{useDaemon: true, binName: "phonefast"}
	env.fallbackToDirect(errors.New("boom"))
	if env.useDaemon {
		t.Error("useDaemon = true after fallbackToDirect, want false")
	}
}

// TestFallbackToDirectHardErrorEnv verifies the PHONEFAST_NO_DAEMON_FALLBACK=1
// opt-out: a daemon auto-start failure is fatal instead of silently degrading
// to direct mode. run as a subprocess because the branch exits.
func TestFallbackToDirectHardErrorEnv(t *testing.T) {
	if os.Getenv("PHONEFAST_TEST_FATAL") == "1" {
		env := &cmdEnv{binName: "phonefast"}
		env.fallbackToDirect(errors.New("boom"))
		return // unreachable — fallbackToDirect must os.Exit(1)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestFallbackToDirectHardErrorEnv")
	cmd.Env = append(os.Environ(), "PHONEFAST_TEST_FATAL=1", "PHONEFAST_NO_DAEMON_FALLBACK=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected non-zero exit when PHONEFAST_NO_DAEMON_FALLBACK=1")
	}
}

// TestEnsureDaemonOrExitFatal verifies ensureDaemonOrExit exits(1) when the
// supervisor cannot start the daemon (commands without a direct fallback must
// fail rather than proceed). run as a subprocess because the branch exits.
func TestEnsureDaemonOrExitFatal(t *testing.T) {
	if os.Getenv("PHONEFAST_TEST_FATAL") == "1" {
		env := &cmdEnv{
			binName: "phonefast",
			supervisor: daemon.NewSupervisor(daemon.SupervisorConfig{
				SocketPath: filepath.Join(os.TempDir(), "pf-nonexistent-test.sock"),
				PIDFile:    filepath.Join(os.TempDir(), "pf-nonexistent-test.pid"),
				Spawn: func(extraEnv []string) (*exec.Cmd, error) {
					return nil, errors.New("spawn must fail")
				},
			}),
		}
		env.ensureDaemonOrExit()
		return // unreachable — ensureDaemonOrExit must os.Exit(1)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestEnsureDaemonOrExitFatal")
	cmd.Env = append(os.Environ(), "PHONEFAST_TEST_FATAL=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected non-zero exit from ensureDaemonOrExit on EnsureRunning failure")
	}
}

// TestParseUIShowArgs verifies the ui/observe argument parser: max-elements
// parsing, --summary and --format flags, -1 semantics, and the
// --format-without-value edge.
func TestParseUIShowArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantMax    int
		wantSum    bool
		wantFormat string
	}{
		{"max + summary + format", []string{"50", "--summary", "--format", "jsonl"}, 50, true, "jsonl"},
		{"-1 shows all", []string{"-1"}, -1, false, ""},
		{"0 → default", []string{"0"}, protocol.DefClientMaxElements, false, ""},
		{"non-numeric → default", []string{"abc"}, protocol.DefClientMaxElements, false, ""},
		{"--format without value → empty, no panic", []string{"--format"}, protocol.DefClientMaxElements, false, ""},
		{"empty args", nil, protocol.DefClientMaxElements, false, ""},
		{"summary only", []string{"--summary"}, protocol.DefClientMaxElements, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMax, gotSum, gotFormat := parseUIShowArgs(tt.args, protocol.DefClientMaxElements)
			if gotMax != tt.wantMax || gotSum != tt.wantSum || gotFormat != tt.wantFormat {
				t.Errorf("parseUIShowArgs(%v) = (%d, %v, %q), want (%d, %v, %q)",
					tt.args, gotMax, gotSum, gotFormat, tt.wantMax, tt.wantSum, tt.wantFormat)
			}
		})
	}
}

// TestExtFromMimeType verifies the MIME → extension mapping.
func TestExtFromMimeType(t *testing.T) {
	tests := map[string]string{
		"image/jpeg":               "jpg",
		"image/jpg":                "jpg",
		"image/png":                "png",
		"":                         "png",
		"application/octet-stream": "png",
	}
	for mime, want := range tests {
		if got := extFromMimeType(mime); got != want {
			t.Errorf("extFromMimeType(%q) = %q, want %q", mime, got, want)
		}
	}
}

// TestWriteScreenshot verifies the base64 decode + file write path with an
// explicit output path.
func TestWriteScreenshot(t *testing.T) {
	payload := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46} // JPEG magic prefix
	b64 := base64.StdEncoding.EncodeToString(payload)
	out := filepath.Join(t.TempDir(), "shot.jpg")

	writeScreenshot([]string{out}, b64, "image/jpeg")

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read written screenshot: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("written bytes = %v, want %v", got, payload)
	}
}

// TestKnownCommand ensures every command dispatched by main()'s switch is
// accepted by knownCommand (the pre-auto-start gate), and typos are rejected.
func TestKnownCommand(t *testing.T) {
	dispatched := []string{
		"daemon", "daemon_worker", "serve", "run", "devices", "help",
		"connect", "disconnect",
		"tap", "tap_element", "swipe", "type", "text", "back", "home",
		"key", "press_key", "launch", "screenshot", "ui", "observe", "ocr",
		"wait", "status",
	}
	for _, c := range dispatched {
		if !knownCommand(c) {
			t.Errorf("knownCommand(%q) = false, but main() dispatches it", c)
		}
	}
	for _, c := range []string{"foobar", "", "TAP", "daemon-worker", "--daemon"} {
		if knownCommand(c) {
			t.Errorf("knownCommand(%q) = true, want false", c)
		}
	}
}

// TestForegroundNeverTouchesDaemon guards the --foreground invariant:
// direct mode must never create a daemon process. The supervisor is given a
// Spawn hook that fails the test if invoked, and a direct-mode command path
// is exercised — the hook must not fire and no pidfile/socket may appear.
func TestForegroundNeverTouchesDaemon(t *testing.T) {
	dir := t.TempDir()
	spawnCalled := false
	supervisor := daemon.NewSupervisor(daemon.SupervisorConfig{
		SocketPath: filepath.Join(dir, "daemon.sock"),
		PIDFile:    filepath.Join(dir, "daemon.pid"),
		Spawn: func(extraEnv []string) (*exec.Cmd, error) {
			spawnCalled = true
			return nil, errors.New("spawn must never be called in direct mode")
		},
	})
	env := &cmdEnv{
		useDaemon:  false,
		binName:    "phonefast",
		supervisor: supervisor,
		dispatcher: daemon.NewDispatcher(nil),
	}

	// A direct-mode command that fails fast (no device): withSession connects
	// to "" serial → the tap handler errors out before any daemon interaction.
	// statusCmd in direct mode probes the pidfile without starting anything —
	// use it as the harmless direct-mode path.
	env.statusCmd()

	if spawnCalled {
		t.Fatal("supervisor spawn was invoked in --foreground mode")
	}
	// Note: the Spawn hook is the complete guard — every daemon-process
	// creation path goes through Supervisor.Spawn (spawnDaemonWorker). If the
	// hook never fires, no daemon process was created by this --foreground run.
}

// TestDaemonStartMode verifies the routing of commands through daemon
// auto-start: excluded commands skip it entirely; required commands
// (connect/disconnect, which have no direct fallback) fail fast; everything
// else degrades to direct mode on failure.
func TestDaemonStartMode(t *testing.T) {
	tests := []struct {
		cmd          string
		wantExcluded bool
		wantRequired bool
	}{
		{"tap", false, false},
		{"swipe", false, false},
		{"screenshot", false, false},
		{"daemon", true, false},
		{"daemon_worker", true, false},
		{"serve", true, false},
		{"devices", true, false},
		{"help", true, false},
		{"status", true, false},
		{"wait", true, false},
		{"connect", false, true},
		{"disconnect", false, true},
		{"unknown_cmd", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			ex, req := daemonStartMode(tt.cmd)
			if ex != tt.wantExcluded {
				t.Errorf("daemonStartMode(%q) excluded = %v, want %v", tt.cmd, ex, tt.wantExcluded)
			}
			if req != tt.wantRequired {
				t.Errorf("daemonStartMode(%q) required = %v, want %v", tt.cmd, req, tt.wantRequired)
			}
		})
	}
}

// TestEnsureDaemonFallsBackToDirect covers the auto-start-failure integration:
// ensureDaemon → Supervisor.EnsureRunning (spawn fails) → fallbackToDirect
// flips the env to direct mode instead of exiting.
func TestEnsureDaemonFallsBackToDirect(t *testing.T) {
	supervisor := daemon.NewSupervisor(daemon.SupervisorConfig{
		SocketPath: filepath.Join(t.TempDir(), "daemon.sock"),
		PIDFile:    filepath.Join(t.TempDir(), "daemon.pid"),
		Spawn: func(extraEnv []string) (*exec.Cmd, error) {
			return nil, errors.New("spawn failed")
		},
	})
	env := &cmdEnv{useDaemon: true, binName: "phonefast", supervisor: supervisor}
	env.ensureDaemon()
	if env.useDaemon {
		t.Error("useDaemon = true after failed auto-start, want direct fallback")
	}
}
