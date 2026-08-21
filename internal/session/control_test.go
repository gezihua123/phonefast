package session

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// --- Mock connection helpers ---

// newMockSession creates a Session with a net.Pipe()-backed controlConn.
// The caller gets the reader end to verify what the Session writes.
// TapDelay defaults to defaultTapDelay (same as production Connect()).
//
// preheatKeyframe is stubbed out: the post-action RESET_VIDEO write blocks
// on the synchronous pipe until someone reads it, which would stall every
// action test after its final expected message. The preheat write itself is
// covered by TestTapPreheatsKeyframe.
func newMockSession() (s *Session, reader net.Conn) {
	client, server := net.Pipe()
	s = &Session{
		serial:            "test-device",
		Scid:              1,
		deviceW:           1080,
		deviceH:           2400,
		nativeW:           1080,
		nativeH:           2400,
		TapDelay:          defaultTapDelay,
		controlConn:       client,
		preheatKeyframeFn: func() {},
	}
	return s, server
}

// readTouchMsg reads a single TypeInjectTouchEvent message from conn.
// Returns the action, position, and pressure.
func readTouchMsg(t *testing.T, conn net.Conn) (action byte, x, y int32, pressure uint16) {
	t.Helper()

	// Touch message layout: 1B type + 1B action + 8B ptr + 12B pos + 2B pressure + 4B btn + 4B buttons = 32B
	buf := make([]byte, 32)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read touch msg: %v", err)
	}

	if buf[0] != protocol.TypeInjectTouchEvent {
		t.Fatalf("expected type %d (inject_touch), got %d", protocol.TypeInjectTouchEvent, buf[0])
	}

	action = buf[1]
	// skip bytes 2-9 (pointerId)
	x = int32(binary.BigEndian.Uint32(buf[10:14]))
	y = int32(binary.BigEndian.Uint32(buf[14:18]))
	// skip bytes 18-22 (screenW, screenH)
	pressure = binary.BigEndian.Uint16(buf[22:24])

	return
}

// readKeyMsg reads a single TypeInjectKeycode message from conn.
func readKeyMsg(t *testing.T, conn net.Conn) (action byte, keycode int) {
	t.Helper()

	buf := make([]byte, 14)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read key msg: %v", err)
	}

	if buf[0] != protocol.TypeInjectKeycode {
		t.Fatalf("expected type %d (inject_keycode), got %d", protocol.TypeInjectKeycode, buf[0])
	}

	action = buf[1]
	keycode = int(binary.BigEndian.Uint32(buf[2:6]))
	return
}

// --- Tap protocol tests ---

// TestTapSendsTouchDownThenUp verifies Tap() emits ACTION_DOWN → ACTION_UP
// in correct scrcpy binary format, with both events targeting the same
// screen coordinates. Install buttons can't be triggered if DOWN and UP
// don't land on the same location.
func TestTapSendsTouchDownThenUp(t *testing.T) {
	s, reader := newMockSession()

	done := make(chan error, 1)
	go func() {
		done <- s.Tap(540, 1296) // Play Store install button typical position
	}()

	// First message: ACTION_DOWN
	action1, x1, y1, pressure1 := readTouchMsg(t, reader)
	if action1 != protocol.ActionDown {
		t.Errorf("first msg action = %d, want %d (ACTION_DOWN)", action1, protocol.ActionDown)
	}
	if x1 != 540 || y1 != 1296 {
		t.Errorf("DOWN position = (%d,%d), want (540,1296)", x1, y1)
	}
	if pressure1 != 0xffff {
		t.Errorf("DOWN pressure = 0x%04x, want 0xffff (full pressure)", pressure1)
	}

	// Second message: ACTION_UP — same coordinates
	action2, x2, y2, pressure2 := readTouchMsg(t, reader)
	if action2 != protocol.ActionUp {
		t.Errorf("second msg action = %d, want %d (ACTION_UP)", action2, protocol.ActionUp)
	}
	if x2 != 540 || y2 != 1296 {
		t.Errorf("UP position = (%d,%d), want (540,1296) — DOWN and UP must match", x2, y2)
	}
	if pressure2 != 0xffff {
		t.Errorf("UP pressure = 0x%04x, want 0xffff", pressure2)
	}

	if err := <-done; err != nil {
		t.Errorf("Tap() returned error: %v", err)
	}
}

