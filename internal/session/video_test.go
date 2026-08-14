package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gezihua123/phonefast/pkg/avcodec"
	"github.com/gezihua123/phonefast/pkg/h264"
)

// decoderWithKeyframe returns an h264.Decoder holding one cached keyframe —
// config packet + IDR fed through ReadFrame — so LatestKeyframe() is non-nil
// without a device. idr may be nil, in which case a real recorded keyframe
// from the avcodec test fixtures is used.
func decoderWithKeyframe(t *testing.T, idr []byte) *h264.Decoder {
	t.Helper()
	if idr == nil {
		var err error
		idr, err = os.ReadFile(filepath.Join("..", "..", "pkg", "avcodec", "testdata", "keyframe.h264"))
		if err != nil {
			t.Fatalf("read keyframe fixture: %v", err)
		}
	}
	dec := h264.NewDecoder()
	writePacket := func(flags uint64, pts uint64, payload []byte) {
		t.Helper()
		pkt := make([]byte, 0, h264.FrameHeaderSize+len(payload))
		var hdr [h264.FrameHeaderSize]byte
		binary.BigEndian.PutUint64(hdr[0:8], pts|flags)
		binary.BigEndian.PutUint32(hdr[8:12], uint32(len(payload)))
		pkt = append(pkt, hdr[:]...)
		pkt = append(pkt, payload...)
		if _, err := dec.ReadFrame(bytes.NewReader(pkt)); err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
	}
	writePacket(h264.PacketFlagConfig, 0, []byte{0x00, 0x00, 0x00, 0x01, 0x67}) // fake SPS
	writePacket(h264.PacketFlagKeyFrame, 100, idr)
	if dec.LatestKeyframe() == nil {
		t.Fatal("decoder holds no keyframe after feeding config + IDR")
	}
	return dec
}

// TestScreenshotFormatSessionClosed verifies the early error path: a closed
// session fails immediately without touching the decoder or control socket.
func TestScreenshotFormatSessionClosed(t *testing.T) {
	s := &Session{closed: true}
	_, _, _, _, err := s.ScreenshotFormat(avcodec.FormatPNG)
	if err == nil || err.Error() != "session closed" {
		t.Fatalf("err = %v, want 'session closed'", err)
	}
}

// TestScreenshotFormatControlDeadServesCached covers the dead-control branch:
// no RESET_VIDEO can ever produce a fresh keyframe, so ScreenshotFormat must
// skip the 3s PTS wait entirely and serve (or attempt to decode) the cached
// keyframe instead of stalling.
func TestScreenshotFormatControlDeadServesCached(t *testing.T) {
	s := &Session{decoder: decoderWithKeyframe(t, nil)}
	s.markControlBroken(errors.New("control write failed"))

	start := time.Now()
	data, _, _, _, err := s.ScreenshotFormat(avcodec.FormatJPEG)
	el := time.Since(start)

	// The contract under test is the no-wait behavior: a live control socket
	// would wait up to 3s for a fresh keyframe; the dead path must not.
	if el > 2*time.Second {
		t.Errorf("took %v — dead control path must not wait for a fresh keyframe", el)
	}
	// Decoding itself goes through the ffmpeg CLI fallback (CGO is off in this
	// test build), so success depends on the environment: when ffmpeg is
	// present the real fixture decodes and the payload must be non-empty.
	if err == nil && len(data) == 0 {
		t.Error("nil error but empty image payload")
	}
}

// TestScreenshotFormatNoKeyframe verifies the 'no keyframe available' error:
// with a dead control socket and an empty decoder there is nothing to serve.
func TestScreenshotFormatNoKeyframe(t *testing.T) {
	s := &Session{decoder: h264.NewDecoder()}
	s.markControlBroken(errors.New("control write failed"))

	start := time.Now()
	_, _, _, _, err := s.ScreenshotFormat(avcodec.FormatPNG)
	if err == nil || err.Error() != "no keyframe available after 3s" {
		t.Fatalf("err = %v, want 'no keyframe available after 3s'", err)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("took %v — no-keyframe error should be immediate on dead control", el)
	}
}

// TestGetAvDecoderCachesInitError verifies a cached init failure is returned
// as-is — the expensive CGO init is never retried once it has failed.
func TestGetAvDecoderCachesInitError(t *testing.T) {
	s := &Session{}
	sentinel := errors.New("cached init failure")
	s.avDecoderErr = sentinel

	dec, err := s.getAvDecoder(100, 200)
	if dec != nil || !errors.Is(err, sentinel) {
		t.Fatalf("getAvDecoder = (%v, %v), want (nil, sentinel)", dec, err)
	}
	// Second call returns the same cached error.
	if _, err2 := s.getAvDecoder(100, 200); !errors.Is(err2, sentinel) {
		t.Fatalf("second getAvDecoder err = %v, want sentinel", err2)
	}
}

// TestGetAvDecoderCachesRealInitError exercises the real init path: when the
// CGO decoder is unavailable (this test build runs with CGO disabled), the
// init error must be stored in avDecoderErr so later calls short-circuit.
func TestGetAvDecoderCachesRealInitError(t *testing.T) {
	s := &Session{}
	_, err := s.getAvDecoder(100, 200)
	if err == nil {
		t.Skip("CGO decoder available in this build — init-error path not reachable")
	}
	if s.avDecoderErr == nil {
		t.Fatal("init error was not cached in avDecoderErr")
	}
	// The cached value is what subsequent calls return.
	if _, err2 := s.getAvDecoder(100, 200); err2 != s.avDecoderErr {
		t.Fatalf("second call err = %v, want cached %v", err2, s.avDecoderErr)
	}
}

// TestImageBufferSetGet covers the thread-safe image buffer round-trip.
func TestImageBufferSetGet(t *testing.T) {
	ib := NewImageBuffer()
	if data, w, h := ib.Get(); data != nil || w != 0 || h != 0 {
		t.Fatalf("zero-value Get = (%v, %d, %d), want (nil, 0, 0)", data, w, h)
	}
	ib.Set([]byte("img"), 100, 200)
	data, w, h := ib.Get()
	if string(data) != "img" || w != 100 || h != 200 {
		t.Errorf("Get = (%q, %d, %d), want (img, 100, 200)", data, w, h)
	}
	ib.Set([]byte("img2"), 300, 400)
	data, w, h = ib.Get()
	if string(data) != "img2" || w != 300 || h != 400 {
		t.Errorf("Get after overwrite = (%q, %d, %d), want (img2, 300, 400)", data, w, h)
	}
}
