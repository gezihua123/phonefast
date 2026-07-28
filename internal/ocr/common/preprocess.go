package common

import (
	"image"
	"math"
	"sync"
)

// Postprocessing buffer pools — shared across engines, serialized by the
// Service mutex. Pools are only used for ExtractTextBoxes' intermediate bool
// masks which have no external references (safe to reuse).
var boolPool = sync.Pool{New: func() any { s := make([]bool, 0, 128*1024); return &s }}

// growFloat32 returns a []float32 of length n, reusing buf's backing array when
// it is large enough and allocating (and growing) only when n exceeds cap(buf).
// The returned slice aliases buf when reused. Used to keep a long-lived scratch
// tensor buffer across serialized OCR calls instead of allocating ~5–6 MB per
// call. The caller must not reuse the returned slice while ORT still references
// it (zero-copy CreateTensorWithDataAsOrtValue) — i.e. not until the input
// Value built from it is Closed.
func growFloat32(buf []float32, n int) []float32 {
	if cap(buf) >= n {
		return buf[:n]
	}
	return make([]float32, n)
}

// getBool gets a []bool slice of at least `need` capacity from the pool.
func getBool(need int) []bool {
	pp := boolPool.Get().(*[]bool)
	s := *pp
	if cap(s) < need {
		s = make([]bool, need)
	} else {
		s = s[:need]
	}
	*pp = s
	return s
}

// putBool returns a []bool slice to the pool (cap ≤ 4MB only to bound memory).
func putBool(s []bool) {
	if s != nil && cap(s) <= 4*1024*1024 {
		boolPool.Put(&s)
	}
}

// RecHeight is the PP-OCR recognition model input height (training resolution).
const RecHeight = 48

// ── Detection Preprocessing ──────────────────────────────────────

// DetPreprocess converts a Go image.Image into a normalized float32 CHW
// tensor for the PP-OCR detection model.
//
// Pipeline:
//  1. Compute target resolution: cap longer side at maxSide pixels
//     (default 1024 for speed, 0 = keep original)
//  2. Cap max dimension and round to 32×
//  3. Resize with bilinear (or fast copy if near 1:1)
//  4. No padding — the model accepts dynamic input shapes [1, 3, ?, ?]
//  5. Scale: pixel / 255 → [0, 1]
//  6. Normalize: (val - mean) / std
//
// maxSide=0 means keep original resolution (quality mode, up to ~173ms).
// maxSide=1024 is the default speed/quality trade-off (~35ms for 1080×2400).
//
// Returns tensor data, resized width, resized height, and tensor shape.
func DetPreprocess(img image.Image, maxSide int) ([]float32, int, int, []int64) {
	return DetPreprocessInto(img, maxSide, nil)
}

// DetPreprocessInto is the buffer-reusing variant of DetPreprocess. It writes
// the detection input tensor into `buf` (growing it as needed) and returns the
// (possibly reallocated) slice along with the geometry. The caller keeps the
// returned slice to reuse on the next call — this eliminates the ~5.4 MB
// per-call allocation that DetPreprocess would otherwise make.
//
// Safety: the returned slice is referenced zero-copy by ORT's
// CreateTensorWithDataAsOrtValue; it must not be reused until the input Value
// built from it has been Closed. The Detector enforces this by reusing its
// detBuf only across serialized Recognize calls (each closes its input before
// returning).
func DetPreprocessInto(img image.Image, maxSide int, buf []float32) ([]float32, int, int, []int64) {
	const (
		minSide  = 32
		detMeanR = 0.485
		detMeanG = 0.456
		detMeanB = 0.406
		detStdR  = 0.229
		detStdG  = 0.224
		detStdB  = 0.225
	)

	bounds := img.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	// Compute target resolution
	resizeH, resizeW := origH, origW
	if maxSide > 0 {
		longer := origW
		if origH > origW {
			longer = origH
		}
		if longer > maxSide {
			ratio := float64(maxSide) / float64(longer)
			resizeW = int(float64(origW) * ratio)
			resizeH = int(float64(origH) * ratio)
		}
	}

	// Round to nearest multiple of 32
	resizeH = int(math.Round(float64(resizeH)/32) * 32)
	resizeW = int(math.Round(float64(resizeW)/32) * 32)
	if resizeH < minSide {
		resizeH = minSide
	}
	if resizeW < minSide {
		resizeW = minSide
	}

	resized := ResizeImage(img, resizeW, resizeH)

	// Build CHW float32 tensor using direct Pix access (no per-pixel function call).
	// Reuse the caller's buffer when it's big enough; only allocate (and grow)
	// when the image is larger than anything seen so far.
	pix := resized.Pix
	stride := resized.Stride
	tensorSize := 3 * resizeH * resizeW
	tensor := growFloat32(buf, tensorSize)
	for y := 0; y < resizeH; y++ {
		rowPix := pix[y*stride:]
		for x := 0; x < resizeW; x++ {
			px := x * 4
			r := rowPix[px]
			g := rowPix[px+1]
			b := rowPix[px+2]
			base := y*resizeW + x
			tensor[base] = float32(float64(r)/255.0-detMeanR) / detStdR
			tensor[resizeH*resizeW+base] = float32(float64(g)/255.0-detMeanG) / detStdG
			tensor[2*resizeH*resizeW+base] = float32(float64(b)/255.0-detMeanB) / detStdB
		}
	}
	// Release resized intermediate (~2 MB for 480×1024) after tensor is built.
	resized = nil

	shape := []int64{1, 3, int64(resizeH), int64(resizeW)}
	return tensor, resizeW, resizeH, shape
}