// TestTapPreheatsKeyframe verifies Tap's real preheat path: after DOWN+UP
// the session writes the RESET_VIDEO (17) byte so the next screenshot can
// hit the freshness fast path. Uses a raw session (no preheat stub).
func TestTapPreheatsKeyframe(t *testing.T) {
	client, server := net.Pipe()
	s := &Session{
		serial:      "test-device",
		deviceW:     1080,
		deviceH:     2400,
		TapDelay:    defaultTapDelay,
		controlConn: client,
	}

	done := make(chan error, 1)
	go func() {
		done <- s.Tap(540, 1296)
	}()

	readTouchMsg(t, server) // DOWN
	readTouchMsg(t, server) // UP

	buf := make([]byte, 1)
	if _, err := io.ReadFull(server, buf); err != nil {
		t.Fatalf("read reset byte: %v", err)
	}
	if buf[0] != 17 {
		t.Errorf("post-tap preheat wrote %d, want 17 (RESET_VIDEO)", buf[0])
	}

	if err := <-done; err != nil {
		t.Errorf("Tap() returned error: %v", err)
	}
}

// --- TapDelay timing tests ---

// TestTapDefaultDelay verifies the default TapDelay (10ms) is observed.
func TestTapDefaultDelay(t *testing.T) {
	s, reader := newMockSession()

	done := make(chan error, 1)
	go func() {
		done <- s.Tap(540, 1296)
	}()

	readTouchMsg(t, reader) // DOWN
	t1 := time.Now()
	readTouchMsg(t, reader) // UP
	t2 := time.Now()

	interval := t2.Sub(t1)
	t.Logf("default TapDelay → DOWN→UP interval: %v", interval)

	// Default is 10ms; allow scheduling jitter
	if interval > 30*time.Millisecond {
		t.Errorf("default DOWN→UP interval = %v, want ≈10ms", interval)
	}

	if err := <-done; err != nil {
		t.Errorf("Tap() returned error: %v", err)
	}
}

// TestTapCustomDelay verifies the caller can externally control Tap()
// timing via Session.TapDelay. Different devices / apps may need
// different tap durations to trigger UI actions.
func TestTapCustomDelay(t *testing.T) {
	testCases := []struct {
		delay time.Duration
		minMs time.Duration
		desc  string
	}{
		{20 * time.Millisecond, 15, "fast tap (≤20ms — may fail Play Store)"},
		{80 * time.Millisecond, 70, "slow tap (≥80ms — safe for any app)"},
		{120 * time.Millisecond, 110, "long press-like tap"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			s, reader := newMockSession()
			s.TapDelay = tc.delay // override

			done := make(chan error, 1)
			go func() {
				done <- s.Tap(100, 200)
			}()

			readTouchMsg(t, reader) // DOWN
			t1 := time.Now()
			readTouchMsg(t, reader) // UP
			t2 := time.Now()

			interval := t2.Sub(t1)
			t.Logf("TapDelay=%v → actual interval=%v", tc.delay, interval)

			if interval < tc.minMs*time.Millisecond {
				t.Errorf("interval = %v, want ≥ %v (TapDelay=%v)", interval, tc.minMs*time.Millisecond, tc.delay)
			}

			if err := <-done; err != nil {
				t.Errorf("Tap() returned error: %v", err)
			}
		})
	}
}

// TestTapZeroDelayFallsBack verifies that TapDelay=0 falls back to
// the defaultTapDelay default, preventing accidentally-zero tap durations.
func TestTapZeroDelayFallsBack(t *testing.T) {
	s, reader := newMockSession()
	s.TapDelay = 0 // zero → should use defaultTapDelay default

	done := make(chan error, 1)
	go func() {
		done <- s.Tap(540, 1296)
	}()

	readTouchMsg(t, reader) // DOWN
	t1 := time.Now()
	readTouchMsg(t, reader) // UP
	t2 := time.Now()

	interval := t2.Sub(t1)
	t.Logf("TapDelay=0 (default fallback) → interval=%v", interval)

	if interval > 30*time.Millisecond {
		t.Errorf("TapDelay=0 should fall back to 10ms, got interval=%v", interval)
	}

	if err := <-done; err != nil {
		t.Errorf("Tap() returned error: %v", err)
	}
}

