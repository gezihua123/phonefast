package session

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// TestObserveBothSuccess verifies the concurrent screenshot + UI capture
// returns both payloads.
func TestObserveBothSuccess(t *testing.T) {
	s := &Session{}
	png, elems, err := s.observe(
		func() ([]byte, error) { return []byte("png-bytes"), nil },
		func() ([]protocol.UIElement, error) {
			return []protocol.UIElement{{Index: 1, Text: "hello"}}, nil
		},
	)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if string(png) != "png-bytes" {
		t.Errorf("screenshot = %q, want %q", png, "png-bytes")
	}
	if len(elems) != 1 || elems[0].Text != "hello" {
		t.Errorf("elements = %+v, want [hello]", elems)
	}
}

// TestObserveScreenshotError verifies a screenshot failure wraps with the
// "screenshot:" prefix.
func TestObserveScreenshotError(t *testing.T) {
	s := &Session{}
	_, _, err := s.observe(
		func() ([]byte, error) { return nil, errors.New("shot failed") },
		func() ([]protocol.UIElement, error) { return nil, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "screenshot:") {
		t.Fatalf("err = %v, want wrapped 'screenshot:' error", err)
	}
}

// TestObserveUIErrorNonFatal verifies the UI-error contract: the screenshot
// is still returned (nil error) even when the UI fetch fails.
func TestObserveUIErrorNonFatal(t *testing.T) {
	s := &Session{}
	png, elems, err := s.observe(
		func() ([]byte, error) { return []byte("png-bytes"), nil },
		func() ([]protocol.UIElement, error) { return nil, errors.New("ui failed") },
	)
	if err != nil {
		t.Fatalf("observe returned error for non-fatal UI failure: %v", err)
	}
	if string(png) != "png-bytes" {
		t.Errorf("screenshot = %q, want %q (must survive UI failure)", png, "png-bytes")
	}
	if elems != nil {
		t.Errorf("elements = %v, want nil when UI fetch failed", elems)
	}
}

// TestObserveTimeoutDrains verifies the use-after-return fix: when the
// watchdog fires, observe STILL joins the slow goroutine before returning —
// the caller can Close the session immediately after without a straggler
// goroutine still using it.
func TestObserveTimeoutDrains(t *testing.T) {
	old := observeTimeout
	observeTimeout = 50 * time.Millisecond
	defer func() { observeTimeout = old }()

	s := &Session{}
	start := time.Now()
	png, elems, err := s.observe(
		func() ([]byte, error) { return []byte("png-bytes"), nil },
		func() ([]protocol.UIElement, error) {
			time.Sleep(250 * time.Millisecond) // bounded "hang" longer than the watchdog
			return nil, nil
		},
	)
	// The watchdog fired at 50ms but both results arrived within the drain
	// grace — the valid capture must be RETURNED, not discarded.
	if err != nil {
		t.Fatalf("err = %v, want nil (results arrived within grace)", err)
	}
	if string(png) != "png-bytes" {
		t.Errorf("png = %q, want %q", png, "png-bytes")
	}
	_ = elems
	// Returned at ~250ms (drained the slow fetcher), not ~50ms (bare timeout).
	if el := time.Since(start); el < 200*time.Millisecond {
		t.Errorf("returned after %v — slow goroutine was not drained", el)
	}
}

// TestObserveGraceExpiryReturnsWithoutHanging verifies that a fetcher stuck
// past the drain grace window does not block the caller: observe returns
// the timeout error once the grace window expires.
func TestObserveGraceExpiryReturnsWithoutHanging(t *testing.T) {
	oldTO, oldGrace := observeTimeout, observeDrainGrace
	observeTimeout, observeDrainGrace = 50*time.Millisecond, 50*time.Millisecond
	defer func() {
		observeTimeout, observeDrainGrace = oldTO, oldGrace
	}()

	s := &Session{}
	start := time.Now()
	_, _, err := s.observe(
		func() ([]byte, error) { return []byte("png-bytes"), nil },
		func() ([]protocol.UIElement, error) {
			time.Sleep(500 * time.Millisecond) // longer than timeout + grace
			return nil, nil
		},
	)
	if err == nil || err.Error() != "observe timeout" {
		t.Fatalf("err = %v, want 'observe timeout'", err)
	}
	el := time.Since(start)
	// Watchdog (~50ms) + grace (~50ms) — must NOT wait for the full 500ms.
	if el > 300*time.Millisecond {
		t.Errorf("returned after %v — grace window did not bound the drain", el)
	}
}
