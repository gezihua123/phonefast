//go:build cgo

package avcodec

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"sync"
	"time"

	"github.com/asticode/go-astiav"
)

// astiavStreamDecoder implements StreamDecoder on top of go-astiav.
//
// The codec context is persistent: Feed sends each stream packet and drains
// every decoded frame, caching the latest one as packed YUV420P. Consecutive
// P-frames accumulate decoder state naturally, so the cached frame tracks
// the screen within one frame period (~100ms at the stream's 10fps floor).
type astiavStreamDecoder struct {
	codec    *astiav.Codec
	codecCtx *astiav.CodecContext

	// swsCtx converts non-YUV420P device output to YUV420P (rare: MediaCodec
	// H.264 encoders output 420P or NV12). Owned by Feed's goroutine.
	swsCtx *astiav.SoftwareScaleContext

	// pendingConfig holds a config-only packet (SPS+PPS, sent by scrcpy after
	// each RESET_VIDEO pipeline recreation and on resolution changes) until
	// the IDR that always follows it arrives. Fed separately, FFmpeg rejects
	// the duplicate parameter sets with INVALIDDATA; prepending it to the
	// next frame is exactly the concatenation the legacy keyframe path uses.
	pendingConfig []byte

	// mu guards the cached frame (buf/size/w/h/at) shared between Feed and
	// Frame. The decode state (codecCtx) itself is single-goroutine (Feed).
	mu   sync.Mutex
	buf  []byte // latest frame, packed YUV420P (Y Cb Cr contiguous, align=1)
	size int    // valid prefix of buf
	w    int
	h    int
	at   time.Time
}

// NewStreamDecoder creates a persistent stream decoder. CGO builds only;
// non-CGO builds return ErrNotAvailable (see stream_nocgo.go).
func NewStreamDecoder() (StreamDecoder, error) {
	codec := astiav.FindDecoder(astiav.CodecIDH264)
	if codec == nil {
		return nil, fmt.Errorf("%w: H.264 decoder not found in FFmpeg", ErrNotAvailable)
	}
	return &astiavStreamDecoder{codec: codec}, nil
}

func (d *astiavStreamDecoder) getCodecCtx() (*astiav.CodecContext, error) {
	if d.codecCtx != nil {
		return d.codecCtx, nil
	}
	codecCtx := astiav.AllocCodecContext(d.codec)
	if codecCtx == nil {
		return nil, fmt.Errorf("alloc codec context failed")
	}
	// Single-threaded: at ~10fps decode cost is a few ms/frame; threading
	// doubles DPB memory and adds slice-merge overhead for zero gain here.
	codecCtx.SetThreadCount(1)
	if err := codecCtx.Open(d.codec, nil); err != nil {
		codecCtx.Free()
		return nil, fmt.Errorf("open codec: %w", err)
	}
	d.codecCtx = codecCtx
	return codecCtx, nil
}

// Feed decodes one AnnexB frame and caches the latest decoded frame.
func (d *astiavStreamDecoder) Feed(data []byte) error {
	if len(data) == 0 {
		return errors.New("avcodec: empty stream frame")
	}

	// Config-only packet (SPS+PPS, no slice): hold it and prepend it to the
	// next frame. scrcpy always sends the config immediately before the IDR
	// it describes, so the concatenation is a complete decodable unit.
	if hasNoSlice(data) {
		d.pendingConfig = append(d.pendingConfig[:0], data...)
		return nil
	}
	if d.pendingConfig != nil {
		joined := make([]byte, 0, len(d.pendingConfig)+len(data))
		joined = append(joined, d.pendingConfig...)
		joined = append(joined, data...)
		d.pendingConfig = nil
		data = joined
	}

	codecCtx, err := d.getCodecCtx()
	if err != nil {
		return err
	}

	pkt := astiav.AllocPacket()
	if err := pkt.FromData(data); err != nil {
		pkt.Free()
		return newDecodeError("stream packet", err)
	}
	sendErr := codecCtx.SendPacket(pkt)
	pkt.Free()
	if sendErr != nil {
		return newDecodeError("stream send_packet", sendErr)
	}

	// Drain every frame this packet produced (usually 0 or 1).
	for {
		frame := astiav.AllocFrame()
		if err := codecCtx.ReceiveFrame(frame); err != nil {
			frame.Free()
			if errors.Is(err, astiav.ErrEagain) || errors.Is(err, astiav.ErrEof) {
				return nil
			}
			return newDecodeError("stream receive_frame", err)
		}
		d.cacheFrame(frame)
		frame.Unref()
		frame.Free()
	}
}