// TestTapAtMultipleCoordinates verifies Tap position encoding is correct
// across different screen regions (edge conditions for coordinate serialization).
func TestTapAtMultipleCoordinates(t *testing.T) {
	testCases := []struct {
		x, y int32
		desc string
	}{
		{100, 200, "top-left area"},
		{540, 1296, "center area (install button typical)"},
		{1000, 2300, "bottom-right area"},
		{0, 0, "origin — edge case"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			s, reader := newMockSession()

			go func() {
				s.Tap(int(tc.x), int(tc.y))
			}()

			_, x1, y1, _ := readTouchMsg(t, reader) // DOWN
			if x1 != tc.x || y1 != tc.y {
				t.Errorf("DOWN at (%d,%d), want (%d,%d)", x1, y1, tc.x, tc.y)
			}

			_, x2, y2, _ := readTouchMsg(t, reader) // UP
			if x2 != tc.x || y2 != tc.y {
				t.Errorf("UP at (%d,%d), want (%d,%d)", x2, y2, tc.x, tc.y)
			}
		})
	}
}

// TestTapNilControlConn ensures Tap returns error when control socket is nil.
func TestTapNilControlConn(t *testing.T) {
	s := &Session{
		serial:      "test-device",
		TapDelay:    10 * time.Millisecond,
		controlConn: nil,
	}
	err := s.Tap(100, 200)
	if err == nil {
		t.Error("expected error for nil controlConn, got nil")
	}
}

// --- Swipe tests ---

func TestSwipeSendsCorrectSequence(t *testing.T) {
	s, reader := newMockSession()

	done := make(chan error, 1)
	go func() {
		done <- s.Swipe(100, 500, 900, 1500, 200)
	}()

	action, x, y, _ := readTouchMsg(t, reader)
	if action != protocol.ActionDown {
		t.Errorf("swipe must start with ACTION_DOWN, got %d", action)
	}
	if x != 100 || y != 500 {
		t.Errorf("start at (%d,%d), want (100,500)", x, y)
	}

	moveCount := 0
	for {
		action, _, _, _ = readTouchMsg(t, reader)
		if action == protocol.ActionUp {
			break
		}
		if action != protocol.ActionMove {
			t.Errorf("intermediate action should be ACTION_MOVE(%d), got %d", protocol.ActionMove, action)
		}
		moveCount++
	}

	if moveCount == 0 {
		t.Error("swipe produced no ACTION_MOVE events")
	}
	t.Logf("swipe move events: %d", moveCount)

	if err := <-done; err != nil {
		t.Errorf("Swipe() returned error: %v", err)
	}
}

// --- PressKey tests ---

func TestPressKeySendsCorrectSequence(t *testing.T) {
	s, reader := newMockSession()

	done := make(chan error, 1)
	go func() {
		done <- s.PressKey(protocol.KeycodeEnter)
	}()

	action1, keycode1 := readKeyMsg(t, reader)
	if action1 != protocol.KeyEventActionDown {
		t.Errorf("first msg action = %d, want %d (KEY_DOWN)", action1, protocol.KeyEventActionDown)
	}
	if keycode1 != protocol.KeycodeEnter {
		t.Errorf("keycode = %d, want %d (ENTER)", keycode1, protocol.KeycodeEnter)
	}

	action2, keycode2 := readKeyMsg(t, reader)
	if action2 != protocol.KeyEventActionUp {
		t.Errorf("second msg action = %d, want %d (KEY_UP)", action2, protocol.KeyEventActionUp)
	}
	if keycode2 != protocol.KeycodeEnter {
		t.Errorf("keycode = %d, want %d (ENTER)", keycode2, protocol.KeycodeEnter)
	}

	if err := <-done; err != nil {
		t.Errorf("PressKey() returned error: %v", err)
	}
}

