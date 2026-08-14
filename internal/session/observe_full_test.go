package session

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gezihua123/phonefast/pkg/avcodec"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

func TestObserveFullBothSuccess(t *testing.T) {
	s := &Session{}
	res, err := s.observeFull(
		func() ([]byte, string, error) { return []byte("img"), "image/jpeg", nil },
		func() ([]protocol.UIFullElement, error) { return nil, nil },
	)
	if err != nil {
		t.Fatalf("observeFull: %v", err)
	}
	if string(res.Image) != "img" || res.Mime != "image/jpeg" {
		t.Errorf("result = (%q, %q), want (img, image/jpeg)", res.Image, res.Mime)
	}
}

func TestObserveFullScreenshotError(t *testing.T) {
	s := &Session{}
	_, err := s.observeFull(
		func() ([]byte, string, error) { return nil, "", errors.New("shot failed") },
		func() ([]protocol.UIFullElement, error) { return nil, nil },
	)
	if err == nil || err.Error() != "shot failed" {
		t.Errorf("err = %v, want \"shot failed\"", err)
	}
}

func TestObserveFullUIError(t *testing.T) {
	s := &Session{}
	_, err := s.observeFull(
		func() ([]byte, string, error) { return []byte("img"), "image/jpeg", nil },
		func() ([]protocol.UIFullElement, error) { return nil, errors.New("ui failed") },
	)
	if err == nil || err.Error() != "ui failed" {
		t.Errorf("err = %v, want \"ui failed\"", err)
	}
}

// TestObserveFullRetriesTransientUIError exercises the PUBLIC ObserveFull
// wrapper (not the observeFull seam): a transient GetUIFull failure must be
// retried exactly once, and the result must carry the screenshot bytes/mime
// from ScreenshotFormat plus the elements from the second UI call.
func TestObserveFullRetriesTransientUIError(t *testing.T) {
	s := &Session{}
	s.screenshotFormatFn = func(format avcodec.ImageFormat) ([]byte, int, int, string, error) {
		return []byte("jpeg"), 100, 200, "image/jpeg", nil
	}
	uiCalls := 0
	s.getUIFullFn = func(maxElements int) ([]protocol.UIFullElement, error) {
		uiCalls++
		if uiCalls == 1 {
			return nil, errors.New("stale node")
		}
		return []protocol.UIFullElement{{ID: 1, Text: "ok"}}, nil
	}

	res, err := s.ObserveFull(0, avcodec.FormatJPEG)
	if err != nil {
		t.Fatalf("ObserveFull: %v", err)
	}
	if uiCalls != 2 {
		t.Errorf("GetUIFull called %d times, want exactly 2 (one retry)", uiCalls)
	}
	if string(res.Image) != "jpeg" || res.Mime != "image/jpeg" {
		t.Errorf("result image = (%q, %q), want (jpeg, image/jpeg)", res.Image, res.Mime)
	}
	if len(res.Elements) != 1 || res.Elements[0].Text != "ok" {
		t.Errorf("result elements = %+v, want the second call's [ok]", res.Elements)
	}
}

// TestObserveFullUIErrorWrappedAfterRetry verifies that when BOTH UI calls
// fail, ObserveFull surfaces the second error wrapped with "observe ui:".
func TestObserveFullUIErrorWrappedAfterRetry(t *testing.T) {
	s := &Session{}
	s.screenshotFormatFn = func(format avcodec.ImageFormat) ([]byte, int, int, string, error) {
		return []byte("jpeg"), 100, 200, "image/jpeg", nil
	}
	uiCalls := 0
	s.getUIFullFn = func(maxElements int) ([]protocol.UIFullElement, error) {
		uiCalls++
		return nil, errors.New("ui boom")
	}

	_, err := s.ObserveFull(0, avcodec.FormatJPEG)
	if err == nil || !strings.Contains(err.Error(), "observe ui: ui boom") {
		t.Fatalf("err = %v, want wrapped \"observe ui: ui boom\"", err)
	}
	if uiCalls != 2 {
		t.Errorf("GetUIFull called %d times, want exactly 2 (initial + one retry)", uiCalls)
	}
}