// cacheFrame converts the decoded frame to packed YUV420P and stores it as
// the latest cached frame. Non-fatal on conversion failure (keeps the
// previous frame — the next natural IDR, at most 10s away, resyncs).
func (d *astiavStreamDecoder) cacheFrame(frame *astiav.Frame) {
	w, h := frame.Width(), frame.Height()
	if w <= 0 || h <= 0 {
		return
	}

	var src *astiav.Frame
	release := false
	if frame.PixelFormat() == astiav.PixelFormatYuv420P {
		src = frame
	} else {
		scaled, err := d.scaleToYUV420P(frame, w, h)
		if err != nil {
			return
		}
		src = scaled
		release = true
	}

	bufSize, err := src.ImageBufferSize(1)
	if err != nil {
		if release {
			src.Unref()
			src.Free()
		}
		return
	}

	d.mu.Lock()
	if cap(d.buf) < bufSize {
		d.buf = make([]byte, bufSize)
	}
	if _, err := src.ImageCopyToBuffer(d.buf[:bufSize], 1); err == nil {
		d.size = bufSize
		d.w, d.h = w, h
		d.at = time.Now()
	}
	d.mu.Unlock()

	if release {
		src.Unref()
		src.Free()
	}
}

// scaleToYUV420P converts a non-420P frame to YUV420P at its own dimensions.
func (d *astiavStreamDecoder) scaleToYUV420P(src *astiav.Frame, dstW, dstH int) (*astiav.Frame, error) {
	if d.swsCtx == nil {
		swsCtx, err := astiav.CreateSoftwareScaleContext(
			src.Width(), src.Height(), src.PixelFormat(),
			dstW, dstH, astiav.PixelFormatYuv420P,
			astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBilinear),
		)
		if err != nil {
			return nil, fmt.Errorf("create scaler: %w", err)
		}
		d.swsCtx = swsCtx
	}

	dst := astiav.AllocFrame()
	if err := d.swsCtx.ScaleFrame(src, dst); err != nil {
		dst.Free()
		return nil, fmt.Errorf("scale: %w", err)
	}
	return dst, nil
}

// LatestFrameAt returns the arrival time of the latest cached frame without
// copying it. Callers pair this with EncodeLatest.
func (d *astiavStreamDecoder) LatestFrameAt() (time.Time, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.size == 0 {
		return time.Time{}, false
	}
	return d.at, true
}

// EncodeLatest encodes the current cached frame in place, holding the cache
// lock for the encode duration (~15-40ms for JPEG). No snapshot copy: the
// old Frame()+Encode design allocated a fresh ~1.7MB per screenshot (at
// 720x1600), which over an hour-long stress run churned >1GB of garbage
// through the GC. Feed stalls briefly while the lock is held — the video
// socket buffer absorbs ~0.2 frames at 10fps, a non-issue.
//
// The encoded frame may be NEWER than the one LatestFrameAt reported (the
// cache can update between the two calls) — strictly fresher, so callers
// that validated the older timestamp remain correct.
func (d *astiavStreamDecoder) EncodeLatest(format ImageFormat) ([]byte, int, int, string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.size == 0 {
		return nil, 0, 0, "", false
	}
	data, mime, err := encodeYUV420P(d.buf[:d.size], d.w, d.h, format)
	if err != nil {
		return nil, 0, 0, "", false
	}
	return data, d.w, d.h, mime, true
}

