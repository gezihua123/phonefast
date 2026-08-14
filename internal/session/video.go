package session

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	phonelog "github.com/gezihua123/phonefast/internal/log"
	"github.com/gezihua123/phonefast/pkg/avcodec"
)

// Screenshot captures the current screen by grabbing the latest keyframe
// from the video stream and converting it to PNG.
// Deprecated: use ScreenshotFormat for explicit format control.
func (s *Session) Screenshot() ([]byte, int, int, error) {
	data, w, h, _, err := s.ScreenshotFormat(avcodec.FormatPNG)
	return data, w, h, err
}

// ScreenshotFormat captures the current screen and returns the image in the
// specified format. Returns encoded bytes, width, height, MIME type, and error.
//
// Decoding uses the go-astiav CGO decoder (compiled with -tags=cgo) when
// available, falling back to an ffmpeg CLI subprocess. The CGO path is 2-4×
// faster because it avoids process-spawn overhead (~100-200ms per call).
func (s *Session) ScreenshotFormat(format avcodec.ImageFormat) ([]byte, int, int, string, error) {
	if s.screenshotFormatFn != nil { // test seam — see Session fields
		return s.screenshotFormatFn(format)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, 0, 0, "", fmt.Errorf("session closed")
	}
	devW, devH := s.deviceW, s.deviceH
	s.mu.Unlock()

	t0 := time.Now()

	// Force a fresh keyframe on every call. The screen state between IDRs
	// lives in P-frames, which never update latestKeyframe — without the
	// reset, screenshot keeps returning the last keyframe even after the
	// screen changed (the observed stale-screenshot bug: identical md5 for
	// Home vs Settings). RESET_VIDEO makes the encoder emit a new IDR
	// immediately; PTS monotonicity tells us when it has arrived.
	//
	// Freshness fast path: a keyframe that arrived within this window
	// already reflects the current screen — it was preheated by the action
	// preceding this capture (post-action RESET_VIDEO while the screen
	// animated). Skip the ~350ms pipeline-recreation round trip. Older than
	// the window → cold path (correctness preserved: the frame may predate a
	// screen change).
	//
	// When the control connection is dead, no reset can ever produce a fresh
	// keyframe — skip the 3s wait entirely and serve the cached frame (with a
	// warning) instead of stalling every call.
	if s.IsControlAvailable() && s.decoder.LatestKeyframeAge() >= freshKeyframeWindow {
		beforePTS := s.decoder.LatestKeyframePTS()
		// Cold path: exactly ONE reset, then wait passively. On scrcpy 3.3.x
		// each RESET_VIDEO recreates the whole encode pipeline
		// (consumeReset → prepare → MediaCodec configure → start) — repeating
		// the reset mid-recreation restarts it and pushes the wait to the 3s
		// deadline. The throttle is bypassed here (correctness over rate).
		s.forceRequestKeyframe()

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if s.decoder.LatestKeyframePTS() > beforePTS {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		// Degrade gracefully instead of failing: if the reset never produced
		// a fresh keyframe (control socket died mid-wait), return the last
		// frame we have and warn — a stale image beats a failed call.
		if s.decoder.LatestKeyframePTS() <= beforePTS {
			phonelog.Default().Write("screenshot: WARN no fresh keyframe after reset (PTS %d), using possibly stale frame", beforePTS)
		}
	} else if !s.IsControlAvailable() {
		phonelog.Default().Write("screenshot: control socket unavailable, serving cached keyframe")
	} else {
		phonelog.Default().Write("screenshot: fast path (keyframe %v old)",
			s.decoder.LatestKeyframeAge().Round(time.Millisecond))
	}
	keyframe := s.decoder.LatestKeyframe()
	if keyframe == nil {
		return nil, 0, 0, "", fmt.Errorf("no keyframe available after 3s")
	}

	imgData, w, h, mime, err := s.decodeKeyframe(keyframe, devW, devH, format)
	elapsed := time.Since(t0)
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("decode keyframe: %w", err)
	}
	phonelog.Default().Write("screenshot: total=%v keyframe=%dKB format=%s",
		elapsed, len(keyframe)/1024, mime)
	// NOTE: no post-capture pipelining reset here. Each RESET_VIDEO recreates
	// the whole device encode pipeline (~350ms warm, up to 3s cold) and a
	// reset sent mid-recreation restarts it — pipelining after every capture
	// turned tight screenshot loops into 3s restart storms (measured). The
	// fast path + action preheat cover the agent patterns instead.
	return imgData, w, h, mime, nil
}

