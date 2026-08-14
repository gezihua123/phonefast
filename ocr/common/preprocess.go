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
//     (PP-OCRv6 default 960 = PaddleOCR resize_long=960, limit_type="max";
//     0 = keep original)
//  2. Cap max dimension and round to 32×
//  3. Resize with bilinear (or fast copy if near 1:1)
//  4. No padding — the model accepts dynamic input shapes [1, 3, ?, ?]
//  5. Scale: pixel / 255 → [0, 1]
//  6. Normalize: (val - mean) / std
//
// maxSide=0 means keep original resolution (quality mode, up to ~173ms).
// maxSide=960 matches PaddleOCR's PP-OCRv6 training resolution. The earlier
// 1024 was out-of-distribution and caused intra-word fragmentation on live
// screens (see ocr/detect/detect.go).
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
		minSide = 32
		// PaddleOCR feeds BGR (img_mode: BGR, PP-OCRv6_medium_det/inference.yml).
		// NormalizeImage mean/std [0.485,0.456,0.406]/[0.229,0.224,0.225] are
		// applied per-plane in BGR order: plane0(B)←0.485, plane1(G)←0.456,
		// plane2(R)←0.406. Constants are named by plane index, not color — the
		// model was trained with this BGR-mean pairing (ImageNet means listed in
		// RGB order but applied to BGR channels), so inference must match exactly.
		detMean0 = 0.485 // plane0 = B
		detMean1 = 0.456 // plane1 = G
		detMean2 = 0.406 // plane2 = R
		detStd0  = 0.229
		detStd1  = 0.224
		detStd2  = 0.225
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
			// image.RGBA layout is [R,G,B,A]; PaddleOCR wants BGR planes.
			r := rowPix[px]
			g := rowPix[px+1]
			b := rowPix[px+2]
			base := y*resizeW + x
			tensor[base] = float32(float64(b)/255.0-detMean0) / detStd0
			tensor[resizeH*resizeW+base] = float32(float64(g)/255.0-detMean1) / detStd1
			tensor[2*resizeH*resizeW+base] = float32(float64(r)/255.0-detMean2) / detStd2
		}
	}
	// Release resized intermediate (~2 MB for 480×960) after tensor is built.
	resized = nil

	shape := []int64{1, 3, int64(resizeH), int64(resizeW)}
	return tensor, resizeW, resizeH, shape
}

// ── Batch Recognition Preprocessing ─────────────────────────────

// RecMaxWidth is the PP-OCR rec input width ceiling. The rec model is
// shape-specialized to this width on conversion (see scripts/convert-ncnn.sh);
// single engines pad each crop to it. Defined once here so the ncnn engine
// and the preprocess helpers share the single source of truth.
//
// 640 prevents character truncation on long merged text lines (e.g. the full
// title "Avocado Toast with Egg" ≈ 576px at H=48). At 480, the line was capped
// and compressed, dropping the trailing 'g' → "Ego". 640 leaves headroom without
// meaningfully increasing inference cost (T ∝ W, CTC is O(T·nClass); most crops
// are far shorter than the cap, so the average impact is small).
const RecMaxWidth = 640

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

// recMaxImgW is PaddleOCR's max_imgW cap on the rec input width
// (processors.py:51, OCRReisizeNormImg). The rec ONNX model accepts dynamic
// width up to this; PP-OCRv6_medium_rec's TRT dynamic shapes declare W up to
// 3200 (inference.yml). Used only by the ONNX path — the NCNN path uses the
// static RecMaxWidth=640 shape-specialized model.
const recMaxImgW = 3200

// recMinRatio is PaddleOCR's max_wh_ratio floor: rec_image_shape [3,48,320] →
// 320/48 ≈ 6.667 (processors.py:91-96, OCRReisizeNormImg.resize). A crop
// narrower than this is still padded to width 320 so the rec model sees ≥320
// timesteps — matching PaddleOCR exactly, where the old pure-aspect resize fed
// short text a sub-320 width and changed the temporal context.
const recMinRatio = 320.0 / 48.0