// encodeYUV420P converts a packed YUV420P buffer (Y Cb Cr contiguous,
// align=1) to the requested image format. Stateless — safe for any caller.
func encodeYUV420P(yuv []byte, w, h int, format ImageFormat) ([]byte, string, error) {
	if w <= 0 || h <= 0 || len(yuv) < w*h*3/2 {
		return nil, "", fmt.Errorf("avcodec: invalid yuv420p frame %dx%d (%d bytes)", w, h, len(yuv))
	}

	if format == FormatJPEG {
		// Direct YUV→JPEG: jpeg.Encode is natively YUV-based, so wrap the
		// planes as image.YCbCr and skip the YUV→RGBA→YUV round-trip.
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, yuvToYCbCr(yuv, w, h), &jpeg.Options{Quality: 90}); err != nil {
			return nil, "", newDecodeError("jpeg encode", err)
		}
		return buf.Bytes(), format.String(), nil
	}

	// PNG path: pure-Go YUV420P→RGBA then encode. Rare (legacy Observe
	// path); go-astiav's Frame can't wrap a Go-owned buffer, so no swscale.
	var buf bytes.Buffer
	if err := png.Encode(&buf, yuv420PToNRGBA(yuv, w, h)); err != nil {
		return nil, "", newDecodeError("png encode", err)
	}
	return buf.Bytes(), format.String(), nil
}

// hasNoSlice reports whether the AnnexB data contains no slice NALs
// (non-IDR type 1 or IDR type 5) — i.e. it is a parameter-set-only packet
// (SPS/PPS, possibly with SEI/AUD). Such packets carry no picture data and
// are handled by pendingConfig instead of being fed standalone.
func hasNoSlice(data []byte) bool {
	for pos := 0; pos+3 < len(data); {
		if data[pos] != 0 || data[pos+1] != 0 {
			pos++
			continue
		}
		if data[pos+2] == 1 {
			pos += 3
		} else if data[pos+3] == 1 {
			pos += 4
		} else {
			pos++
			continue
		}
		if pos >= len(data) {
			break
		}
		switch data[pos] & 0x1F {
		case 1, 5: // slice / IDR — has picture data
			return false
		}
	}
	return true
}

// yuvToYCbCr wraps a packed YUV420P buffer (Y Cb Cr contiguous, align=1) as
// image.YCbCr without copying — the caller owns the buffer for the image's
// lifetime (Encode consumes it synchronously).
func yuvToYCbCr(yuv []byte, w, h int) *image.YCbCr {
	yStride := w
	cStride := w / 2
	ySize := h * yStride
	cSize := (h / 2) * cStride
	return &image.YCbCr{
		Y:              yuv[:ySize],
		Cb:             yuv[ySize : ySize+cSize],
		Cr:             yuv[ySize+cSize : ySize+2*cSize],
		YStride:        yStride,
		CStride:        cStride,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect:           image.Rect(0, 0, w, h),
	}
}

// yuv420PToNRGBA converts packed YUV420P to NRGBA using BT.601 limited-range
// fixed-point math (H.264 video levels: Y 16-235, Cb/Cr 16-240). ~1.1M px
// at 1080p ≈ a few ms — acceptable for the rare PNG path.
func yuv420PToNRGBA(yuv []byte, w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	yStride := w
	cStride := w / 2
	ySize := h * yStride
	cSize := (h / 2) * cStride
	yPlane := yuv[:ySize]
	cbPlane := yuv[ySize : ySize+cSize]
	crPlane := yuv[ySize+cSize : ySize+2*cSize]

	// 8.8 fixed point, clamped per pixel.
	for j := 0; j < h; j++ {
		rowY := yPlane[j*yStride : (j+1)*yStride]
		rowCb := cbPlane[(j/2)*cStride : (j/2+1)*cStride]
		rowCr := crPlane[(j/2)*cStride : (j/2+1)*cStride]
		out := img.Pix[j*img.Stride : (j+1)*img.Stride]
		for i := 0; i < w; i++ {
			y := int(rowY[i])
			cb := int(rowCb[i/2]) - 128
			cr := int(rowCr[i/2]) - 128

			// BT.601 limited→full, coefficients ×256.
			r := (298*(y-16) + 409*cr + 128) >> 8
			g := (298*(y-16) - 100*cb - 208*cr + 128) >> 8
			b := (298*(y-16) + 516*cb + 128) >> 8

			o := i * 4
			out[o] = clamp8(r)
			out[o+1] = clamp8(g)
			out[o+2] = clamp8(b)
			out[o+3] = 0xFF
		}
	}
	return img
}

func clamp8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// Close releases all resources. Not safe for concurrent use.
func (d *astiavStreamDecoder) Close() error {
	if d.codecCtx != nil {
		d.codecCtx.Free()
		d.codecCtx = nil
	}
	if d.swsCtx != nil {
		d.swsCtx.Free()
		d.swsCtx = nil
	}
	return nil
}
