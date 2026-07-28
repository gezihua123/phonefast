//go:build cgo

package avcodec

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log"
	"time"

	"github.com/asticode/go-astiav"
)

// astiavDecoder implements Decoder using CGO bindings to FFmpeg's libavcodec
// and libswscale.
//
// Not safe for concurrent use — the caller (DeviceActor) guarantees
// single-goroutine access. No mutex needed.
type astiavDecoder struct {
	codec    *astiav.Codec
	codecCtx *astiav.CodecContext // persistent codec context, reused across decodes
	swsCtx   *astiav.SoftwareScaleContext

	// rgbaScratch holds the tightly-packed RGBA pixels wrapped by the
	// image.NRGBA handed to the PNG/JPEG encoder. Reused across Decode calls
	// (the caller guarantees single-goroutine access), so screenshots no
	// longer allocate a fresh w*h*4 (~8.3MB at 1080p) Go image per call.
	// Grown only when dimensions increase.
	rgbaScratch []byte

	// Cached dimensions so we know when to recreate the scaler.
	width  int
	height int
}

// NewDecoder creates a go-astiav-based H.264 → image decoder.
// Returns an error if FFmpeg shared libraries are missing or the H.264
// decoder is unavailable.
func NewDecoder(width, height int) (Decoder, error) {
	codec := astiav.FindDecoder(astiav.CodecIDH264)
	if codec == nil {
		return nil, fmt.Errorf("%w: H.264 decoder not found in FFmpeg", ErrNotAvailable)
	}

	return &astiavDecoder{
		codec:  codec,
		width:  width,
		height: height,
	}, nil
}

// getCodecCtx returns the persistent codec context, initializing it lazily.
// Since we only decode single IDR keyframes (no flush needed between frames),
// the same context can be reused indefinitely — the decoder naturally resets
// its reference frames when it encounters a new IDR.
func (d *astiavDecoder) getCodecCtx() (*astiav.CodecContext, error) {
	if d.codecCtx != nil {
		return d.codecCtx, nil
	}
	codecCtx := astiav.AllocCodecContext(d.codec)
	if codecCtx == nil {
		return nil, fmt.Errorf("alloc codec context failed")
	}
	codecCtx.SetThreadCount(1)
	// Single IDR keyframe decode: no threading needed. Multi-threading
	// doubles DPB allocation and adds slice-merge overhead for zero gain
	// on single-frame decode. One thread = half the DPB memory, less
	// synchronization, and on small frames lower latency.
	if err := codecCtx.Open(d.codec, nil); err != nil {
		codecCtx.Free()
		return nil, fmt.Errorf("open codec: %w", err)
	}
	d.codecCtx = codecCtx
	return codecCtx, nil
}

// Decode converts a raw AnnexB H.264 keyframe to a PNG or JPEG image.
// Not safe for concurrent use — the caller guarantees single-goroutine access.
func (d *astiavDecoder) Decode(keyframe []byte, width, height int, format ImageFormat) ([]byte, int, int, string, error) {

	if len(keyframe) == 0 {
		return nil, 0, 0, "", errors.New("avcodec: empty keyframe")
	}

	tTotal := time.Now()

	// Create/retrieve persistent codec context.
	// Single IDR keyframe decode: no flush needed — the decoder resets its
	// reference frames naturally on each new IDR.
	t0 := time.Now()
	codecCtx, err := d.getCodecCtx()
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("%w: %v", ErrNotAvailable, err)
	}
	tCtx := time.Since(t0)

	// ---- Step 1: determine effective dimensions & recreate scaler if needed ----
	effectiveW, effectiveH := d.width, d.height
	if width > 0 && height > 0 {
		effectiveW, effectiveH = width, height
	}
	if effectiveW != d.width || effectiveH != d.height {
		d.width = effectiveW
		d.height = effectiveH
		if d.swsCtx != nil {
			d.swsCtx.Free()
			d.swsCtx = nil
		}
	}

	// ---- Step 2: send the entire AnnexB keyframe as one packet ----
	t0 = time.Now()
	pkt := astiav.AllocPacket()
	if err := pkt.FromData(keyframe); err != nil {
		pkt.Free()
		return nil, 0, 0, "", newDecodeError("packet", err)
	}
	if sendErr := codecCtx.SendPacket(pkt); sendErr != nil {
		pkt.Free()
		return nil, 0, 0, "", newDecodeError("send_packet", sendErr)
	}
	pkt.Free()
	tSend := time.Since(t0)

	// ---- Step 3: receive decoded frame ----
	// Single IDR keyframe decode: exactly 1 output frame. No loop needed.
	// No flush — the persistent codec context keeps its DPB allocated,
	// and each new IDR naturally overwrites reference frames in-place.
	t0 = time.Now()
	frame := astiav.AllocFrame()
	if err := codecCtx.ReceiveFrame(frame); err != nil {
		frame.Free()
		return nil, 0, 0, "", newDecodeError("receive_frame", err)
	}
	defer frame.Unref()
	defer frame.Free()
	tRecv := time.Since(t0)

	// ---- Step 4: scale YUV420P → RGBA ----
	t0 = time.Now()
	rgbaFrame, err := d.scaleToRGBA(frame, effectiveW, effectiveH)
	if err != nil {
		return nil, 0, 0, "", newDecodeError("scale", err)
	}
	defer rgbaFrame.Free()
	tScale := time.Since(t0)

	// ---- Step 5: convert to Go image.Image ----
	t0 = time.Now()
	img, err := d.frameToImage(rgbaFrame)
	if err != nil {
		return nil, 0, 0, "", newDecodeError("to_image", err)
	}
	outW, outH := rgbaFrame.Width(), rgbaFrame.Height()
	tToImg := time.Since(t0)

	// ---- Step 6: encode to PNG or JPEG ----
	t0 = time.Now()
	var buf bytes.Buffer
	switch format {
	case FormatJPEG:
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	default:
		err = png.Encode(&buf, img)
	}
	if err != nil {
		return nil, 0, 0, "", newDecodeError("encode", err)
	}
	tEncode := time.Since(t0)

	tTotalElapsed := time.Since(tTotal)
	log.Printf("avcodec timing: ctx=%v send=%v recv=%v scale=%v toImg=%v encode=%v TOTAL=%v",
		tCtx, tSend, tRecv, tScale, tToImg, tEncode, tTotalElapsed)

	return buf.Bytes(), outW, outH, format.String(), nil
}

