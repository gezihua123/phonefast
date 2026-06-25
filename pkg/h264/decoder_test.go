package h264

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// makeFrameHeader builds a 12-byte scrcpy frame header for tests.
func makeFrameHeader(pts int64, isConfig, isKeyframe bool, size uint32) []byte {
	flags := uint64(pts)
	if isConfig {
		flags |= PacketFlagConfig
	}
	if isKeyframe {
		flags |= PacketFlagKeyFrame
	}
	buf := make([]byte, FrameHeaderSize)
	binary.BigEndian.PutUint64(buf[0:8], flags)
	binary.BigEndian.PutUint32(buf[8:12], size)
	return buf
}

func TestReadVideoHeader(t *testing.T) {
	// 4B codec "h264" (0x68323634 LE-of-name, but stored as big-endian uint32),
	// 4B width, 4B height — all big-endian per ReadVideoHeader.
	hdr := make([]byte, VideoHeaderSize)
	binary.BigEndian.PutUint32(hdr[0:4], 0x68323634)
	binary.BigEndian.PutUint32(hdr[4:8], 1080)
	binary.BigEndian.PutUint32(hdr[8:12], 2400)

	d := NewDecoder()
	if err := d.ReadVideoHeader(bytes.NewReader(hdr)); err != nil {
		t.Fatalf("ReadVideoHeader: %v", err)
	}
	if d.Width() != 1080 || d.Height() != 2400 {
		t.Fatalf("dims = %dx%d, want 1080x2400", d.Width(), d.Height())
	}
	if d.codec != 0x68323634 {
		t.Fatalf("codec = %#x, want 0x68323634", d.codec)
	}
}

func TestReadVideoHeaderTruncated(t *testing.T) {
	d := NewDecoder()
	err := d.ReadVideoHeader(bytes.NewReader([]byte{1, 2, 3}))
	if err == nil {
		t.Fatal("expected error on truncated header, got nil")
	}
}

func TestReadFrameConfigStoresConfigRaw(t *testing.T) {
	d := NewDecoder()
	configPayload := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00} // SPS-ish
	frame := append(makeFrameHeader(0, true, false, uint32(len(configPayload))), configPayload...)

	f, err := d.ReadFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !f.Config {
		t.Fatal("frame.Config = false, want true")
	}
	if f.KeyFrame {
		t.Fatal("config frame reported as keyframe")
	}
	if !bytes.Equal(f.Data, configPayload) {
		t.Fatalf("frame data mismatch")
	}
	if d.configRaw == nil || !bytes.Equal(d.configRaw, configPayload) {
		t.Fatal("configRaw not stored verbatim")
	}
}

func TestReadFrameKeyframePrependsConfig(t *testing.T) {
	d := NewDecoder()

	// Feed a config frame first.
	configPayload := []byte{0x00, 0x00, 0x00, 0x01, 0x67}
	cfgFrame := append(makeFrameHeader(0, true, false, uint32(len(configPayload))), configPayload...)
	if _, err := d.ReadFrame(bytes.NewReader(cfgFrame)); err != nil {
		t.Fatalf("read config frame: %v", err)
	}

	// Then a keyframe (IDR).
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAB}
	kfFrame := append(makeFrameHeader(42, false, true, uint32(len(idr))), idr...)
	f, err := d.ReadFrame(bytes.NewReader(kfFrame))
	if err != nil {
		t.Fatalf("read keyframe: %v", err)
	}
	if !f.KeyFrame {
		t.Fatal("frame.KeyFrame = false, want true")
	}
	if f.PTS != 42 {
		t.Fatalf("PTS = %d, want 42", f.PTS)
	}

	want := append(append([]byte{}, configPayload...), idr...)
	got := d.LatestKeyframe()
	if !bytes.Equal(got, want) {
		t.Fatalf("LatestKeyframe = %x, want %x (config prepended to idr)", got, want)
	}
}

func TestReadFrameKeyframeWithoutConfig(t *testing.T) {
	// Keyframe arriving before any config: buildKeyframe returns idr alone
	// (configRaw == nil). Must not panic or prepend garbage.
	d := NewDecoder()
	idr := []byte{0x00, 0x00, 0x00, 0x01, 0x65}
	frame := append(makeFrameHeader(7, false, true, uint32(len(idr))), idr...)
	if _, err := d.ReadFrame(bytes.NewReader(frame)); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got := d.LatestKeyframe(); !bytes.Equal(got, idr) {
		t.Fatalf("LatestKeyframe = %x, want %x", got, idr)
	}
}

