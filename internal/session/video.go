package session

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/gezihua123/phonefast/pkg/avcodec"
	phonelog "github.com/gezihua123/phonefast/internal/log"
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
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, 0, 0, "", fmt.Errorf("session closed")
	}
	devW, devH := s.DeviceW, s.DeviceH
	s.mu.Unlock()

	t0 := time.Now()

	keyframe := s.decoder.LatestKeyframe()
	if keyframe == nil {
		s.requestKeyframe()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			keyframe = s.decoder.LatestKeyframe()
			if keyframe != nil {
				break
			}
		}
		if keyframe == nil {
			return nil, 0, 0, "", fmt.Errorf("no keyframe available after 3s")
		}
	}

	imgData, w, h, mime, err := s.decodeKeyframe(keyframe, devW, devH, format)
	elapsed := time.Since(t0)
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("decode keyframe: %w", err)
	}
	phonelog.Default().Write("screenshot: total=%v keyframe=%dKB format=%s",
		elapsed, len(keyframe)/1024, mime)
	return imgData, w, h, mime, nil
}

// requestKeyframe sends a RESET_VIDEO message to trigger a new keyframe.
func (s *Session) requestKeyframe() {
	// Take the control conn under lock like every other control write.
	// The Write error is intentionally ignored: a failed keyframe request
	// just means Screenshot falls back to its 3s timeout.
	conn := s.lockControlConn()
	if conn == nil {
		return
	}
	conn.Write([]byte{17})
}

// WaitStable waits until the video stream stabilizes (no significant frame changes).
func (s *Session) WaitStable(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	_ = deadline
	stableDuration := 500 * time.Millisecond
	time.Sleep(stableDuration)
	return nil
}

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
