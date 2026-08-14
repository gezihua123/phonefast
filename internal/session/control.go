package session

import (
	"fmt"
	"time"

	"github.com/gezihua123/phonefast/internal/adb"
	phonelog "github.com/gezihua123/phonefast/internal/log"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// --- High-level device control operations ---

// Tap taps at the specified screen coordinates.
func (s *Session) Tap(x, y int) error {
	if s.controlConn == nil {
		return fmt.Errorf("control socket not available")
	}

	w := uint16(s.deviceW)
	h := uint16(s.deviceH)

	// Touch down
	down := protocol.NewTouchMsg(protocol.ActionDown, int32(x), int32(y), w, h)
	if _, err := s.controlConn.Write(down.Encode()); err != nil {
		return fmt.Errorf("tap down: %w", err)
	}

	delay := s.TapDelay
	if delay <= 0 {
		delay = defaultTapDelay
	}
	time.Sleep(delay)

	// Touch up
	up := protocol.NewTouchMsg(protocol.ActionUp, int32(x), int32(y), w, h)
	if _, err := s.controlConn.Write(up.Encode()); err != nil {
		return fmt.Errorf("tap up: %w", err)
	}

	// Preheat the next screenshot's keyframe (see preheatKeyframe).
	s.preheatKeyframe()
	return nil
}

// Swipe performs a swipe gesture from (x1, y1) to (x2, y2).
func (s *Session) Swipe(x1, y1, x2, y2, durationMs int) error {
	if s.controlConn == nil {
		return fmt.Errorf("control socket not available")
	}

	w := uint16(s.deviceW)
	h := uint16(s.deviceH)

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
		if _, err := s.controlConn.Write(move.Encode()); err != nil {
			return fmt.Errorf("swipe move: %w", err)
		}
	}

	time.Sleep(stepInterval)

	// Touch up at end
	up := protocol.NewTouchMsg(protocol.ActionUp, int32(x2), int32(y2), w, h)
	if _, err := s.controlConn.Write(up.Encode()); err != nil {
		return fmt.Errorf("swipe up: %w", err)
	}

	// Preheat the next screenshot's keyframe (see preheatKeyframe).
	s.preheatKeyframe()
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

	// Preheat the next screenshot's keyframe (see preheatKeyframe).
	s.preheatKeyframe()
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
	if _, err := s.controlConn.Write(up.Encode()); err != nil {
		return err
	}
	// Preheat the next screenshot's keyframe (see preheatKeyframe).
	s.preheatKeyframe()
	return nil
}

// Home presses the home button.
func (s *Session) Home() error {
	return s.PressKey(protocol.KeycodeHome)
}

// pfimeGate encapsulates the "activate PFIME once per session" state
// machine. activate runs on the first ensure call only; subsequent calls
// are no-ops. Extracted from TypeText so the state machine is testable
// without ADB.
type pfimeGate struct {
	active bool
}

// ensure runs activate on the first call only. Returns activate's error
// wrapped with the "pfime set:" prefix. Not safe for concurrent use —
// the session owns it on a single goroutine.
func (g *pfimeGate) ensure(activate func() error) error {
	if g.active {
		return nil
	}
	if err := activate(); err != nil {
		return fmt.Errorf("pfime set: %w", err)
	}
	g.active = true
	return nil
}

// reset clears the gate so the next ensure re-runs activation (used when
// the original IME is restored on session close).
func (g *pfimeGate) reset() { g.active = false }

// TypeText types text into the currently focused field.
//
// All text goes through the PhoneFast IME (PFIME), which commits via
// broadcast straight into the focused field's InputConnection. This is the
// only path that is reliable regardless of soft-keyboard state:
//
//   - scrcpy INJECT_TEXT (control socket) drops/loses chunks while the soft
//     keyboard is open — the IME's predictive input interferes with injected
//     text ("Spicy Tuna Wrapsyy", or truncated input on emulators).
//   - Hiding the keyboard first is not dependable: keyevent 111 (ESCAPE) is
//     ignored by many IMEs (AOSP on API 33 emulators), and BACK would leave
//     the page when no keyboard is showing.
//   - Switching to PFIME itself hides the soft keyboard (IME switch tears it
//     down), and the broadcast commit does not depend on keyboard visibility.
//
// Cost: one ADB round-trip per call (~50-100ms) vs <10ms for INJECT_TEXT.
// Correctness wins — typing with a visible keyboard is the common case in
// agent flows (tap to focus, then type), and lost/duplicated characters
// fail the whole task.
func (s *Session) TypeText(text string) error {
	// Skip SetPFIME if already active (avoids ADB round-trip on every keystroke).
	firstActivation := !s.pfime.active
	if err := s.pfime.ensure(func() error { return adb.SetPFIME(s.serial) }); err != nil {
		return err
	}
	if firstActivation {
		// On the first type of a session the IME switch/startup tears down and
		// re-binds the InputConnection; a broadcast sent in that window is
		// silently dropped by PFIME (it has no connection to commit into).
		// Give the bind a beat before the first broadcast. Subsequent calls
		// skip this — the connection is already established.
		time.Sleep(300 * time.Millisecond)
	}
	if err := adb.TypeTextB64(s.serial, text); err != nil {
		return fmt.Errorf("pfime type: %w", err)
	}
	// Preheat the next screenshot's keyframe: the IDR arrives ~300ms later,
	// after the IME commit renders — so the follow-up screenshot's fast path
	// sees the typed text (see preheatKeyframe).
	s.preheatKeyframe()
	return nil
}