// freshKeyframeWindow is the freshness fast-path window: a keyframe this
// young (~2-3 frame periods at 30fps) is considered "as good as live" and
// skips the reset+wait round trip.
const freshKeyframeWindow = 60 * time.Millisecond

// requestKeyframe sends a RESET_VIDEO message to trigger a new keyframe.
// Rate-limited: each reset recreates the device's whole encode pipeline
// (scrcpy 3.3.x consumeReset → prepare → MediaCodec restart, ~350ms warm /
// up to 3s cold on this device), so the background action-preheat caller
// dedupes via tryMarkReset. The screenshot cold path uses
// forceRequestKeyframe instead (correctness never defers).
func (s *Session) requestKeyframe() {
	if !s.tryMarkReset() {
		return
	}
	s.writeReset()
}

// forceRequestKeyframe sends RESET_VIDEO unconditionally (cold screenshot
// path) and refreshes the throttle timestamp so subsequent background
// resets are suppressed while this one's pipeline recreation is in flight.
func (s *Session) forceRequestKeyframe() {
	s.resetMu.Lock()
	s.lastResetAt = time.Now()
	s.resetMu.Unlock()
	s.writeReset()
}

// writeReset sends the RESET_VIDEO control byte under the control conn lock.
// A failed write marks the control connection broken so IsControlAvailable()
// flips false (the actor then reconnects) and ScreenshotFormat skips its
// keyframe wait instead of burning the full 3s on every call.
func (s *Session) writeReset() {
	conn := s.lockControlConn()
	if conn == nil {
		return
	}
	if _, err := conn.Write([]byte{17}); err != nil {
		s.markControlBroken(err)
	}
}

// keyframeResetMinInterval is the minimum spacing between RESET_VIDEO
// messages (device IDR pipeline latency; see requestKeyframe).
const keyframeResetMinInterval = 400 * time.Millisecond

// tryMarkReset records a reset attempt under lock. Returns false when a
// reset was sent within keyframeResetMinInterval (caller drops this one).
// Safe for concurrent use (the observe path screenshots concurrently with
// the actor's actions).
func (s *Session) tryMarkReset() bool {
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	now := time.Now()
	if now.Sub(s.lastResetAt) < keyframeResetMinInterval {
		return false
	}
	s.lastResetAt = now
	return true
}

// preheatKeyframe sends RESET_VIDEO after a mutating action, so the device
// rebuilds its encode pipeline while the screen animates from that action
// (frames flow at ~10fps during activity — the fresh keyframe rides the
// active stream). The follow-up screenshot then hits ScreenshotFormat's
// freshness fast path instead of paying the ~350ms pipeline recreation.
// Best-effort: throttled, no-op on a dead control conn.
func (s *Session) preheatKeyframe() {
	s.requestKeyframe()
}

// WaitStable waits until the video stream stabilizes (no significant frame changes).

// keyframeToPNG converts a raw H.264 AnnexB keyframe to a PNG image.
// Kept for backward compatibility — prefers CGO, falls back to ffmpeg CLI.
func keyframeToPNG(keyframe []byte, width, height int) ([]byte, error) {
	data, _, _, _, err := decodeKeyframeStatic(keyframe, width, height, avcodec.FormatPNG)
	return data, err
}

// decodeKeyframe converts a raw H.264 AnnexB keyframe to an image.
//
// Primary path: go-astiav CGO decoder (2-4× faster, no subprocess).
// Fallback path: ffmpeg CLI subprocess (~100-200ms process overhead).
func (s *Session) decodeKeyframe(keyframe []byte, width, height int, format avcodec.ImageFormat) ([]byte, int, int, string, error) {
	// Try CGO go-astiav decoder first.
	decoder, err := s.getAvDecoder(width, height)
	if err == nil && decoder != nil {
		t0 := time.Now()
		data, w, h, mime, decErr := decoder.Decode(keyframe, width, height, format)
		elapsed := time.Since(t0)
		if decErr == nil {
			phonelog.Default().Write("decode: CGO %dx%d %s in %v",
				w, h, mime, elapsed)
			return data, w, h, mime, nil
		}
		phonelog.Default().Write("decode: CGO FAIL in %v → CLI fallback: %v", elapsed, decErr)
	}

	// CLI fallback.
	return decodeKeyframeStatic(keyframe, width, height, format)
}

