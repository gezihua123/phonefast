package avcodec

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestStaticDecode verifies the CGO/static-FFmpeg decoder can turn a fixed
// H.264 AnnexB keyframe into a valid PNG. This is the cross-platform smoke
// test that the static FFmpeg link actually works on a given target — it runs
// with zero phone hardware, so it can be executed natively (darwin) and inside
// a container (linux) or under wine (windows).
func TestStaticDecode(t *testing.T) {
	if os.Getenv("AVCODEC_SKIP_TEST") != "" {
		t.Skip("AVCODEC_SKIP_TEST set")
	}

	keyframe, err := os.ReadFile(filepath.Join("testdata", "keyframe.h264"))
	if err != nil {
		t.Fatalf("read keyframe: %v", err)
	}
	if len(keyframe) == 0 {
		t.Fatal("empty keyframe test vector")
	}

	const w, h = 320, 240
	dec, err := NewDecoder(w, h)
	if err != nil {
		t.Fatalf("NewDecoder: %v\n"+
			"  → 如果是 CGO_ENABLED=0，本测试不适用（静态链接未编译）\n"+
			"  → 如果是库缺失，检查静态 FFmpeg 是否链接进二进制", err)
	}
	defer dec.Close()

	out, outW, outH, mime, err := dec.Decode(keyframe, w, h, FormatPNG)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("decode produced empty output")
	}
	if mime != "image/png" {
		t.Fatalf("mime = %q, want image/png", mime)
	}
	if outW != w || outH != h {
		t.Fatalf("dims = %dx%d, want %dx%d", outW, outH, w, h)
	}

	// Validate PNG signature + parseable image.
	if !bytes.HasPrefix(out, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("output missing PNG signature: %x", out[:8])
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("png bounds = %v, want %dx%d", img.Bounds(), w, h)
	}

	t.Logf("OK: decoded %d-byte H.264 keyframe → %d-byte PNG %dx%d",
		len(keyframe), len(out), outW, outH)
}
