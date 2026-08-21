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
	s.mu.Unlock()

	// CGO stream-decode path: drainFrames keeps the stream decoder's cached
	// frame tracking the screen continuously (scrcpy emits frames ~10fps
	// even on a static screen), so a screenshot is just "encode the latest
	// frame" — no RESET_VIDEO, no device pipeline recreation. This also
	// captures screen changes from ANY source (physical touches included),
	// unlike keyframe-based capture which only sees agent actions.
	if s.streamDec != nil {
		return s.screenshotStream(format)
	}

	// Pure-Go / no stream decoder: keyframe reset path (see below).
	return s.screenshotLegacy(format)
}

// streamStaleCap is the max age of a directly-served cached frame. Measured
// on this device: while the encoder is only lightly asleep (≤60s idle),
// EXTERNAL input (physical touch, adb shell input) still wakes it and frames
// flow within ~100-300ms — the cache self-heals, so no cap is needed there.
// After minutes of idle the encoder deep-sleeps: external operations change
// the screen WITHOUT producing any frame (measured: zero frames after ~5min
// idle), so the cache would go stale silently forever. A RESET_VIDEO
// recreates the device pipeline and forces a fresh frame regardless of sleep
// depth — so beyond this age correctness wins and the screenshot pays the
// ~350ms reset instead of serving instantly.
const streamStaleCap = 60 * time.Second

