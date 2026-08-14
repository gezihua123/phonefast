package avcodec

import (
	"bytes"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder for PNGToJPEG
	"sync"
)

// jpegBufPool reuses bytes.Buffer allocations across PNGToJPEG calls.
// Each buffer is typically ~200-500KB (JPEG-encoded 720×1600 image).
var jpegBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// PNGToJPEG decodes a PNG image and re-encodes it as JPEG at the given
// quality (1-100). Used on the CLI ffmpeg fallback path to shrink MCP
// screenshot payloads ~10× without downscaling — native resolution is
// preserved. The primary CGO path returns JPEG directly from the decoder
// and bypasses this function entirely.
func PNGToJPEG(png []byte, quality int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		return nil, err
	}
	buf := jpegBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer jpegBufPool.Put(buf)

	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	// Copy out before returning to pool: buf.Bytes() aliases the internal
	// buffer and will be invalidated by the next Get() call.
	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	return out, nil
}
