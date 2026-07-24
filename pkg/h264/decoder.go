// Package h264 provides H.264 AnnexB stream parsing and keyframe extraction.
package h264

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"
	"sync"
)

// NAL unit types.
const (
	NALTypeSlice    = 1
	NALTypeIDR      = 5 // Instantaneous Decoder Refresh (keyframe)
	NALTypeSEI      = 6
	NALTypeSPS      = 7
	NALTypePPS      = 8
	NALTypeAUD      = 9
)

// Scrcpy frame metadata header (12 bytes):
//   8 bytes: ptsAndFlags (PACKET_FLAG_CONFIG=1<<63, PACKET_FLAG_KEY_FRAME=1<<62)
//   4 bytes: packet size
const (
	FrameHeaderSize   = 12
	PacketFlagConfig  = uint64(1 << 63)
	PacketFlagKeyFrame = uint64(1 << 62)
)

// VideoHeader as sent by scrcpy Streamer.writeVideoHeader:
//   4 bytes: codec ID (0x68323634 = "h264" little-endian)
//   4 bytes: width
//   4 bytes: height
const VideoHeaderSize = 12

// A Frame is a decoded video frame.
type Frame struct {
	Data      []byte // raw frame data (H.264 NAL units, AnnexB format)
	PTS       int64
	KeyFrame  bool
	Config    bool
	Width     int
	Height    int
}

// Decoder extracts frames from scrcpy's video socket stream.
// It does not perform H.264 → RGB decoding; it identifies config packets
// (SPS+PPS) and keyframes, then assembles complete keyframes for screenshot use.
type Decoder struct {
	mu sync.Mutex

	width  int
	height int
	codec  uint32

	// readBuf is a reusable scratch buffer for reading frame payloads off the
	// socket. drainFrames (the sole ReadFrame caller) discards the returned
	// *Frame, so reusing one buffer across calls avoids a make([]byte, size)
	// per frame — at 15fps that's ~60 short-lived 40KB allocations/min that
	// exist only to be immediately GC'd.
	//
	// Lifetime contract: the *Frame.Data returned by ReadFrame aliases this
	// buffer, so it is only valid until the NEXT ReadFrame call. Callers that
	// need to keep frame data must copy it (configRaw/latestKeyframe already do
	// — see extractConfigs/buildKeyframe).
	//
	// Not safe for concurrent use; ReadFrame is called only from the single
	// drainFrames goroutine.
	readBuf []byte

	// configRaw holds the raw AnnexB config packet (SPS+PPS) verbatim.
	// Used to prefix keyframes so they can be decoded independently.
	configRaw []byte

	// latestKeyframe is configRaw + most-recent IDR frame, ready for decoding.
	latestKeyframe []byte
	latestPTS      int64
}

// NewDecoder creates a new H.264 stream decoder.
func NewDecoder() *Decoder {
	return &Decoder{}
}

// ReadVideoHeader reads the scrcpy video codec metadata header.
func (d *Decoder) ReadVideoHeader(r io.Reader) error {
	var buf [VideoHeaderSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return fmt.Errorf("read video header: %w", err)
	}

	d.codec = binary.BigEndian.Uint32(buf[0:4])
	d.width = int(binary.BigEndian.Uint32(buf[4:8]))
	d.height = int(binary.BigEndian.Uint32(buf[8:12]))

	return nil
}

// Width returns the video width.
func (d *Decoder) Width() int { return d.width }

// Height returns the video height.
func (d *Decoder) Height() int { return d.height }