// TestPressKeyNilControlConn ensures PressKey returns error when control socket is nil.
func TestPressKeyNilControlConn(t *testing.T) {
	s := &Session{
		serial:      "test-device",
		controlConn: nil,
	}
	err := s.PressKey(protocol.KeycodeEnter)
	if err == nil {
		t.Error("expected error for nil controlConn, got nil")
	}
}

// TestPressKeyVariousKeycodes verifies correct keycode encoding for all supported keys.
func TestPressKeyVariousKeycodes(t *testing.T) {
	testCases := []struct {
		keycode int
		desc    string
	}{
		{protocol.KeycodeEnter, "ENTER (66)"},
		{protocol.KeycodeBack, "BACK (4)"},
		{protocol.KeycodeHome, "HOME (3)"},
		{protocol.KeycodeDelete, "DELETE (67)"},
		{protocol.KeycodeTab, "TAB (61)"},
		{protocol.KeycodeSpace, "SPACE (62)"},
		{protocol.KeycodeVolumeUp, "VOLUME_UP (24)"},
		{protocol.KeycodeVolumeDown, "VOLUME_DOWN (25)"},
		{protocol.KeycodePower, "POWER (26)"},
		{protocol.KeycodeMenu, "MENU (82)"},
		{19, "DPAD_UP"},
		{23, "DPAD_CENTER"},
		{111, "ESCAPE"},
		{92, "PAGE_UP"},
		{85, "MEDIA_PLAY_PAUSE"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			s, reader := newMockSession()

			done := make(chan error, 1)
			go func() {
				done <- s.PressKey(tc.keycode)
			}()

			action1, keycode1 := readKeyMsg(t, reader)
			if action1 != protocol.KeyEventActionDown {
				t.Errorf("first msg action = %d, want %d (KEY_DOWN)", action1, protocol.KeyEventActionDown)
			}
			if keycode1 != tc.keycode {
				t.Errorf("keycode = %d, want %d", keycode1, tc.keycode)
			}

			action2, keycode2 := readKeyMsg(t, reader)
			if action2 != protocol.KeyEventActionUp {
				t.Errorf("second msg action = %d, want %d (KEY_UP)", action2, protocol.KeyEventActionUp)
			}
			if keycode2 != tc.keycode {
				t.Errorf("keycode = %d, want %d", keycode2, tc.keycode)
			}

			if err := <-done; err != nil {
				t.Errorf("PressKey(%d) returned error: %v", tc.keycode, err)
			}
		})
	}
}

// --- ScaleToDevice tests ---

// TestScaleToDevice verifies coordinate conversion from NativeW×NativeH
// (display-native, used by UI elements) to DeviceW×DeviceH (video, used by touch).
func TestScaleToDevice(t *testing.T) {
	// Typical phone: native 1080×2400, scrcpy video 488×1080
	s := &Session{
		deviceW: 488,
		deviceH: 1080,
		nativeW: 1080,
		nativeH: 2400,
	}

	tests := []struct {
		nx, ny       int
		wantX, wantY int
		desc         string
	}{
		{540, 1200, 244, 540, "center"},
		{0, 0, 0, 0, "origin"},
		{1080, 0, 488, 0, "top-right (native space)"},
		{541, 559, 244, 251, "install button (typical)"},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			sx, sy := s.ScaleToDevice(tc.nx, tc.ny)
			if sx != tc.wantX || sy != tc.wantY {
				t.Errorf("ScaleToDevice(%d,%d) = (%d,%d), want (%d,%d)",
					tc.nx, tc.ny, sx, sy, tc.wantX, tc.wantY)
			}
		})
	}
}

// TestScaleToDeviceNoOpWhenEqual returns identity when native and
// video resolution match.
func TestScaleToDeviceNoOpWhenEqual(t *testing.T) {
	s := &Session{
		deviceW: 1080,
		deviceH: 2400,
		nativeW: 1080,
		nativeH: 2400,
	}

	sx, sy := s.ScaleToDevice(540, 1200)
	if sx != 540 || sy != 1200 {
		t.Errorf("ScaleToDevice should be no-op when Native==Device, got (%d,%d)", sx, sy)
	}
}