// getAvDecoder returns the go-astiav decoder, initializing it lazily.
// Caches init failures so we don't retry the expensive CGO init on every call.
func (s *Session) getAvDecoder(width, height int) (avcodec.Decoder, error) {
	if s.avDecoderErr != nil {
		return nil, s.avDecoderErr
	}
	if s.avDecoder != nil {
		return s.avDecoder, nil
	}

	t0 := time.Now()
	decoder, err := avcodec.NewDecoder(width, height)
	if err != nil {
		s.avDecoderErr = err
		phonelog.Default().Write("avcodec: init FAIL in %v → CLI fallback: %v", time.Since(t0), err)
		return nil, err
	}
	phonelog.Default().Write("avcodec: init OK in %v", time.Since(t0))

	s.avDecoder = decoder
	return decoder, nil
}

// decodeKeyframeStatic is the package-level ffmpeg CLI fallback path.
// The separate function exists so that the legacy keyframeToPNG entry
// point (no Session context) can reach it.
func decodeKeyframeStatic(keyframe []byte, width, height int, format avcodec.ImageFormat) ([]byte, int, int, string, error) {
	pngData, err := decodeViaFFmpeg(keyframe, width, height)
	if err != nil {
		return nil, 0, 0, "", err
	}
	return pngData, width, height, "image/png", nil
}

// decodeViaFFmpeg pipes H.264 AnnexB keyframe data to ffmpeg and returns PNG.
func decodeViaFFmpeg(keyframe []byte, width, height int) ([]byte, error) {
	ffmpegPath, err := findFFmpeg()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-f", "h264",
		"-i", "pipe:0",
		"-frames:v", "1",
		"-f", "image2pipe",
		"-vcodec", "png",
		"pipe:1",
	)

	cmd.Stdin = bytes.NewReader(keyframe)
	_ = width
	_ = height
	cmd.Stderr = nil

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode: %w", err)
	}

	result := stdout.Bytes()
	if len(result) == 0 {
		return nil, fmt.Errorf("ffmpeg produced empty output")
	}

	return result, nil
}

// findFFmpeg locates the ffmpeg binary. On Windows checks common install paths.
func findFFmpeg() (string, error) {
	candidates := []string{"ffmpeg"}
	if isWindows() {
		candidates = append(candidates,
			`C:\ffmpeg\bin\ffmpeg.exe`,
			`C:\Program Files\ffmpeg\bin\ffmpeg.exe`,
			`ffmpeg.exe`,
		)
	}
	for _, p := range candidates {
		if path, err := exec.LookPath(p); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("ffmpeg not found — install it:\n%s", installFFmpegHint())
}

func installFFmpegHint() string {
	return "  macOS:  brew install ffmpeg\n" +
		"  Linux:  apt install ffmpeg  /  dnf install ffmpeg\n" +
		"  Windows: download from https://www.gyan.dev/ffmpeg/builds/\n" +
		"           extract to C:\\ffmpeg, add C:\\ffmpeg\\bin to PATH"
}

// ImageBuffer is a thread-safe buffer for accumulating image data.
// Exported for use by external consumers that want frame-based caching.
type ImageBuffer struct {
	mu   sync.Mutex
	data []byte
	w, h int
}

// NewImageBuffer creates a new ImageBuffer.
func NewImageBuffer() *ImageBuffer { return &ImageBuffer{} }

// Set updates the buffered image.
func (ib *ImageBuffer) Set(data []byte, w, h int) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	ib.data, ib.w, ib.h = data, w, h
}

// Get returns the current buffered image.
func (ib *ImageBuffer) Get() ([]byte, int, int) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.data, ib.w, ib.h
}