// ── Batch Recognition Preprocessing ─────────────────────────────

// RecMaxWidth is the PP-OCR rec input width ceiling. The rec model is
// shape-specialized to this width on conversion (see scripts/convert-ncnn.sh);
// single engines pad each crop to it. Defined once here so the ncnn engine
// and the preprocess helpers share the single source of truth.
const RecMaxWidth = 320

// RecResizeWidth computes the resized width for a crop: H=RecHeight keeping
// aspect ratio, capped at capW (PP-OCR rec ceiling is RecMaxWidth), floored at 1.
func RecResizeWidth(crop image.Image, capW int) int {
	bounds := crop.Bounds()
	ratio := float64(bounds.Dx()) / float64(bounds.Dy())
	rw := int(math.Round(float64(RecHeight) * ratio))
	if rw > capW {
		rw = capW
	}
	if rw < 1 {
		rw = 1
	}
	return rw
}

// RecBatchPreprocess converts multiple cropped text images into a single
// batched float32 tensor for one-pass recognition inference.
//
// Pipeline:
//  1. For each crop: resize to H=RecHeight, W=dynamic (keep aspect ratio)
//  2. Find max width across all crops, pad all to that width
//  3. Stack into [B, 3, RecHeight, maxW] float32 tensor
//  4. Normalize each: (pixel/255 - 0.5) / 0.5
func RecBatchPreprocess(crops []image.Image) ([]float32, int) {
	return RecBatchPreprocessInto(crops, nil)
}

// RecBatchPreprocessInto is the buffer-reusing variant of RecBatchPreprocess.
// It writes the batched [B,3,RecHeight,maxW] tensor into `buf` (grown on demand)
// and returns the (possibly reallocated) slice. Eliminates the ~6 MB per-call
// allocation when the caller reuses one scratch buffer across serialized calls.
//
// Safety: same zero-copy ORT contract as DetPreprocessInto — do not reuse the
// returned slice until the input Value built from it is Closed.
//
// It also releases each resized intermediate as soon as its channel is written,
// so the B resized RGBA images (~60 KB each) don't all coexist with the tensor.
func RecBatchPreprocessInto(crops []image.Image, buf []float32) ([]float32, int) {
	if len(crops) == 0 {
		return nil, 0
	}

	B := len(crops)

	// Step 1: Resize each crop to H=RecHeight, record widths.
	resizedWidths := make([]int, B)
	resizedImages := make([]*image.RGBA, B)
	maxW := 0
	for i, crop := range crops {
		rw := RecResizeWidth(crop, RecMaxWidth)
		resizedWidths[i] = rw
		if rw > maxW {
			maxW = rw
		}
		resizedImages[i] = ResizeImage(crop, rw, RecHeight)
	}

	// Step 2: Build stacked tensor [B, 3, RecHeight, maxW] reusing buf.
	tensor := growFloat32(buf, B*3*RecHeight*maxW)
	stride := 3 * RecHeight * maxW
	for b := 0; b < B; b++ {
		writeRecChannel(tensor[b*stride:], resizedImages[b], resizedWidths[b], maxW)
		// Release this resized image as soon as its channel is written so the
		// B intermediates don't all live alongside the tensor.
		resizedImages[b] = nil
	}

	return tensor, maxW
}

// ── Recognition Preprocessing (single, fixed width) ─────────────

// RecPreprocessFixedInto resizes a single crop to H=RecHeight, pads to a
// fixed width, and writes a CHW [3, RecHeight, width] float32 tensor
// normalized to [-1, 1] into the caller-provided dst (length must be
// 3*RecHeight*width). Used by backends that require a static input width
// (e.g. NCNN with a shape-specialized model). The crop is resized keeping
// aspect ratio (capped at width), then right-padded with zeros to the full
// width. Writing into a caller buffer lets a long-lived engine reuse one
// scratch slice across boxes.
func RecPreprocessFixedInto(img image.Image, width int, dst []float32) {
	rw := RecResizeWidth(img, width)
	resized := ResizeImage(img, rw, RecHeight)
	writeRecChannel(dst, resized, rw, width)
}

// writeRecChannel writes one crop's resized RGBA pixels into a CHW float32
// slice of length 3*RecHeight*strideW, normalizing (pixel/255-0.5)/0.5 and
// zero-padding columns [rw, strideW). Uses direct Pix access (no per-pixel
// At()/PixOffset dispatch), shared by the batch and single-fixed paths.
func writeRecChannel(dst []float32, resized *image.RGBA, rw, strideW int) {
	pix := resized.Pix
	stride := resized.Stride
	for c := 0; c < 3; c++ {
		chanOffset := c * RecHeight * strideW
		for y := 0; y < RecHeight; y++ {
			rowOffset := chanOffset + y*strideW
			srcRow := pix[y*stride:]
			for x := 0; x < rw; x++ {
				val := float64(srcRow[x*4+c]) / 255.0
				dst[rowOffset+x] = float32(val-0.5) / 0.5
			}
		}
	}
}