// LaunchApp launches an app by package name.
func (s *Session) LaunchApp(packageName string) error {
	if s.controlConn == nil {
		return fmt.Errorf("control socket not available")
	}

	msg := protocol.NewStartAppMsg(packageName)
	if _, err := s.controlConn.Write(msg.Encode()); err != nil {
		return err
	}
	// Preheat the next screenshot's keyframe (see preheatKeyframe).
	s.preheatKeyframe()
	return nil
}

// Scroll performs a scroll at the specified position.

// observeTimeout and observeDrainGrace bound Observe's collect and drain
// phases. Package-level vars so tests can shrink them (same pattern as
// observeFullTimeout) — in production the fetchers are internally bounded
// (3s keyframe wait, 5s socket deadlines), so 5s is always enough to drain.
var (
	observeTimeout    = 5 * time.Second
	observeDrainGrace = 5 * time.Second
)

// Observe captures both a screenshot and UI hierarchy concurrently, then
// waits for both to complete (or a 5s timeout). Running them in separate
// goroutines cuts wall-clock time to the slower of the two operations
// (instead of their sum), which matters when the UI dump falls back to
// the slow ADB uiautomator path (~2-3s).
//
// maxElements controls the collection limit on the device side:
//   - > 0: request that many elements (capped at 500 by the server)
//   - <= 0: use server default (500 for full, 100 for summary)
//
// summary filters out layout containers, returning only meaningful elements.
func (s *Session) Observe(maxElements int, summary bool) (screenshot []byte, uiElements []protocol.UIElement, err error) {
	return s.observe(
		func() ([]byte, error) {
			png, _, _, err := s.Screenshot()
			return png, err
		},
		func() ([]protocol.UIElement, error) {
			// Flat path always fetches summary-mode elements (layout containers
			// and pure images filtered server-side). The `summary` param only
			// affects downstream formatting (viewport collapse), not the fetch.
			elems, uiErr := s.GetUISummary(maxElements)
			if uiErr != nil {
				elems, uiErr = s.GetUIElementsFallbackADB(maxElements)
			}
			return elems, uiErr
		},
	)
}

// observe is the testable seam for Observe: two fetchers run concurrently
// with an overall timeout, then a bounded grace drain. UI errors are
// non-fatal (the screenshot is still returned with whatever elements were
// collected) — see Observe's contract.
func (s *Session) observe(
	fetchScreen func() ([]byte, error),
	fetchUI func() ([]protocol.UIElement, error),
) (screenshot []byte, uiElements []protocol.UIElement, err error) {
	type screenResult struct {
		pngData []byte
		err     error
	}
	type uiResult struct {
		elements []protocol.UIElement
		err      error
	}

	screenCh := make(chan screenResult, 1)
	uiCh := make(chan uiResult, 1)

	go func() {
		png, err := fetchScreen()
		screenCh <- screenResult{png, err}
	}()

	go func() {
		elems, uiErr := fetchUI()
		uiCh <- uiResult{elems, uiErr}
	}()

	// Collect results with overall timeout.
	var screen screenResult
	var ui uiResult
	gotScreen, gotUI := false, false
	timer := time.NewTimer(observeTimeout)
	defer timer.Stop()

collect:
	for i := 0; i < 2; i++ {
		select {
		case screen = <-screenCh:
			gotScreen = true
		case ui = <-uiCh:
			gotUI = true
		case <-timer.C:
			// Wait for the spawned goroutines so they cannot keep using the
			// session after Observe returns (the caller may Close() it next).
			// Bounded by a grace window: the session calls have internal
			// bounds (3s keyframe wait, 5s socket deadlines), but a call
			// stuck past the grace window must not block the caller
			// indefinitely — returning keeps the timeout contract.
			grace := time.NewTimer(observeDrainGrace)
			defer grace.Stop()
			for !gotScreen || !gotUI {
				select {
				case screen = <-screenCh:
					gotScreen = true
				case ui = <-uiCh:
					gotUI = true
				case <-grace.C:
					phonelog.Default().Write("observe: timeout, goroutine still running (screen=%v ui=%v)",
						gotScreen, gotUI)
					return nil, nil, fmt.Errorf("observe timeout")
				}
			}
			// Both results arrived within the grace window — the data is
			// valid, so return it instead of discarding a successful capture.
			break collect
		}
	}

	if screen.err != nil {
		return nil, nil, fmt.Errorf("screenshot: %w", screen.err)
	}
	// UI errors are non-fatal — elements may be empty
	return screen.pngData, ui.elements, nil
}