// recResizeDims ports PaddleOCR OCRReisizeNormImg.resize_norm_img
// (processors.py:56-83) for one crop of size w×h. It returns:
//   - resizedW: the aspect-preserved content width
//   - imgW: the per-crop padded width = int(48·max(320/48, w/h)), capped 3200
//
// Faithful detail: the `if ceil(imgH·ratio) > imgW` branch (processors.py:71-75)
// means a wide crop (ratio > 320/48) resizes to int(48·ratio) with no padding,
// while a narrow crop resizes to ceil(48·ratio) and pads up to 320.
func recResizeDims(w, h int) (resizedW, imgW int) {
	const imgH = RecHeight
	if h <= 0 || w <= 0 {
		return 1, int(float64(imgH) * recMinRatio)
	}
	ratio := float64(w) / float64(h)
	maxWhRatio := recMinRatio
	if ratio > maxWhRatio {
		maxWhRatio = ratio
	}
	imgW = int(float64(imgH) * maxWhRatio)
	if imgW > recMaxImgW {
		resizedW = recMaxImgW
		imgW = recMaxImgW
		return
	}
	if math.Ceil(float64(imgH)*ratio) > float64(imgW) {
		resizedW = imgW
	} else {
		resizedW = int(math.Ceil(float64(imgH) * ratio))
	}
	if resizedW < 1 {
		resizedW = 1
	}
	return
}

// RecBatchPreprocessChunkInto writes a [B,3,RecHeight,maxW] tensor for a chunk
// of crops, faithful to PaddleOCR's OCRReisizeNormImg + ToBatch
// (processors.py:47-105, 322-359). Each crop is resized to its content width
// (recResizeDims) and right-padded with zeros to the chunk's max imgW. The
// caller bounds B (≤8, PaddleOCR's rec batch) to keep the output [B,T,nClass]
// memory-bounded.
//
// The whole tensor is zeroed before writing so the padding region is exactly
// 0.0 — matching PaddleOCR's np.pad(constant_values=0). (growFloat32 reuses the
// backing array across calls without re-zeroing, so stale padding would
// otherwise leak into the rec input.)
//
// Safety: same zero-copy ORT contract as DetPreprocessInto — do not reuse the
// returned slice until the input Value built from it is Closed.
func RecBatchPreprocessChunkInto(crops []image.Image, buf []float32) ([]float32, int) {
	B := len(crops)
	if B == 0 {
		return nil, 0
	}
	resizedWidths := make([]int, B)
	resizedImages := make([]*image.RGBA, B)
	maxW := 0
	for i, crop := range crops {
		b := crop.Bounds()
		rw, iw := recResizeDims(b.Dx(), b.Dy())
		resizedWidths[i] = rw
		if iw > maxW {
			maxW = iw
		}
		resizedImages[i] = ResizeImage(crop, rw, RecHeight)
	}

	// Zero the (possibly reused) buffer so right-padding is exactly 0.0,
	// matching PaddleOCR's np.pad constant_values=0.
	tensor := growFloat32(buf, B*3*RecHeight*maxW)
	clear(tensor)
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
// writeRecChannel writes one crop's resized RGBA pixels into a CHW float32
// slice of length 3*RecHeight*strideW, normalizing (pixel/255-0.5)/0.5 and
// zero-padding columns [rw, strideW). Uses direct Pix access (no per-pixel
// At()/PixOffset dispatch), shared by the batch and single-fixed paths.
func writeRecChannel(dst []float32, resized *image.RGBA, rw, strideW int) {
	pix := resized.Pix
	stride := resized.Stride
	// PaddleOCR feeds BGR (img_mode: BGR, PP-OCRv6_medium_rec/inference.yml):
	// plane0=B, plane1=G, plane2=R. image.RGBA layout is [R,G,B,A], so map
	// plane index → byte offset {2,1,0}. Rec norm is symmetric (0.5/0.5), so
	// only the channel order changes.
	planeByte := [3]int{2, 1, 0}
	for c := 0; c < 3; c++ {
		chanOffset := c * RecHeight * strideW
		for y := 0; y < RecHeight; y++ {
			rowOffset := chanOffset + y*strideW
			srcRow := pix[y*stride:]
			for x := 0; x < rw; x++ {
				val := float64(srcRow[x*4+planeByte[c]]) / 255.0
				dst[rowOffset+x] = float32(val-0.5) / 0.5
			}
		}
	}
}