// TestObserveFullScreenshotErrorWrapped verifies the wrapper prefixes the
// screenshot failure with "observe screenshot:".
func TestObserveFullScreenshotErrorWrapped(t *testing.T) {
	s := &Session{}
	s.screenshotFormatFn = func(format avcodec.ImageFormat) ([]byte, int, int, string, error) {
		return nil, 0, 0, "", errors.New("shot boom")
	}
	s.getUIFullFn = func(maxElements int) ([]protocol.UIFullElement, error) {
		return nil, nil
	}

	_, err := s.ObserveFull(0, avcodec.FormatJPEG)
	if err == nil || !strings.Contains(err.Error(), "observe screenshot: shot boom") {
		t.Fatalf("err = %v, want wrapped \"observe screenshot: shot boom\"", err)
	}
}

// TestObserveFullTimeoutDrains verifies the use-after-return fix: when the
// watchdog fires, observeFull STILL joins the slow goroutine before
// returning — the caller can Close the session immediately after without a
// straggler goroutine still using it.
func TestObserveFullTimeoutDrains(t *testing.T) {
	old := observeFullTimeout
	observeFullTimeout = 50 * time.Millisecond
	defer func() { observeFullTimeout = old }()

	s := &Session{}
	start := time.Now()
	_, err := s.observeFull(
		func() ([]byte, string, error) { return []byte("img"), "image/jpeg", nil },
		func() ([]protocol.UIFullElement, error) {
			time.Sleep(250 * time.Millisecond) // bounded "hang" longer than the watchdog
			return nil, nil
		},
	)
	if !errors.Is(err, ErrObserveTimeout) {
		t.Fatalf("err = %v, want ErrObserveTimeout", err)
	}
	// Returned at ~250ms (drained the slow fetcher), not ~50ms (bare timeout).
	if el := time.Since(start); el < 200*time.Millisecond {
		t.Errorf("returned after %v — slow goroutine was not drained", el)
	}
}

// TestObserveFullImgTimeoutDrains covers the FIRST-select watchdog (the img
// fetcher hangs while UI returns immediately): observeFull must still join
// the slow img goroutine before returning ErrObserveTimeout.
func TestObserveFullImgTimeoutDrains(t *testing.T) {
	old := observeFullTimeout
	observeFullTimeout = 50 * time.Millisecond
	defer func() { observeFullTimeout = old }()

	s := &Session{}
	start := time.Now()
	_, err := s.observeFull(
		func() ([]byte, string, error) {
			time.Sleep(250 * time.Millisecond) // bounded "hang" longer than the watchdog
			return []byte("img"), "image/jpeg", nil
		},
		func() ([]protocol.UIFullElement, error) { return nil, nil },
	)
	if !errors.Is(err, ErrObserveTimeout) {
		t.Fatalf("err = %v, want ErrObserveTimeout", err)
	}
	// Returned at ~250ms (drained the slow fetcher), not ~50ms (bare timeout).
	if el := time.Since(start); el < 200*time.Millisecond {
		t.Errorf("returned after %v — slow img goroutine was not drained", el)
	}
}

// TestObserveFullDrainGraceBounded verifies a fetcher stuck FOREVER does not
// deadlock the caller (the actor's single-threaded event loop): once the
// drain grace expires, observeFull returns ErrObserveTimeout without the
// straggler. The straggler's channel is buffered, so its send never blocks
// and the goroutine is collected whenever it eventually returns.
func TestObserveFullDrainGraceBounded(t *testing.T) {
	oldTO, oldGrace := observeFullTimeout, observeDrainGrace
	observeFullTimeout, observeDrainGrace = 50*time.Millisecond, 50*time.Millisecond
	defer func() { observeFullTimeout, observeDrainGrace = oldTO, oldGrace }()

	never := make(chan struct{}) // never closed — the img fetcher hangs forever
	s := &Session{}
	start := time.Now()
	_, err := s.observeFull(
		func() ([]byte, string, error) {
			<-never
			return nil, "", nil
		},
		func() ([]protocol.UIFullElement, error) { return nil, nil },
	)
	if !errors.Is(err, ErrObserveTimeout) {
		t.Fatalf("err = %v, want ErrObserveTimeout", err)
	}
	// Watchdog (~50ms) + grace (~50ms) — must NOT hang on the stuck fetcher.
	if el := time.Since(start); el > 500*time.Millisecond {
		t.Errorf("returned after %v — drain grace did not bound the join", el)
	}
}