// TestScaleToDevicePassthroughWhenNativeUnset returns the input
// NativeW/H == DeviceW/H means no scaling needed (same resolution space).
func TestScaleToDevicePassthroughWhenSameResolution(t *testing.T) {
	s := &Session{
		deviceW: 1080,
		deviceH: 2400,
		nativeW: 1080,
		nativeH: 2400,
	}

	sx, sy := s.ScaleToDevice(541, 559)
	if sx != 541 || sy != 559 {
		t.Errorf("ScaleToDevice should pass through when Native==Device, got (%d,%d)", sx, sy)
	}
}

// TestGetNativeDisplaySizeParsing verifies parsing of "wm size" output.
func TestGetNativeDisplaySizeParsing(t *testing.T) {
	// We can't call getNativeDisplaySize without ADB, so test the parsing
	// logic inline with mock output strings.
	tests := []struct {
		output  string
		wantW   int
		wantH   int
		wantErr bool
	}{
		{"Physical size: 1080x2400", 1080, 2400, false},
		{"Physical size: 1080x2400\n", 1080, 2400, false},
		{"Override size: 720x1600", 720, 1600, false},
		{"Physical size: 1440x3200", 1440, 3200, false},
	}

	for _, tc := range tests {
		var w, h int
		var err error
		if n, _ := fmt.Sscanf(tc.output, "Physical size: %dx%d", &w, &h); n == 2 {
			err = nil
		} else if n, _ := fmt.Sscanf(tc.output, "Override size: %dx%d", &w, &h); n == 2 {
			err = nil
		} else {
			err = fmt.Errorf("cannot parse")
		}

		if tc.wantErr && err == nil {
			t.Errorf("expected error for %q", tc.output)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("unexpected error for %q: %v", tc.output, err)
		}
		if w != tc.wantW || h != tc.wantH {
			t.Errorf("parsed %q → (%d,%d), want (%d,%d)", tc.output, w, h, tc.wantW, tc.wantH)
		}
	}
}

// --- Benchmark ---