// Close releases all resources. Not safe for concurrent use.
func (d *astiavDecoder) Close() error {
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

// scaleToRGBA converts a YUV420P frame to RGBA at the target dimensions.
func (d *astiavDecoder) scaleToRGBA(src *astiav.Frame, dstW, dstH int) (*astiav.Frame, error) {
	if d.swsCtx == nil {
		swsCtx, err := astiav.CreateSoftwareScaleContext(
			src.Width(), src.Height(), src.PixelFormat(),
			dstW, dstH, astiav.PixelFormatRgba,
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

// frameToImage converts an RGBA astiav.Frame to a Go image.Image without a
// per-call allocation: the C frame pixels are copied (packed, align=1) into
// the decoder's persistent rgbaScratch, and the returned *image.NRGBA wraps
// that scratch. The image is valid until the NEXT Decode call — callers
// (Decode → png/jpeg.Encode) consume it synchronously before returning.
func (d *astiavDecoder) frameToImage(frame *astiav.Frame) (image.Image, error) {
	w, h := frame.Width(), frame.Height()
	need := w * h * 4
	if need <= 0 {
		return nil, fmt.Errorf("invalid frame dimensions %dx%d", w, h)
	}
	if cap(d.rgbaScratch) < need {
		d.rgbaScratch = make([]byte, need)
	}
	pix := d.rgbaScratch[:need]
	// align=1 → tightly packed linesize == w*4, matching NRGBA.Stride.
	if _, err := frame.ImageCopyToBuffer(pix, 1); err != nil {
		return nil, fmt.Errorf("copy to buffer: %w", err)
	}
	return &image.NRGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}, nil
}

// ---- NAL unit helpers ----

// splitNALUnits splits AnnexB data into individual NAL units.
// The start code (00 00 00 01 or 00 00 01) is stripped from each NAL,
// but the NAL header byte (with nal_type in bits 0–4) is included.
func splitNALUnits(data []byte) [][]byte {
	var nals [][]byte
	pos := 0
	for pos < len(data) {
		start := nalStartPos(data, pos)
		if start == -1 {
			break
		}
		// Skip past the start code.
		pos = start + nalStartCodeLen(data, start)
		end := nalStartPos(data, pos)
		if end == -1 {
			end = len(data)
		}
		if pos < end {
			nals = append(nals, data[pos:end])
		}
	}
	return nals
}

// nalStartPos finds the next AnnexB start code position, or -1.
func nalStartPos(data []byte, offset int) int {
	for i := offset; i < len(data)-2; i++ {
		if data[i] == 0 && data[i+1] == 0 {
			if data[i+2] == 1 {
				return i // 3-byte start code
			}
			if i+3 < len(data) && data[i+3] == 1 {
				return i // 4-byte start code
			}
		}
	}
	return -1
}

// nalStartCodeLen returns 4 for 00 00 00 01, 3 for 00 00 01.
func nalStartCodeLen(data []byte, pos int) int {
	if pos+3 < len(data) && data[pos] == 0 && data[pos+1] == 0 && data[pos+2] == 0 && data[pos+3] == 1 {
		return 4
	}
	return 3
}