// screenshotStream serves a screenshot from the stream decoder's latest
// decoded frame. Falls back to reset+wait when the frame is stale, and to
// the stale frame (with a warning) when even that fails — a stale image
// beats a failed call (same degrade-gracefully contract as the legacy path).
//
// Freshness rule: the cached frame is usable only if it arrived AFTER the
// most recent device action AND is younger than streamStaleCap:
//   - right after an action the encoder has a ~100-300ms wake gap (frames
//     still show the pre-action screen), so a frame predating the action
//     may be stale → reset path;
//   - when the screen is idle the encoder stops sending frames (~0.5-1s
//     after the animation settles), so age grows while the screen hasn't
//     changed — a post-action frame stays correct → served directly;
//   - the age cap bounds the deep-sleep hole: an encoder asleep for minutes
//     stops producing frames even for EXTERNAL screen changes, which no
//     lastActionAt update can detect → reset+wait after streamStaleCap.
//
// A wedged encoder (action produced no frames) also trips the first case
// (frame predates the action) and falls into the reset+wait loop.
func (s *Session) screenshotStream(format avcodec.ImageFormat) ([]byte, int, int, string, error) {
	t0 := time.Now()
	dec := s.streamDec

	tryEncode := func() ([]byte, int, int, string, bool) {
		at, ok := dec.LatestFrameAt()
		if !ok {
			return nil, 0, 0, "", false
		}
		s.resetMu.Lock()
		lastAction := s.lastActionAt
		s.resetMu.Unlock()
		if !at.After(lastAction) || time.Since(at) > streamStaleCap {
			return nil, 0, 0, "", false
		}
		// EncodeLatest works on the cache in place (no snapshot copy) and
		// may encode a NEWER frame than the one validated above — strictly
		// fresher, so the freshness decision stays conservative.
		return dec.EncodeLatest(format)
	}

	// Fast path: the cached frame is fresh.
	if data, w, h, mime, ok := tryEncode(); ok {
		phonelog.Default().Write("screenshot: stream total=%v %dx%d %s",
			time.Since(t0), w, h, mime)
		return data, w, h, mime, nil
	}

	// Stale: wake the encoder with a RESET_VIDEO (deduped against an
	// in-flight reset) and wait for fresh frames to flow.
	if s.IsControlAvailable() {
		if _, ok := dec.LatestFrameAt(); !ok {
			// Never decoded a frame yet (fresh connect): the stream's first
			// config+IDR is already on its way — wait passively for it.
			// A RESET_VIDEO here would recreate the just-built pipeline and
			// only DELAY that first frame.
			firstDeadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(firstDeadline) {
				if data, w, h, mime, ok := tryEncode(); ok {
					phonelog.Default().Write("screenshot: stream total=%v (first frame) %dx%d %s",
						time.Since(t0), w, h, mime)
					return data, w, h, mime, nil
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		if !s.resetInFlight() {
			s.forceRequestKeyframe()
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if data, w, h, mime, ok := tryEncode(); ok {
				phonelog.Default().Write("screenshot: stream total=%v (reset) %dx%d %s",
					time.Since(t0), w, h, mime)
				return data, w, h, mime, nil
			}
			time.Sleep(5 * time.Millisecond)
		}
		phonelog.Default().Write("screenshot: WARN no fresh stream frame after reset — serving stale")
	}

	// Last resort: serve whatever we have, however old.
	if data, w, h, mime, ok := dec.EncodeLatest(format); ok {
		return data, w, h, mime, nil
	}
	return nil, 0, 0, "", fmt.Errorf("no video frame available")
}

// screenshotLegacy captures via the keyframe reset path: force a fresh
// keyframe, wait for its IDR, decode. Used on pure-Go builds (no stream
// decoder) and when the stream decoder is unavailable.
func (s *Session) screenshotLegacy(format avcodec.ImageFormat) ([]byte, int, int, string, error) {
	s.mu.Lock()
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
		//
		// 在途去重: 若 preheat(动作后)刚在 keyframeResetInFlightWindow 内
		// 发出过 reset, 设备管线重建正在进行中——再发一个会把重建重启、
		// 把等待推到 3s。此时直接等那个在途 keyframe 到达即可。
		if !s.resetInFlight() {
			s.forceRequestKeyframe()
		}

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if s.decoder.LatestKeyframePTS() > beforePTS {
				break
			}
			time.Sleep(5 * time.Millisecond)
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
	phonelog.Default().Write("screenshot: legacy total=%v keyframe=%dKB format=%s",
		elapsed, len(keyframe)/1024, mime)
	// NOTE: no post-capture pipelining reset here. Each RESET_VIDEO recreates
	// the whole device encode pipeline (~350ms warm, up to 3s cold) and a
	// reset sent mid-recreation restarts it — pipelining after every capture
	// turned tight screenshot loops into 3s restart storms (measured). The
	// fast path + action preheat cover the agent patterns instead.
	return imgData, w, h, mime, nil
}

// freshKeyframeWindow is the freshness fast-path window: a keyframe this
// young is considered "as good as live" and skips the reset+wait round trip.
// 100ms ≈ 3 frame periods at 30fps: 仍能覆盖"动作后 preheat 刚送达"的窗口,
// 比原 60ms 更宽容, 更多动作→截图序列命中快路径(压测显示 94% 截图走冷路径)。
const freshKeyframeWindow = 100 * time.Millisecond

// keyframeResetInFlightWindow 是"reset 在途"判定窗口: 最近一次 RESET_VIDEO
// 在此窗口内发出时, 设备管线重建仍在进行, 截图冷路径不再补发 reset(见
// ScreenshotFormat 在途去重)。窗口取值略小于实测重建耗时(~350ms), 只去重
// 明确在途的, 不吞掉真正的冷启动 reset。
const keyframeResetInFlightWindow = 300 * time.Millisecond

// resetInFlight reports whether a RESET_VIDEO was sent within
// keyframeResetInFlightWindow (device pipeline recreation likely in progress).
func (s *Session) resetInFlight() bool {
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	return time.Since(s.lastResetAt) < keyframeResetInFlightWindow
}

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
//
// On the CGO stream-decode path the RESET_VIDEO itself is skipped: the
// stream decoder tracks every frame, so screenshots don't need a fresh IDR —
// and a reset recreates the device encode pipeline (~350ms during which
// frames pause), which would stall the cached frame right after the action.
// lastActionAt is still recorded: screenshotStream serves the cached frame
// only if it arrived after the most recent action (see screenshotStream).
// The legacy keyframe path (pure-Go builds) keeps the preheat reset.
func (s *Session) preheatKeyframe() {
	if s.preheatKeyframeFn != nil { // test seam - see Session fields
		s.preheatKeyframeFn()
		return
	}
	s.resetMu.Lock()
	s.lastActionAt = time.Now()
	s.resetMu.Unlock()
	if s.streamDec != nil {
		return
	}
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
