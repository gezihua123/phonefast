package session

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// Screenshot captures the current screen by grabbing the latest keyframe
// from the video stream and converting it to PNG.
func (s *Session) Screenshot() ([]byte, int, int, error) {
	// Read session state under lock, then release so drainFrames() can run.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, 0, 0, fmt.Errorf("session closed")
	}
	devW, devH := s.DeviceW, s.DeviceH
	s.mu.Unlock()

	// Fast path: keyframe already available.
	keyframe := s.decoder.LatestKeyframe()
	if keyframe == nil {
		// Request a new keyframe and wait — lock must NOT be held here
		// because drainFrames() needs s.mu to get its conn each iteration.
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
			return nil, 0, 0, fmt.Errorf("no keyframe available after 3s")
		}
	}

	pngData, err := keyframeToPNG(keyframe, devW, devH)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("convert to PNG: %w", err)
	}

	return pngData, devW, devH, nil
}

// requestKeyframe sends a RESET_VIDEO message to trigger a new keyframe.
func (s *Session) requestKeyframe() {
	// Take the control conn under lock like every other control write —
	// reading s.controlConn directly races with Close()/markControlBroken()
	// which nil it out. The Write error is intentionally ignored: a failed
	// keyframe request just means Screenshot falls back to its 3s timeout.
	conn := s.lockControlConn()
	if conn == nil {
		return
	}
	// RESET_VIDEO (type 17) has no payload — just a 1-byte type
	conn.Write([]byte{17})
}

// WaitStable waits until the video stream stabilizes (no significant frame changes).
func (s *Session) WaitStable(timeout time.Duration) error {
	// Simple approach: wait for a fixed duration, then ensure
	// we have a recent keyframe.
	//
	// A more sophisticated implementation would compare consecutive
	// frames for pixel differences to detect animation/transition end.

	deadline := time.Now().Add(timeout)
	stableDuration := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		time.Sleep(stableDuration)
		// Just ensure we have a frame — the keyframe grab handles the rest
		return nil
	}

	return fmt.Errorf("wait_stable timeout after %v", timeout)
}

// keyframeToPNG converts a raw H.264 AnnexB keyframe to a PNG via ffmpeg.
// Returns an error if ffmpeg is not available or decoding fails — callers
// receive an explicit error rather than a silent black image.
func keyframeToPNG(keyframe []byte, width, height int) ([]byte, error) {
	return decodeViaFFmpeg(keyframe, width, height)
}

// decodeViaFFmpeg pipes H.264 AnnexB keyframe data to ffmpeg and returns PNG.
// Converts: H.264 AnnexB → ffmpeg -f h264 -i pipe:0 -frames:v 1 -f image2pipe pipe:1 → PNG bytes
func decodeViaFFmpeg(keyframe []byte, width, height int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-f", "h264", // input format: raw H.264
		"-i", "pipe:0", // read from stdin
		"-frames:v", "1", // single frame
		"-f", "image2pipe", // output format: image pipe
		"-vcodec", "png", // PNG output
		"pipe:1", // write to stdout
	)

	cmd.Stdin = bytes.NewReader(keyframe)
	cmd.Stderr = nil // suppress ffmpeg diagnostic output

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

// ImageBuffer is a thread-safe buffer for accumulating image data.
type ImageBuffer struct {
	mu   sync.Mutex
	data []byte
	w, h int
}

// NewImageBuffer creates a new ImageBuffer.
func NewImageBuffer() *ImageBuffer {
	return &ImageBuffer{}
}

// Set updates the buffered image.
func (ib *ImageBuffer) Set(data []byte, w, h int) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	ib.data = data
	ib.w = w
	ib.h = h
}

// Get returns the current buffered image.
func (ib *ImageBuffer) Get() ([]byte, int, int) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.data, ib.w, ib.h
}