func BenchmarkTapTiming(b *testing.B) {
	s, reader := newMockSession()

	go func() {
		buf := make([]byte, 32)
		for i := 0; i < b.N; i++ {
			io.ReadFull(reader, buf) // DOWN
			io.ReadFull(reader, buf) // UP
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Tap(540, 1296)
	}
	b.StopTimer()
}

// Ensure protocol import compiles.
var _ = protocol.Position{}

// --- requestKeyframe tests ---

// TestRequestKeyframeWritesResetVideo verifies requestKeyframe writes the
// RESET_VIDEO (type 17) byte through the locked control conn.
func TestRequestKeyframeWritesResetVideo(t *testing.T) {
	s, server := newMockSession()
	defer server.Close()
	// Note: intentionally not calling s.Close() — it would invoke
	// adb.StopServer on a fake serial. The mock session has no real
	// forwards (videoPort=0) and no background goroutines.

	// Reads must happen concurrently — net.Pipe write blocks until read.
	go s.requestKeyframe()

	buf := make([]byte, 1)
	if _, err := io.ReadFull(server, buf); err != nil {
		t.Fatalf("read reset byte: %v", err)
	}
	if buf[0] != 17 {
		t.Fatalf("requestKeyframe wrote %d, want 17 (RESET_VIDEO)", buf[0])
	}
}

// TestRequestKeyframeNilConnNoPanic verifies requestKeyframe is safe when the
// control conn has been closed/nilled (the race the lock fix addresses).
func TestRequestKeyframeNilConnNoPanic(t *testing.T) {
	s := &Session{} // nil controlConn
	// Must not panic and must not dereference a nil conn.
	s.requestKeyframe()
}

// --- Swipe error tests ---

// TestSwipeNilControlConn ensures Swipe returns error when control socket is nil.
func TestSwipeNilControlConn(t *testing.T) {
	s := &Session{serial: "test-device"}
	err := s.Swipe(0, 0, 100, 100, 100)
	if err == nil {
		t.Error("expected error for nil controlConn, got nil")
	}
}

// TestSwipeMidGestureWriteError verifies Swipe surfaces a write failure on an
// intermediate ACTION_MOVE event instead of silently swallowing it (a dead
// control connection must fail the gesture, not report success).
func TestSwipeMidGestureWriteError(t *testing.T) {
	s, reader := newMockSession()

	done := make(chan error, 1)
	go func() {
		done <- s.Swipe(100, 500, 900, 1500, 100)
	}()

	// DOWN succeeds, then the peer dies mid-gesture.
	readTouchMsg(t, reader)
	reader.Close()

	if err := <-done; err == nil {
		t.Error("Swipe() returned nil despite a mid-gesture write failure")
	}
}

// --- Back tests ---

// TestBackSendsDownUpSequence verifies Back emits ACTION_DOWN (0) then
// ACTION_UP (1) of TypeBackOrScreenOn — Android triggers back on UP.
func TestBackSendsDownUpSequence(t *testing.T) {
	s, reader := newMockSession()

	done := make(chan error, 1)
	go func() {
		done <- s.Back()
	}()

	buf := make([]byte, 2)
	if _, err := io.ReadFull(reader, buf); err != nil {
		t.Fatalf("read back down: %v", err)
	}
	if buf[0] != protocol.TypeBackOrScreenOn || buf[1] != 0 {
		t.Errorf("first back msg = %v, want type=%d action=0", buf, protocol.TypeBackOrScreenOn)
	}

	if _, err := io.ReadFull(reader, buf); err != nil {
		t.Fatalf("read back up: %v", err)
	}
	if buf[0] != protocol.TypeBackOrScreenOn || buf[1] != 1 {
		t.Errorf("second back msg = %v, want type=%d action=1", buf, protocol.TypeBackOrScreenOn)
	}

	if err := <-done; err != nil {
		t.Errorf("Back() returned error: %v", err)
	}
}

// TestBackNilControlConn ensures Back returns error when control socket is nil.
func TestBackNilControlConn(t *testing.T) {
	s := &Session{serial: "test-device"}
	if err := s.Back(); err == nil {
		t.Error("expected error for nil controlConn, got nil")
	}
}

// --- Home tests ---

// TestHomeDelegatesToPressKey verifies Home sends the HOME keycode
// down/up pair through PressKey.
func TestHomeDelegatesToPressKey(t *testing.T) {
	s, reader := newMockSession()

	done := make(chan error, 1)
	go func() {
		done <- s.Home()
	}()

	action1, keycode1 := readKeyMsg(t, reader)
	if action1 != protocol.KeyEventActionDown || keycode1 != protocol.KeycodeHome {
		t.Errorf("first msg = (%d, %d), want (DOWN, HOME)", action1, keycode1)
	}
	action2, keycode2 := readKeyMsg(t, reader)
	if action2 != protocol.KeyEventActionUp || keycode2 != protocol.KeycodeHome {
		t.Errorf("second msg = (%d, %d), want (UP, HOME)", action2, keycode2)
	}

	if err := <-done; err != nil {
		t.Errorf("Home() returned error: %v", err)
	}
}

// TestHomeNilControlConn ensures Home returns error when control socket is nil.
func TestHomeNilControlConn(t *testing.T) {
	s := &Session{serial: "test-device"}
	if err := s.Home(); err == nil {
		t.Error("expected error for nil controlConn, got nil")
	}
}

// --- LaunchApp tests ---

// TestLaunchAppWritesStartApp verifies LaunchApp emits a start-app message
// with the package name.
func TestLaunchAppWritesStartApp(t *testing.T) {
	s, reader := newMockSession()
	pkg := "com.example.app"

	done := make(chan error, 1)
	go func() {
		done <- s.LaunchApp(pkg)
	}()

	buf := make([]byte, 1)
	if _, err := io.ReadFull(reader, buf); err != nil {
		t.Fatalf("read type: %v", err)
	}
	if buf[0] != protocol.TypeStartApp {
		t.Fatalf("message type = %d, want %d (start_app)", buf[0], protocol.TypeStartApp)
	}
	if _, err := io.ReadFull(reader, buf); err != nil {
		t.Fatalf("read name length: %v", err)
	}
	name := make([]byte, int(buf[0]))
	if _, err := io.ReadFull(reader, name); err != nil {
		t.Fatalf("read name: %v", err)
	}
	if string(name) != pkg {
		t.Errorf("start-app name = %q, want %q", string(name), pkg)
	}

	if err := <-done; err != nil {
		t.Errorf("LaunchApp() returned error: %v", err)
	}
}

// TestLaunchAppNilControlConn ensures LaunchApp returns error when control
// socket is nil.
func TestLaunchAppNilControlConn(t *testing.T) {
	s := &Session{serial: "test-device"}
	if err := s.LaunchApp("com.example.app"); err == nil {
		t.Error("expected error for nil controlConn, got nil")
	}
}

// --- pfimeGate tests (TypeText state machine) ---

// TestPfimeGateActivatesOnce verifies the state machine's short-circuit: the
// first ensure runs activation, subsequent calls skip it (the performance
// contract that avoids an ADB round-trip per TypeText call).
func TestPfimeGateActivatesOnce(t *testing.T) {
	var g pfimeGate
	calls := 0
	activate := func() error {
		calls++
		return nil
	}
	ran, err := g.ensure(activate)
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if !ran {
		t.Error("first ensure must report ran=true")
	}
	ran, err = g.ensure(activate)
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if ran {
		t.Error("second ensure must report ran=false (already active)")
	}
	if calls != 1 {
		t.Errorf("activate called %d times, want 1 (state machine must short-circuit)", calls)
	}
	if !g.active {
		t.Error("gate not active after successful activation")
	}
}

// TestPfimeGateFailurePropagatesAndRetries verifies a failed activation is
// wrapped with the "pfime set:" prefix, leaves the gate inactive, and a
// later attempt re-runs activation.
func TestPfimeGateFailurePropagatesAndRetries(t *testing.T) {
	var g pfimeGate
	sentinel := fmt.Errorf("adb boom")
	ran, err := g.ensure(func() error { return sentinel })
	if err == nil {
		t.Fatal("ensure returned nil for failed activation")
	}
	if ran {
		t.Error("failed activation must report ran=false")
	}
	if !strings.Contains(err.Error(), "pfime set:") {
		t.Errorf("ensure error = %v, want 'pfime set:' prefix", err)
	}
	if g.active {
		t.Error("gate marked active after failed activation")
	}

	calls := 0
	ran, err = g.ensure(func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("ensure after failure: %v", err)
	}
	if !ran {
		t.Error("activation after failure must report ran=true")
	}
	if calls != 1 {
		t.Errorf("activate called %d times after failure, want 1", calls)
	}
}

// TestPfimeGateReset verifies reset (used when the original IME is restored
// on session close) makes the next ensure re-run activation.
func TestPfimeGateReset(t *testing.T) {
	var g pfimeGate
	calls := 0
	activate := func() error {
		calls++
		return nil
	}
	if _, err := g.ensure(activate); err != nil {
		t.Fatal(err)
	}
	g.reset()
	ran, err := g.ensure(activate)
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("ensure after reset must report ran=true")
	}
	if calls != 2 {
		t.Errorf("activate called %d times after reset, want 2", calls)
	}
}

