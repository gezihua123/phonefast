package avcodec

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// TestPNGToJPEGRoundTrip verifies PNGToJPEG produces decodable JPEG output
// from a PNG input.
func TestPNGToJPEGRoundTrip(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 17)
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	jpegData, err := PNGToJPEG(pngBuf.Bytes(), 85)
	if err != nil {
		t.Fatalf("PNGToJPEG: %v", err)
	}
	if _, format, err := image.Decode(bytes.NewReader(jpegData)); err != nil {
		t.Fatalf("output does not decode: %v", err)
	} else if format != "jpeg" {
		t.Errorf("decoded format = %q, want %q", format, "jpeg")
	}
}