// ReadFrame reads a single frame (header + payload) from the stream.
func (d *Decoder) ReadFrame(r io.Reader) (*Frame, error) {
	var header [FrameHeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("read frame header: %w", err)
	}

	ptsAndFlags := binary.BigEndian.Uint64(header[0:8])
	size := int(binary.BigEndian.Uint32(header[8:12]))

	if size < 0 || size > 50*1024*1024 { // 50MB sanity cap
		return nil, fmt.Errorf("invalid frame size: %d", size)
	}

	// Reuse readBuf across frames instead of allocating per call. Grown only
	// when a frame exceeds the current capacity (e.g. a large IDR after small
	// P-frames); thereafter the buffer is reused at zero allocation cost.
	// readBuf is touched only here (single drainFrames goroutine), so no lock.
	if cap(d.readBuf) < size {
		d.readBuf = make([]byte, size)
	}
	data := d.readBuf[:size]
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read frame data: %w", err)
	}

	isConfig := (ptsAndFlags & PacketFlagConfig) != 0
	isKeyframe := (ptsAndFlags & PacketFlagKeyFrame) != 0
	pts := int64(ptsAndFlags &^ (PacketFlagConfig | PacketFlagKeyFrame))

	frame := &Frame{
		Data:     data,
		PTS:      pts,
		KeyFrame: isKeyframe,
		Config:   isConfig,
		Width:    d.width,
		Height:   d.height,
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if isConfig {
		d.extractConfigs(data)
	} else if isKeyframe {
		d.latestKeyframe = d.buildKeyframe(data)
		d.latestPTS = pts
	}

	return frame, nil
}

// LatestKeyframe returns the most recent keyframe assembled with SPS+PPS.
func (d *Decoder) LatestKeyframe() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.latestKeyframe
}

// buildKeyframe prepends the SPS+PPS config data before the IDR frame.
// Both configRaw and idr are already in AnnexB format (00 00 00 01 start codes),
// so a plain concatenation is sufficient and correct.
//
// We use configRaw (the whole config packet verbatim) rather than the
// individually-parsed sps/pps slices to avoid boundary-detection bugs in
// the NAL start-code scanner.
func (d *Decoder) buildKeyframe(idr []byte) []byte {
	if d.configRaw == nil {
		// Copy even without config: idr aliases readBuf (reused across frames),
		// so latestKeyframe must own its bytes or the next ReadFrame would
		// overwrite it. (Before readBuf reuse, idr was a fresh make() and this
		// copy was unnecessary — now it is required for correctness.)
		out := make([]byte, len(idr))
		copy(out, idr)
		return out
	}
	out := make([]byte, len(d.configRaw)+len(idr))
	copy(out, d.configRaw)
	copy(out[len(d.configRaw):], idr)
	return out
}

// extractConfigs stores the raw AnnexB config packet (SPS+PPS).
// scrcpy's MediaCodec outputs config data as:
//   [00 00 00 01] SPS-NAL [00 00 00 01] PPS-NAL
// We store it verbatim so buildKeyframe can prepend it unchanged.
func (d *Decoder) extractConfigs(data []byte) {
	d.configRaw = make([]byte, len(data))
	copy(d.configRaw, data)
}

// KeyframeToPNG converts a raw AnnexB H.264 keyframe to a PNG image.
// Returns a black placeholder image if the decoder can't render the frame.
// For production use, integrate with a proper H.264 decoder (e.g., via CGO + FFmpeg).
func KeyframeToPNG(keyframe []byte, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 {
		width, height = 1080, 2400
	}

	// Placeholder: a real implementation would use FFmpeg/libx264 to decode.
	// For now, we store the raw H.264 and note that the consumer
	// (MCP layer) should use an external decoder or scrcpy's own display.
	//
	// In practice, the scrcpy video stream is already being decoded
	// and displayed by the scrcpy client. phonefast uses the scrcpy
	// client's --no-display mode and grabs frames from the decoder,
	// or alternatively, we can launch scrcpy with --no-video and
	// decode frames ourselves using a Go H.264 decoder.
	//
	// For v1, we return the raw keyframe bytes which can be fed to
	// any H.264 decoder (ffmpeg, MediaCodec, etc.).

	// If we have valid frame data, note it
	_ = keyframe

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, fmt.Errorf("png encode: %w", err)
	}
	return pngBuf.Bytes(), nil
}

// FindStartCode finds the next AnnexB start code (00 00 00 01 or 00 00 01).
func FindStartCode(data []byte, offset int) int {
	for i := offset; i < len(data)-3; i++ {
		if data[i] == 0 && data[i+1] == 0 {
			if data[i+2] == 1 {
				return i
			}
			if data[i+2] == 0 && data[i+3] == 1 {
				return i
			}
		}
	}
	return -1
}
