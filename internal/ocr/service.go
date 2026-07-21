// Package ocr provides a daemon-level OCR service that recognizes text
// in images and returns text with positions (bounding boxes).
//
// The service is a singleton held by the daemon — models load ONCE and
// are shared across all device sessions. This avoids the 3.7s model
// loading + 12MB memory cost per-session.
//
// A mutex serializes Recognize() calls for safety (costs ~1μs vs 70ms
// inference = zero impact). OCR is stateless — no per-session state
// (unlike avcodec.Decoder which holds video stream parameters).
package ocr

import (
	"sync"

	"github.com/gezihua123/phonefast/pkg/ocr"
)

// Config holds OCR service settings.
type Config struct {
	// Engine selects the recognition backend: "onnx" (default) or "ncnn".
	// ncnn requires building with the `ncnn` build tag (see internal/ocr/ncnn)
	// and the PHONEFAST_NCNN_PARAM / PHONEFAST_NCNN_BIN env vars pointing at
	// the converted model. Empty string defaults to "onnx".
	Engine string
	// UseVision enables macOS Vision text detection on the ANE (detection
	// fast path, <1ms vs ~35ms ONNX det). Ignored on non-macOS / non-CGO.
	UseVision bool
}

// Service is a daemon-level singleton that wraps an OCR engine.
// It initializes the engine lazily (first Recognize call) and caches
// any init error so the expensive model loading is never retried.
//
// The mutex serializes Recognize() calls — concurrent device actors
// calling OCR at the same time are safely serialized. This costs
// ~1μs (mutex lock/unlock) vs ~170ms (model inference) = negligible.
type Service struct {
	mu      sync.Mutex
	eng     ocr.Engine // lazily initialized, nil until first use
	initErr error      // cached init error — don't retry if failed once
	cfg     Config
}

// NewService returns an uninitialized OCR service. The engine is loaded
// lazily on the first Recognize() call.
func NewService(cfg Config) *Service {
	return &Service{cfg: cfg}
}

// Recognize performs OCR on the given PNG image bytes, returning
// recognized text regions with their positions.
//
// Thread-safe: concurrent calls are serialized via mutex.
// Returns ocr.ErrNotAvailable if the engine cannot be initialized
// (models not downloaded, runtime library missing, or an ncnn build
// without the ncnn tag / model paths).
func (s *Service) Recognize(pngImage []byte) ([]ocr.TextResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	eng, err := s.getEngine()
	if err != nil {
		return nil, err
	}
	return eng.Recognize(pngImage)
}

// Close releases the OCR engine resources. Called on daemon shutdown.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng != nil {
		err := s.eng.Close()
		s.eng = nil
		return err
	}
	return nil
}

// getEngine lazily initializes the configured OCR engine, caching any init
// error. Caller must hold s.mu.
func (s *Service) getEngine() (ocr.Engine, error) {
	if s.eng != nil {
		return s.eng, nil
	}
	if s.initErr != nil {
		return nil, s.initErr
	}
	eng, err := newEngine(s.cfg)
	if err != nil {
		s.initErr = err // cache — don't retry
		return nil, err
	}
	s.eng = eng
	return eng, nil
}

// warmupPNG is a minimal 1×1 PNG used to trigger engine initialization
// (ONNX model load +, on macOS, Vision ANE CoreML compilation) before the
// first real request. The pixels carry no text, so detection returns empty
// and recognition is not exercised — the goal is just to pay the one-time
// init cost, not to warm the rec path.
var warmupPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
	0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, // IDAT
	0x54, 0x08, 0xD7, 0x63, 0x68, 0x00, 0x00, 0x00,
	0x02, 0x00, 0x01, 0xE2, 0x21, 0xBC, 0x33, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, // IEND
	0x42, 0x60, 0x82,
}

// Warmup eagerly-initializes the engine by running a no-op recognize. It owns
// the warmup strategy (synthetic input, which path to exercise), so callers
// (the daemon) don't need to know engine internals. On init failure it does
// NOT poison the Service: the error is reported but not cached, so a later
// real request re-attempts init (and only then caches a persistent failure).
// Safe to run in a goroutine — it serializes on s.mu like Recognize.
func (s *Service) Warmup() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng != nil {
		// Already initialized; just touch Vision/ANE once more is unnecessary.
		return nil
	}
	eng, err := newEngine(s.cfg)
	if err != nil {
		// Report but don't cache: a dev build without models, or a transient
		// lib load hiccup, shouldn't permanently disable OCR. A real request
		// will retry and cache if the failure persists.
		return err
	}
	s.eng = eng
	_, _ = eng.Recognize(warmupPNG)
	return nil
}