// --- waitPFIMEBound tests ---

// stubPFIMEProbes overrides the device-probe seams for waitPFIMEBound and
// shrinks its timing vars. Returns a restore func.
func stubPFIMEProbes(t *testing.T, which func(serial string) (string, error), pidOf func(serial string) (string, error)) func() {
	t.Helper()
	oldWhich, oldPidOf := pfimeWhich, pfimePidOf
	oldTimeout, oldPoll, oldGrace, oldFallback := pfimeWaitTimeout, pfimeWaitPoll, pfimeBindGrace, pfimeFallbackWait
	pfimeWhich, pfimePidOf = which, pidOf
	pfimeWaitTimeout, pfimeWaitPoll, pfimeBindGrace, pfimeFallbackWait = 50*time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond
	return func() {
		pfimeWhich, pfimePidOf = oldWhich, oldPidOf
		pfimeWaitTimeout, pfimeWaitPoll, pfimeBindGrace, pfimeFallbackWait = oldTimeout, oldPoll, oldGrace, oldFallback
	}
}

// TestWaitPFIMEBoundFallsBackWithoutPidof: devices without pidof keep the old
// fixed settle and never error — the subsequent broadcast surfaces any
// transport problem.
func TestWaitPFIMEBoundFallsBackWithoutPidof(t *testing.T) {
	defer stubPFIMEProbes(t,
		func(serial string) (string, error) { return "", nil },
		nil,
	)()
	if err := waitPFIMEBound("test-device"); err != nil {
		t.Fatalf("fallback wait: %v", err)
	}
}

