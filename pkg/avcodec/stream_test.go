package avcodec

import (
	"bytes"
	"errors"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStreamDecode verifies the CGO stream decoder can feed an H.264
// keyframe, cache it as YUV420P, and encode the snapshot to JPEG and PNG.
// Mirrors TestStaticDecode: no device needed, runs anywhere the static
// FFmpeg link exists (skips via AVCODEC_SKIP_TEST / non-CGO builds).
func TestStreamDecode(t *testing.T) {
	if os.Getenv("AVCODEC_SKIP_TEST") != "" {
		t.Skip("AVCODEC_SKIP_TEST set")
	}

	keyframe, err := os.ReadFile(filepath.Join("testdata", "keyframe.h264"))
	if err != nil {
		t.Fatalf("read keyframe: %v", err)
	}

	dec, err := NewStreamDecoder()
	if err != nil {
		t.Skipf("NewStreamDecoder: %v (non-CGO build or missing FFmpeg)", err)
	}
	defer dec.Close()

	// No frame yet.
	if _, ok := dec.LatestFrameAt(); ok {
		t.Fatal("LatestFrameAt before any Feed should return ok=false")
	}
	if _, _, _, _, ok := dec.EncodeLatest(FormatJPEG); ok {
		t.Fatal("EncodeLatest before any Feed should return ok=false")
	}

	before := time.Now()
	if err := dec.Feed(keyframe); err != nil {
		t.Fatalf("Feed: %v", err)
	}

	at, ok := dec.LatestFrameAt()
	if !ok {
		t.Fatal("LatestFrameAt after Feed should return ok=true")
	}
	if at.Before(before) {
		t.Fatalf("frame arrival time %v predates feed time %v", at, before)
	}

	// JPEG encoded from the cache must decode back.
	jpgData, w, h, mime, ok := dec.EncodeLatest(FormatJPEG)
	if !ok {
		t.Fatal("EncodeLatest JPEG returned ok=false")
	}
	if w != 320 || h != 240 {
		t.Fatalf("frame dims = %dx%d, want 320x240", w, h)
	}
	if mime != "image/jpeg" {
		t.Fatalf("mime = %q, want image/jpeg", mime)
	}
	if _, err := jpeg.Decode(bytes.NewReader(jpgData)); err != nil {
		t.Fatalf("encoded JPEG does not decode: %v", err)
	}

	// PNG from the same cache must decode back.
	pngData, w2, h2, mime, ok := dec.EncodeLatest(FormatPNG)
	if !ok {
		t.Fatal("EncodeLatest PNG returned ok=false")
	}
	if mime != "image/png" {
		t.Fatalf("mime = %q, want image/png", mime)
	}
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("encoded PNG does not decode: %v", err)
	}
	if img.Bounds().Dx() != w2 || img.Bounds().Dy() != h2 {
		t.Fatalf("PNG dims = %dx%d, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), w2, h2)
	}
}

// TestStreamDecoderNoCGOBounds checks the API contract on non-CGO builds:
// NewStreamDecoder must return ErrNotAvailable so callers can fall back.
func TestStreamDecoderNoCGOBounds(t *testing.T) {
	dec, err := NewStreamDecoder()
	if err == nil {
		dec.Close()
		return // CGO build: decoder available, nothing to check
	}
	if !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("NewStreamDecoder error = %v, want ErrNotAvailable", err)
	}
}

// TestStreamDecoderConfigThenIDR feeds the config packet (SPS+PPS) and the
// IDR as SEPARATE feeds — the stream shape scrcpy emits after every
// RESET_VIDEO pipeline recreation. A standalone config feed must be held
// (no frame, no error) and the IDR that follows must decode.
func TestStreamDecoderConfigThenIDR(t *testing.T) {
	if os.Getenv("AVCODEC_SKIP_TEST") != "" {
		t.Skip("AVCODEC_SKIP_TEST set")
	}
	keyframe, err := os.ReadFile(filepath.Join("testdata", "keyframe.h264"))
	if err != nil {
		t.Fatalf("read keyframe: %v", err)
	}

	// Split at the IDR NAL (type 5) start: config = everything before it.
	idrPos := findNALOfType(keyframe, 5)
	if idrPos < 0 {
		t.Fatal("test vector has no IDR NAL")
	}
	config := keyframe[:idrPos]
	idr := keyframe[idrPos:]

	dec, err := NewStreamDecoder()
	if err != nil {
		t.Skipf("NewStreamDecoder: %v (non-CGO build or missing FFmpeg)", err)
	}
	defer dec.Close()

	if err := dec.Feed(config); err != nil {
		t.Fatalf("Feed(config): %v", err)
	}
	if _, ok := dec.LatestFrameAt(); ok {
		t.Fatal("config-only feed must not produce a frame")
	}

	if err := dec.Feed(idr); err != nil {
		t.Fatalf("Feed(IDR after config): %v", err)
	}
	if _, ok := dec.LatestFrameAt(); !ok {
		t.Fatal("IDR after config must produce a frame")
	}

	// And the PNG/JPEG round-trip still works off the cache.
	for _, format := range []ImageFormat{FormatJPEG, FormatPNG} {
		data, w, h, mime, ok := dec.EncodeLatest(format)
		if !ok || data == nil {
			t.Fatalf("EncodeLatest(%v): ok=%v", format, ok)
		}
		if w != 320 || h != 240 {
			t.Fatalf("frame dims = %dx%d, want 320x240", w, h)
		}
		if mime != format.String() {
			t.Fatalf("mime = %q, want %q", mime, format.String())
		}
	}
}

// findNALOfType returns the offset of the first NAL of the given type
// (start code inclusive), or -1.
func findNALOfType(data []byte, nalType byte) int {
	for pos := 0; pos+3 < len(data); pos++ {
		if data[pos] != 0 || data[pos+1] != 0 {
			continue
		}
		var nalsPos int
		if data[pos+2] == 1 {
			nalsPos = pos + 3
		} else if data[pos+3] == 1 {
			nalsPos = pos + 4
		} else {
			continue
		}
		if nalsPos < len(data) && data[nalsPos]&0x1F == nalType {
			return pos
		}
	}
	return -1
}

// TestHasNoSlice covers the config-packet classifier on synthetic NALs.
func TestHasNoSlice(t *testing.T) {
	sps := []byte{0, 0, 0, 1, 0x67, 0x42, 0x00}
	pps := []byte{0, 0, 0, 1, 0x68, 0xCE, 0x00}
	sei := []byte{0, 0, 0, 1, 0x06, 0x05, 0x01}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88, 0x84}
	slice := []byte{0, 0, 0, 1, 0x41, 0x9A, 0x00}

	if !hasNoSlice(append(append(append([]byte{}, sps...), pps...), sei...)) {
		t.Fatal("SPS+PPS+SEI must classify as config-only")
	}
	if hasNoSlice(append(append([]byte{}, sps...), idr...)) {
		t.Fatal("SPS+IDR must not classify as config-only")
	}
	if hasNoSlice(append(append([]byte{}, sps...), slice...)) {
		t.Fatal("SPS+slice must not classify as config-only")
	}
}
