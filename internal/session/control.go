package session

import (
	"fmt"
	"time"

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
func (s *Session) TypeText(text string) error {
	if s.controlConn == nil {
		return fmt.Errorf("control socket not available")
	}

	msg := protocol.NewTextMsg(text)
	_, err := s.controlConn.Write(msg.Encode())
	return err
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

// Observe captures both a screenshot and UI hierarchy in parallel.
func (s *Session) Observe() (screenshot []byte, uiElements []protocol.UIElement, err error) {
	type result struct {
		pngData   []byte
		w, h      int
		elements  []protocol.UIElement
		screenErr error
		uiErr     error
	}

	ch := make(chan result, 1)

	go func() {
		var r result
		r.pngData, r.w, r.h, r.screenErr = s.Screenshot()

		// Try fast socket UI dump first, fallback to ADB
		elems, uiErr := s.GetUIElements()
		if uiErr != nil {
			elems, uiErr = s.GetUIElementsFallbackADB()
		}
		r.elements = elems
		r.uiErr = uiErr

		ch <- r
	}()

	// Wait with timeout
	select {
	case r := <-ch:
		if r.screenErr != nil {
			return nil, nil, fmt.Errorf("screenshot: %w", r.screenErr)
		}
		// UI errors are non-fatal — elements may be empty
		return r.pngData, r.elements, nil
	case <-time.After(5 * time.Second):
		return nil, nil, fmt.Errorf("observe timeout")
	}
}