func TestReadFrameNonKeyframeDoesNotOverwriteKeyframe(t *testing.T) {
	d := NewDecoder()
	// Send a P-frame (neither config nor keyframe) — must not clear/latestKeyframe.
	idr := []byte{0x65}
	kf := append(makeFrameHeader(1, false, true, uint32(len(idr))), idr...)
	if _, err := d.ReadFrame(bytes.NewReader(kf)); err != nil {
		t.Fatal(err)
	}
	pframe := []byte{0x61}
	pf := append(makeFrameHeader(2, false, false, uint32(len(pframe))), pframe...)
	if _, err := d.ReadFrame(bytes.NewReader(pf)); err != nil {
		t.Fatal(err)
	}
	if got := d.LatestKeyframe(); !bytes.Equal(got, idr) {
		t.Fatalf("P-frame overwrote keyframe: got %x, want %x", got, idr)
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	d := NewDecoder()
	// size = 50MB + 1, just over the cap.
	hdr := makeFrameHeader(0, false, false, 50*1024*1024+1)
	_, err := d.ReadFrame(bytes.NewReader(hdr))
	if err == nil {
		t.Fatal("expected error for oversize frame, got nil")
	}
}

func TestReadFrameTruncatedData(t *testing.T) {
	d := NewDecoder()
	// Header claims 10 bytes but stream has 3.
	hdr := makeFrameHeader(0, false, true, 10)
	_, err := d.ReadFrame(bytes.NewReader(append(hdr, 1, 2, 3)))
	if err == nil {
		t.Fatal("expected error on truncated frame data, got nil")
	}
}

func TestReadFramePTSUnmaskedFromFlags(t *testing.T) {
	// PTS shares the high uint64 with flags; verify masking keeps low bits.
	d := NewDecoder()
	const pts = 1234567
	idr := []byte{0x65}
	frame := append(makeFrameHeader(pts, false, true, uint32(len(idr))), idr...)
	f, err := d.ReadFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	if f.PTS != pts {
		t.Fatalf("PTS = %d, want %d", f.PTS, pts)
	}
}

func TestFindStartCode3Byte(t *testing.T) {
	data := []byte{0xAA, 0x00, 0x00, 0x01, 0x67}
	if got := FindStartCode(data, 0); got != 1 {
		t.Fatalf("FindStartCode(3-byte) = %d, want 1", got)
	}
}

func TestFindStartCode4Byte(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x01, 0x67}
	if got := FindStartCode(data, 0); got != 0 {
		t.Fatalf("FindStartCode(4-byte) = %d, want 0", got)
	}
}

func TestFindStartCodeNone(t *testing.T) {
	data := []byte{0xAA, 0xBB, 0xCC}
	if got := FindStartCode(data, 0); got != -1 {
		t.Fatalf("FindStartCode = %d, want -1", got)
	}
}

func TestFindStartCodeAtDataEnd(t *testing.T) {
	// Less than 4 bytes remaining after offset — must not read out of bounds.
	data := []byte{0x00, 0x00}
	if got := FindStartCode(data, 0); got != -1 {
		t.Fatalf("FindStartCode on short data = %d, want -1", got)
	}
}

func TestLatestKeyframeEmptyByDefault(t *testing.T) {
	d := NewDecoder()
	if got := d.LatestKeyframe(); got != nil {
		t.Fatalf("LatestKeyframe on fresh decoder = %x, want nil", got)
	}
}

// TestReadFrameEOFOnCleanStream verifies a closed stream returns a wrapped
// error (not a panic) — drainFrames relies on this to detect video death.
func TestReadFrameEOFOnCleanStream(t *testing.T) {
	d := NewDecoder()
	_, err := d.ReadFrame(bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected error on empty stream, got nil")
	}
	// Must be io.EOF or wrap it (io.ReadFull on 0 bytes returns io.EOF;
	// decoder wraps it via fmt.Errorf("...: %w")).
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF-wrapped error, got %v", err)
	}
}
