package session

import (
	"errors"
	"fmt"
	"time"

	phonelog "github.com/gezihua123/phonefast/internal/log"
	"github.com/gezihua123/phonefast/pkg/avcodec"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// ErrObserveTimeout is returned by ObserveFull when the 30s watchdog fires.
// The internal goroutines are still joined before returning, bounded by a
// drain grace window (observeDrainGrace) — the production fetchers are
// internally bounded (3s keyframe wait, 5s socket deadlines), so the join
// normally completes well inside the grace and no session use outlives the
// call. A fetcher stuck past the grace is abandoned rather than allowed to
// deadlock the caller.
var ErrObserveTimeout = errors.New("observe full: timed out")

// ObserveFullResult is the outcome of a concurrent screenshot + full UI
// capture.
type ObserveFullResult struct {
	Image    []byte
	Mime     string
	Elements []protocol.UIFullElement
}

// ObserveFull captures a screenshot (in the requested format) and the full
// hierarchical UI dump CONCURRENTLY, with one retry on transient UI errors.
//
// The concurrency lives here, inside the session: a caller (the device
// actor's single-threaded event loop, or the CLI) issues exactly ONE device
// call per observe. The old handler-level goroutines let the actor move on
// to the next request (e.g. disconnect → Close) while an observe goroutine
// was still using the session — a use-after-return hazard this eliminates.
func (s *Session) ObserveFull(maxElements int, format avcodec.ImageFormat) (*ObserveFullResult, error) {
	return s.observeFull(
		func() ([]byte, string, error) {
			// Request JPEG directly from the CGO decoder to avoid the ~4.6MB
			// image.Decode allocation a PNG round-trip would incur.
			img, _, _, mime, err := s.ScreenshotFormat(format)
			if err != nil {
				return nil, "", fmt.Errorf("observe screenshot: %w", err)
			}
			return img, mime, nil
		},
		func() ([]protocol.UIFullElement, error) {
			elems, err := s.GetUIFull(maxElements)
			if err != nil {
				// Retry once on transient errors (stale-node exceptions during
				// animations are common now that waitForIdle is removed).
				elems, err = s.GetUIFull(maxElements)
			}
			if err != nil {
				return nil, fmt.Errorf("observe ui: %w", err)
			}
			return elems, nil
		},
	)
}

// observeFullTimeout bounds each wait in observeFull. Package-level var so
// tests can shrink it (the production fetchers are internally bounded —
// 3s keyframe wait, 5s socket deadlines — so 30s is always enough to drain).
var observeFullTimeout = 30 * time.Second

// observeFullImgRes / observeFullUIRes are the fetcher results, at package
// scope so drainObserveFull can reference them.
type observeFullImgRes struct {
	img  []byte
	mime string
	err  error
}
type observeFullUIRes struct {
	elements []protocol.UIFullElement
	err      error
}

// observeFull is the testable seam for ObserveFull: two fetchers run
// concurrently, each wait has its own 30s watchdog. On watchdog fire the
// remaining goroutines are STILL drained before returning — bounded by a
// grace window (observeDrainGrace, the same bound observe uses), so the
// caller can safely Close the session right after ObserveFull returns, and a
// fetcher hung past the grace cannot deadlock the actor's event loop.
func (s *Session) observeFull(
	fetchImg func() ([]byte, string, error),
	fetchUI func() ([]protocol.UIFullElement, error),
) (*ObserveFullResult, error) {
	imgCh := make(chan observeFullImgRes, 1)
	uiCh := make(chan observeFullUIRes, 1)

	go func() {
		img, mime, err := fetchImg()
		imgCh <- observeFullImgRes{img, mime, err}
	}()
	go func() {
		elems, err := fetchUI()
		uiCh <- observeFullUIRes{elems, err}
	}()

	var img observeFullImgRes
	select {
	case img = <-imgCh:
	case <-time.After(observeFullTimeout):
		// Drain both goroutines before returning — see doc comment.
		drainObserveFull(imgCh, uiCh, true, true)
		return nil, ErrObserveTimeout
	}

	var ui observeFullUIRes
	select {
	case ui = <-uiCh:
	case <-time.After(observeFullTimeout):
		drainObserveFull(imgCh, uiCh, false, true)
		return nil, ErrObserveTimeout
	}

	if img.err != nil {
		return nil, img.err
	}
	if ui.err != nil {
		return nil, ui.err
	}
	return &ObserveFullResult{Image: img.img, Mime: img.mime, Elements: ui.elements}, nil
}

// drainObserveFull joins the fetcher goroutines flagged by waitImg/waitUI,
// bounded by observeDrainGrace (mirroring observe's drain in control.go).
// The grace bound matters: a fetcher stuck past it (e.g. a hung screenshot)
// must not block the caller indefinitely — returning keeps the timeout
// contract. Both channels are buffered (cap 1), so an abandoned straggler
// never blocks on send; it is collected whenever it eventually returns.
func drainObserveFull(imgCh <-chan observeFullImgRes, uiCh <-chan observeFullUIRes, waitImg, waitUI bool) {
	grace := time.NewTimer(observeDrainGrace)
	defer grace.Stop()
	for waitImg || waitUI {
		select {
		case <-imgCh:
			waitImg = false
		case <-uiCh:
			waitUI = false
		case <-grace.C:
			phonelog.Default().Write("observe full: fetcher still running past %v drain grace (img pending=%v ui pending=%v)",
				observeDrainGrace, waitImg, waitUI)
			return
		}
	}
}
