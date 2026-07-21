package session

import (
	"fmt"
	"time"

	"github.com/gezihua123/phonefast/internal/adb"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// --- High-level device control operations ---

// Tap taps at the specified screen coordinates.
func (s *Session) Tap(x, y int) error {
	if s.controlConn == nil {
		return fmt.Errorf("control socket not available")
	}

	w := uint16(s.DeviceW)
	h := uint16(s.DeviceH)

	// Touch down
	down := protocol.NewTouchMsg(protocol.ActionDown, int32(x), int32(y), w, h)
	if _, err := s.controlConn.Write(down.Encode()); err != nil {
		return fmt.Errorf("tap down: %w", err)
	}

	delay := s.TapDelay
	if delay <= 0 {
		delay = 10 * time.Millisecond
	}
	time.Sleep(delay)

	// Touch up
	up := protocol.NewTouchMsg(protocol.ActionUp, int32(x), int32(y), w, h)
	if _, err := s.controlConn.Write(up.Encode()); err != nil {
		return fmt.Errorf("tap up: %w", err)
	}

	return nil
}

// Swipe performs a swipe gesture from (x1, y1) to (x2, y2).
func (s *Session) Swipe(x1, y1, x2, y2, durationMs int) error {
	if s.controlConn == nil {
		return fmt.Errorf("control socket not available")
	}

	w := uint16(s.DeviceW)
	h := uint16(s.DeviceH)

	// Touch down at start
	down := protocol.NewTouchMsg(protocol.ActionDown, int32(x1), int32(y1), w, h)
	if _, err := s.controlConn.Write(down.Encode()); err != nil {
		return fmt.Errorf("swipe down: %w", err)
	}

	// Move through intermediate points
	steps := 10
	stepInterval := time.Duration(durationMs/steps) * time.Millisecond
	for i := 1; i < steps; i++ {
		time.Sleep(stepInterval)
		fx := int32(x1 + (x2-x1)*i/steps)
		fy := int32(y1 + (y2-y1)*i/steps)
		move := protocol.NewTouchMsg(protocol.ActionMove, fx, fy, w, h)
		s.controlConn.Write(move.Encode())
	}

	time.Sleep(stepInterval)

	// Touch up at end
	up := protocol.NewTouchMsg(protocol.ActionUp, int32(x2), int32(y2), w, h)
	if _, err := s.controlConn.Write(up.Encode()); err != nil {
		return fmt.Errorf("swipe up: %w", err)
	}

	return nil
}

// PressKey sends a key event to the device.
func (s *Session) PressKey(keycode int) error {
	if s.controlConn == nil {
		return fmt.Errorf("control socket not available")
	}

	// Key down
	down := protocol.NewKeycodeMsg(protocol.KeyEventActionDown, keycode)
	if _, err := s.controlConn.Write(down.Encode()); err != nil {
		return fmt.Errorf("key down: %w", err)
	}

	time.Sleep(10 * time.Millisecond)

	// Key up
	up := protocol.NewKeycodeMsg(protocol.KeyEventActionUp, keycode)
	if _, err := s.controlConn.Write(up.Encode()); err != nil {
		return fmt.Errorf("key up: %w", err)
	}

	return nil
}

// Back presses the back button.
// TypeBackOrScreenOn requires both ACTION_DOWN (0) and ACTION_UP (1) —
// the server injects them verbatim into Android, which triggers back on UP.
func (s *Session) Back() error {
	if s.controlConn == nil {
		return fmt.Errorf("control socket not available")
	}

	down := &protocol.ControlMessage{Type: protocol.TypeBackOrScreenOn, ActionBack: 0}
	if _, err := s.controlConn.Write(down.Encode()); err != nil {
		return err
	}

	time.Sleep(10 * time.Millisecond)

	up := &protocol.ControlMessage{Type: protocol.TypeBackOrScreenOn, ActionBack: 1}
	_, err := s.controlConn.Write(up.Encode())
	return err
}

// Home presses the home button.
func (s *Session) Home() error {
	return s.PressKey(protocol.KeycodeHome)
}

// TypeText types text into the currently focused field.
// For ASCII-only text, uses the fast scrcpy control socket path (<10ms).
// For non-ASCII (CJK/emoji), switches to PhoneFast IME and commits via broadcast.
func (s *Session) TypeText(text string) error {
	// ASCII fast path via scrcpy control socket
	if adb.IsASCII(text) {
		if s.controlConn == nil {
			return fmt.Errorf("control socket not available")
		}
		msg := protocol.NewTextMsg(text)
		_, err := s.controlConn.Write(msg.Encode())
		return err
	}

	// Unicode path via PFIME IME broadcast.
	// Skip SetPFIME if already active (avoids ADB round-trip on every keystroke).
	if !s.pfimeActive {
		if err := adb.SetPFIME(s.Serial); err != nil {
			return fmt.Errorf("pfime set: %w", err)
		}
		s.pfimeActive = true
	}
	if err := adb.TypeTextB64(s.Serial, text); err != nil {
		return fmt.Errorf("pfime type: %w", err)
	}
	return nil
}

// LaunchApp launches an app by package name.
func (s *Session) LaunchApp(packageName string) error {
	if s.controlConn == nil {
		return fmt.Errorf("control socket not available")
	}

	msg := protocol.NewStartAppMsg(packageName)
	_, err := s.controlConn.Write(msg.Encode())
	return err
}

// Scroll performs a scroll at the specified position.
func (s *Session) Scroll(x, y int, hScroll, vScroll float32) error {
	if s.controlConn == nil {
		return fmt.Errorf("control socket not available")
	}

	w := uint16(s.DeviceW)
	h := uint16(s.DeviceH)

	msg := protocol.NewScrollMsg(int32(x), int32(y), w, h, hScroll, vScroll)
	_, err := s.controlConn.Write(msg.Encode())
	return err
}

// Observe captures both a screenshot and UI hierarchy concurrently, then
// waits for both to complete (or a 5s timeout). Running them in separate
// goroutines cuts wall-clock time to the slower of the two operations
// (instead of their sum), which matters when the UI dump falls back to
// the slow ADB uiautomator path (~2-3s).
//
// maxElements controls the collection limit on the device side:
//   - > 0: request that many elements (capped at 500 by the server)
//   - <= 0: use server default (500 for full, 100 for summary)
// summary filters out layout containers, returning only meaningful elements.
func (s *Session) Observe(maxElements int, summary bool) (screenshot []byte, uiElements []protocol.UIElement, err error) {
	// Launch screenshot and UI dump concurrently in separate goroutines.
	type screenResult struct {
		pngData []byte
		w, h    int
		err     error
	}
	type uiResult struct {
		elements []protocol.UIElement
		err      error
	}

	screenCh := make(chan screenResult, 1)
	uiCh := make(chan uiResult, 1)

	go func() {
		png, w, h, err := s.Screenshot()
		screenCh <- screenResult{png, w, h, err}
	}()

	go func() {
		var elems []protocol.UIElement
		var uiErr error
		if summary {
			elems, uiErr = s.GetUISummary(maxElements)
		} else {
			elems, uiErr = s.GetUIElements(maxElements)
		}
		if uiErr != nil {
			elems, uiErr = s.GetUIElementsFallbackADB(maxElements)
		}
		uiCh <- uiResult{elems, uiErr}
	}()

	// Collect results with overall timeout.
	var screen screenResult
	var ui uiResult
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	for i := 0; i < 2; i++ {
		select {
		case screen = <-screenCh:
		case ui = <-uiCh:
		case <-timer.C:
			return nil, nil, fmt.Errorf("observe timeout")
		}
	}

	if screen.err != nil {
		return nil, nil, fmt.Errorf("screenshot: %w", screen.err)
	}
	// UI errors are non-fatal — elements may be empty
	return screen.pngData, ui.elements, nil
}
