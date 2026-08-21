package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
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

// fakeStreamDecoder is an in-package avcodec.StreamDecoder double for
// exercising screenshotStream's freshness rule without a device.
type fakeStreamDecoder struct {
	yuv        []byte
	w, h       int
	at         time.Time
	hasFrame   bool
	encodeFail bool
}

func (f *fakeStreamDecoder) Feed([]byte) error { return nil }

func (f *fakeStreamDecoder) LatestFrameAt() (time.Time, bool) {
	return f.at, f.hasFrame
}

func (f *fakeStreamDecoder) EncodeLatest(format avcodec.ImageFormat) ([]byte, int, int, string, bool) {
	if !f.hasFrame || f.encodeFail {
		return nil, 0, 0, "", false
	}
	return append([]byte(nil), f.yuv...), f.w, f.h, format.String(), true
}

func (f *fakeStreamDecoder) Close() error { return nil }

// TestScreenshotStreamServesPostActionFrame: a cached frame that arrived
// after the most recent action is the current screen (idle encoder stops
// sending, but the screen hasn't changed) — served directly, no reset.
func TestScreenshotStreamServesPostActionFrame(t *testing.T) {
	now := time.Now()
	s := &Session{
		streamDec: &fakeStreamDecoder{
			yuv:      []byte{0x10, 0x20},
			w:        2, h: 2,
			at:       now.Add(-500 * time.Millisecond), // old but post-action
			hasFrame: true,
		},
		lastActionAt: now.Add(-time.Second),
	}
	s.markControlBroken(errors.New("no control conn in test"))

	start := time.Now()
	data, w, h, mime, err := s.ScreenshotFormat(avcodec.FormatJPEG)
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("took %v — post-action frame must be served directly", el)
	}
	if err != nil {
		t.Fatalf("ScreenshotFormat: %v", err)
	}
	if w != 2 || h != 2 || mime != "image/jpeg" || len(data) == 0 {
		t.Errorf("got %dx%d %q %d bytes — want 2x2 image/jpeg non-empty", w, h, mime, len(data))
	}
}

// TestScreenshotStreamStaleFrameDegrades: a frame predating the last action
// may show a pre-action screen. With a dead control conn no reset can help,
// so the contract is degrade-gracefully: serve what we have, however old,
// instead of failing (mirrors the legacy dead-control branch).
func TestScreenshotStreamStaleFrameDegrades(t *testing.T) {
	now := time.Now()
	s := &Session{
		streamDec: &fakeStreamDecoder{
			yuv:      []byte{0x30, 0x40},
			w:        2, h: 2,
			at:       now.Add(-2 * time.Second), // predates the action
			hasFrame: true,
		},
		lastActionAt: now.Add(-time.Second),
	}
	s.markControlBroken(errors.New("no control conn in test"))

	start := time.Now()
	data, _, _, _, err := s.ScreenshotFormat(avcodec.FormatJPEG)
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("took %v — dead control must not wait for a fresh frame", el)
	}
	if err != nil || len(data) == 0 {
		t.Fatalf("err = %v, want degraded serve of the stale frame", err)
	}
}

// TestScreenshotStreamNoFrameErrors: never decoded a frame + dead control =
// nothing to serve, error out fast (no 3s reset wait).
func TestScreenshotStreamNoFrameErrors(t *testing.T) {
	s := &Session{streamDec: &fakeStreamDecoder{}}
	s.markControlBroken(errors.New("no control conn in test"))

	start := time.Now()
	_, _, _, _, err := s.ScreenshotFormat(avcodec.FormatJPEG)
	if err == nil || err.Error() != "no video frame available" {
		t.Fatalf("err = %v, want 'no video frame available'", err)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("took %v — no-frame error should be immediate on dead control", el)
	}
}

// TestScreenshotStreamStaleAgeTriggersReset: a frame older than
// streamStaleCap (encoder deep-sleep — external changes produce no frames)
// must trigger a RESET_VIDEO instead of being served directly. Uses a
// net.Pipe control conn to observe the reset byte; the frame never goes
// fresh, so the wait loop runs to its deadline and degrades to the stale
// serve (contract: a stale image beats a failed call).
func TestScreenshotStreamStaleAgeTriggersReset(t *testing.T) {
	if testing.Short() {
		t.Skip("3s reset-wait deadline — slow test")
	}
	now := time.Now()
	ctrlServer, ctrlClient := net.Pipe()
	s := &Session{
		streamDec: &fakeStreamDecoder{
			yuv:      []byte{0x50, 0x60},
			w:        2, h: 2,
			at:       now.Add(-2 * time.Minute), // post-action but deep-slept age
			hasFrame: true,
		},
		lastActionAt: now.Add(-3 * time.Minute),
		controlConn:  ctrlServer,
	}
	defer ctrlServer.Close()

	resetBytes := make(chan byte, 1)
	go func() {
		buf := make([]byte, 1)
		if _, err := ctrlClient.Read(buf); err == nil {
			resetBytes <- buf[0]
		} else {
			resetBytes <- 0
		}
		ctrlClient.Close()
	}()

	start := time.Now()
	data, _, _, _, err := s.ScreenshotFormat(avcodec.FormatJPEG)
	el := time.Since(start)

	if err != nil || len(data) == 0 {
		t.Fatalf("err = %v, want degraded serve of the stale frame", err)
	}
	select {
	case b := <-resetBytes:
		if b != 17 { // RESET_VIDEO control byte
			t.Errorf("control byte = %d, want 17 (RESET_VIDEO)", b)
		}
	default:
		t.Error("no RESET_VIDEO written — stale-age frame was served without a reset attempt")
	}
	// The wait loop polls until its 3s deadline (the fake frame never goes
	// fresh), then degrades. Verify the deadline was actually honored.
	if el < 2500*time.Millisecond {
		t.Errorf("took %v — want ~3s reset-wait deadline before degraded serve", el)
	}
}

// TestScreenshotStreamServesFreshPostActionFrame pins the fast-path
// contract: a post-action frame within streamStaleCap serves instantly with
// NO control write (no reset byte on the pipe).
func TestScreenshotStreamServesFreshPostActionFrame(t *testing.T) {
	now := time.Now()
	ctrlServer, ctrlClient := net.Pipe()
	s := &Session{
		streamDec: &fakeStreamDecoder{
			yuv:      []byte{0x70, 0x80},
			w:        2, h: 2,
			at:       now.Add(-time.Second),
			hasFrame: true,
		},
		lastActionAt: now.Add(-2 * time.Second),
		controlConn:  ctrlServer,
	}
	defer ctrlServer.Close()
	defer ctrlClient.Close()

	// Any control write would block this goroutine until the test's defer
	// closes the pipe — detect it by a read with a timeout instead.
	wrote := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 1)
		ctrlClient.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if _, err := ctrlClient.Read(buf); err == nil {
			wrote <- struct{}{}
		}
	}()

	start := time.Now()
	data, _, _, _, err := s.ScreenshotFormat(avcodec.FormatJPEG)
	if el := time.Since(start); el > 500*time.Millisecond {
		t.Errorf("took %v — fresh post-action frame must serve instantly", el)
	}
	if err != nil || len(data) == 0 {
		t.Fatalf("err = %v, want direct serve", err)
	}
	select {
	case <-wrote:
		t.Error("fresh post-action frame must not trigger a RESET_VIDEO")
	case <-time.After(350 * time.Millisecond):
	}
}