// TestWaitPFIMEBoundWaitsForProcess: pidof reports "no match" (non-zero exit
// with empty output) first, then the PID — the wait must poll until the
// process is up and succeed.
func TestWaitPFIMEBoundWaitsForProcess(t *testing.T) {
	calls := 0
	defer stubPFIMEProbes(t,
		func(serial string) (string, error) { return "/system/bin/pidof", nil },
		func(serial string) (string, error) {
			calls++
			if calls < 2 {
				return "", fmt.Errorf("exit status 1") // toybox pidof, no match
			}
			return "1234", nil
		},
	)()
	if err := waitPFIMEBound("test-device"); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if calls != 2 {
		t.Errorf("pidof called %d times, want 2 (poll until up)", calls)
	}
}

// TestWaitPFIMEBoundWhichHangSurfaced: a hung `which` probe (DeadlineExceeded)
// must surface as an error, not be misclassified as "device has no pidof" —
// misclassification would silently fall back to the fixed settle and leave
// the transport hang to surface only at the broadcast, one step later.
func TestWaitPFIMEBoundWhichHangSurfaced(t *testing.T) {
	defer stubPFIMEProbes(t,
		func(serial string) (string, error) {
			return "", fmt.Errorf("adb shell which: timed out: %w", context.DeadlineExceeded)
		},
		nil,
	)()
	err := waitPFIMEBound("test-device")
	if err == nil {
		t.Fatal("expected error for hung which probe, got nil")
	}
	if !strings.Contains(err.Error(), "wait pfime") {
		t.Errorf("error = %v, want 'wait pfime' context", err)
	}
}

// TestWaitPFIMEBoundPropagatesHang: a hung pidof probe (DeadlineExceeded)
// must surface immediately — the caller must know the text was NOT sent,
// because a silent drop + caller retype can double the input.
func TestWaitPFIMEBoundPropagatesHang(t *testing.T) {
	defer stubPFIMEProbes(t,
		func(serial string) (string, error) { return "/system/bin/pidof", nil },
		func(serial string) (string, error) {
			return "", fmt.Errorf("adb shell pidof: timed out: %w", context.DeadlineExceeded)
		},
	)()
	err := waitPFIMEBound("test-device")
	if err == nil {
		t.Fatal("expected error for hung probe, got nil")
	}
	if !strings.Contains(err.Error(), "wait pfime") {
		t.Errorf("error = %v, want 'wait pfime' context", err)
	}
}

// TestWaitPFIMEBoundFailsClosed: if the process never comes up, the wait
// returns an error and no broadcast is sent — safe for the caller to retry
// (nothing was delivered), unlike the old silent-drop behavior.
func TestWaitPFIMEBoundFailsClosed(t *testing.T) {
	defer stubPFIMEProbes(t,
		func(serial string) (string, error) { return "/system/bin/pidof", nil },
		func(serial string) (string, error) { return "", fmt.Errorf("exit status 1") },
	)()
	err := waitPFIMEBound("test-device")
	if err == nil {
		t.Fatal("expected error when pfime never starts, got nil")
	}
	if !strings.Contains(err.Error(), "retry type") {
		t.Errorf("error = %v, want a retry hint", err)
	}
}
