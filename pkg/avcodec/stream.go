package avcodec

import "time"

// StreamDecoder is a persistent H.264 decoder fed with the live scrcpy
// video stream (config packets, IDR and P-frames) that maintains the latest
// decoded frame for screenshot use.
//
// Screenshots served from a StreamDecoder never need RESET_VIDEO: the
// decoder state tracks the stream continuously, so the latest frame is the
// current screen (scrcpy sets KEY_REPEAT_PREVIOUS_FRAME_AFTER=100ms, so
// frames keep flowing ~10fps even when the screen is static).
//
// Feed is called from a single goroutine (the session's drain loop).
// LatestFrameAt/EncodeLatest are called from request goroutines; the cached
// frame is encoded under the internal lock, so no per-call snapshot copy is
// needed — the encoder works on the cache in place and returns fresh bytes.
type StreamDecoder interface {
	// Feed decodes one AnnexB frame (SPS/PPS config, IDR, or P-frame) and
	// caches the latest decoded frame. Config-only packets (SPS+PPS, sent
	// by scrcpy after each pipeline reset and on resolution changes) are
	// held and prepended to the IDR that follows them. The input is copied;
	// the caller may reuse its buffer immediately.
	Feed(data []byte) error

	// LatestFrameAt returns the arrival time of the latest decoded frame,
	// without copying it. ok=false until the first frame arrives. Callers
	// use this for the freshness decision, then EncodeLatest to encode.
	LatestFrameAt() (at time.Time, ok bool)

	// EncodeLatest encodes the CURRENT cached frame (the newest available
	// at call time, which may be newer than the frame LatestFrameAt
	// returned) to the requested image format. JPEG encodes directly from
	// YUV420P (no color round-trip); PNG converts via a pure-Go YUV→RGBA.
	// Returns encoded bytes, dimensions, MIME, and ok=false when no frame
	// has been decoded yet or encoding failed.
	EncodeLatest(format ImageFormat) (data []byte, w, h int, mime string, ok bool)

	// Close releases all resources. After Close the decoder must not be used.
	Close() error
}
