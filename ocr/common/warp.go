package common

import (
	"image"
	"math"

	"github.com/gezihua123/phonefast/ocr/postprocess"
)

// WarpCropBox is the faithful Go port of PaddleOCR's CropByPolys→
// get_minarea_rect_crop→get_rotate_crop_image
// (crop_image_regions.py:133-208). It re-runs minAreaRect on the (int-truncated)
// box points to get a consistent [TL,TR,BR,BL] winding, computes the crop
// dimensions from the edge lengths, then perspective-warps the source image
// region into a w×h rectangle with bicubic interpolation and border replication
// (matching cv2.warpPerspective INTER_CUBIC + BORDER_REPLICATE). If the crop is
// taller than wide (h/w ≥ 1.5, vertical text), it is rotated 90° CCW (np.rot90).
//
// For axis-aligned boxes this degenerates to a near-exact pixel crop (the
// homography is a pure translation); for rotated/vertical text it de-rotates
// the line, which the recognition model is trained on.
func WarpCropBox(src image.Image, box [4][2]float64) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	// PaddleOCR's boxes are int16 (boxes_from_bitmap rounds to int). Truncate
	// the float box to int points before minAreaRect, matching
	// get_minarea_rect_crop's `np.array(points).astype(np.int32)`.
	intPts := [][2]float64{
		{math.Round(box[0][0]), math.Round(box[0][1])},
		{math.Round(box[1][0]), math.Round(box[1][1])},
		{math.Round(box[2][0]), math.Round(box[2][1])},
		{math.Round(box[3][0]), math.Round(box[3][1])},
	}

	// get_minarea_rect_crop: minAreaRect → boxPoints → x-sort → [TL,TR,BR,BL].
	ordered, _ := postprocess.GetMiniBoxes(intPts)
	tl, tr, br, bl := ordered[0], ordered[1], ordered[2], ordered[3]

	// get_rotate_crop_image: crop dims from the max of opposite edge lengths.
	top := dist(tl, tr)
	bottom := dist(br, bl)
	cropW := int(math.Max(top, bottom))
	left := dist(tl, bl)
	right := dist(tr, br)
	cropH := int(math.Max(left, right))
	if cropW < 1 || cropH < 1 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}

	// Homography mapping dst (standard rect) → src (the ordered box), so we can
	// sample the source for each destination pixel. This is the inverse of
	// cv2.getPerspectiveTransform(box, pts_std); computing it directly as
	// getPerspectiveTransform(pts_std, box) avoids a 3×3 inversion.
	dstStd := [4][2]float64{
		{0, 0},
		{float64(cropW), 0},
		{float64(cropW), float64(cropH)},
		{0, float64(cropH)},
	}
	srcPts := [4][2]float64{tl, tr, br, bl}
	H := getPerspectiveTransform(dstStd, srcPts) // dst→src, row-major 3×3

	// Flatten the source to RGBA once for fast sampling.
	srcPix, srcStride := imageToRGBA(src, bounds, srcW, srcH)

	dst := image.NewRGBA(image.Rect(0, 0, cropW, cropH))
	dstPix := dst.Pix
	dstStride := dst.Stride

	h00, h01, h02 := H[0], H[1], H[2]
	h10, h11, h12 := H[3], H[4], H[5]
	h20, h21 := H[6], H[7]

	for y := 0; y < cropH; y++ {
		for x := 0; x < cropW; x++ {
			// Map dst (x,y) → src (sx,sy) via H (dst→src), perspective divide.
			w := h20*float64(x) + h21*float64(y) + 1
			if w == 0 {
				w = 1e-12
			}
			sx := (h00*float64(x) + h01*float64(y) + h02) / w
			sy := (h10*float64(x) + h11*float64(y) + h12) / w

			// Sample src at (sx,sy) with bicubic + border replicate.
			r, g, b := sampleBicubicReplicate(srcPix, srcStride, srcW, srcH, sx, sy)
			di := y*dstStride + x*4
			dstPix[di] = r
			dstPix[di+1] = g
			dstPix[di+2] = b
			dstPix[di+3] = 255
		}
	}

	// Vertical-text handling: if height/width ≥ 1.5, rotate 90° CCW (np.rot90,
	// k=1). This presents vertical lines horizontally to the rec model.
	if cropH >= int(1.5*float64(cropW)) {
		dst = rot90CCW(dst)
	}
	return dst
}

func dist(a, b [2]float64) float64 {
	dx := a[0] - b[0]
	dy := a[1] - b[1]
	return math.Sqrt(dx*dx + dy*dy)
}

// rot90CCW rotates an RGBA image 90° counter-clockwise (numpy rot90, k=1).
// Input dims (h,w) → output dims (w,h). Verified against numpy:
// out[i,j] = m[j, w-1-i]  with i ∈ [0,w), j ∈ [0,h).
func rot90CCW(src *image.RGBA) *image.RGBA {
	w := src.Bounds().Dx()
	h := src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w)) // dims swap: (w_out=h, h_out=w)
	srcPix := src.Pix
	srcStride := src.Stride
	dstPix := dst.Pix
	dstStride := dst.Stride
	for i := 0; i < w; i++ { // out row i ∈ [0,w)
		for j := 0; j < h; j++ { // out col j ∈ [0,h)
			// out[i][j] = src[j][w-1-i]
			si := j*srcStride + (w-1-i)*4
			ndi := i*dstStride + j*4
			dstPix[ndi] = srcPix[si]
			dstPix[ndi+1] = srcPix[si+1]
			dstPix[ndi+2] = srcPix[si+2]
			dstPix[ndi+3] = srcPix[si+3]
		}
	}
	return dst
}

// sampleBicubicReplicate samples an RGBA pixel buffer at float coords (fx,fy)
// with bicubic interpolation (OpenCV INTER_CUBIC, a=-0.75) and BORDER_REPLICATE
// (out-of-range indices clamped to [0,w-1]/[0,h-1]). Returns R,G,B.
func sampleBicubicReplicate(pix []uint8, stride, w, h int, fx, fy float64) (uint8, uint8, uint8) {
	const a = -0.75

	// X taps.
	ix := int(math.Floor(fx))
	tx := fx - float64(ix)
	wx := [4]float64{cubicWeight(a, tx+1), cubicWeight(a, tx), cubicWeight(a, 1-tx), cubicWeight(a, 2-tx)}
	// Y taps.
	iy := int(math.Floor(fy))
	ty := fy - float64(iy)
	wy := [4]float64{cubicWeight(a, ty+1), cubicWeight(a, ty), cubicWeight(a, 1-ty), cubicWeight(a, 2-ty)}

	var r, g, b float64
	for j := 0; j < 4; j++ {
		yy := clampIdx(iy+j-1, h)
		wyj := wy[j]
		if wyj == 0 {
			continue
		}
		rowOff := yy * stride
		for i := 0; i < 4; i++ {
			wi := wx[i]
			if wi == 0 {
				continue
			}
			xx := clampIdx(ix+i-1, w)
			pi := rowOff + xx*4
			weight := wyj * wi
			r += float64(pix[pi]) * weight
			g += float64(pix[pi+1]) * weight
			b += float64(pix[pi+2]) * weight
		}
	}
	return clamp255(r), clamp255(g), clamp255(b)
}

// cubicWeight is the OpenCV INTER_CUBIC kernel (a=-0.75) evaluated at distance
// d≥0 from the sample center.
func cubicWeight(a, d float64) float64 {
	d = math.Abs(d)
	if d <= 1 {
		return (a+2)*d*d*d - (a+3)*d*d + 1
	}
	if d < 2 {
		return a*d*d*d - 5*a*d*d + 8*a*d - 4*a
	}
	return 0
}

func clampIdx(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func clamp255(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5) // round
}

// getPerspectiveTransform solves the 3×3 homography mapping src→dst from four
// point correspondences, faithful to cv2.getPerspectiveTransform. Returns the
// matrix row-major (h00..h22, with h22 normalized to 1).
//
// Each correspondence gives two equations; the 8 unknowns (h00,h01,h02,h10,h11,
// h12,h20,h21) are solved by Gaussian elimination on the 8×8 system.
func getPerspectiveTransform(src, dst [4][2]float64) [9]float64 {
	var A [8][8]float64
	var b [8]float64
	for i := 0; i < 4; i++ {
		x, y := src[i][0], src[i][1]
		X, Y := dst[i][0], dst[i][1]
		// Row for X: h00*x + h01*y + h02 - h20*x*X - h21*y*X = X
		A[2*i][0] = x
		A[2*i][1] = y
		A[2*i][2] = 1
		A[2*i][6] = -x * X
		A[2*i][7] = -y * X
		b[2*i] = X
		// Row for Y: h10*x + h11*y + h12 - h20*x*Y - h21*y*Y = Y
		A[2*i+1][3] = x
		A[2*i+1][4] = y
		A[2*i+1][5] = 1
		A[2*i+1][6] = -x * Y
		A[2*i+1][7] = -y * Y
		b[2*i+1] = Y
	}
	h := solveLinear8(A, b)
	return [9]float64{h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7], 1}
}

// solveLinear8 solves A·x = b for an 8×8 system via Gaussian elimination with
// partial pivoting.
func solveLinear8(A [8][8]float64, b [8]float64) [8]float64 {
	// Augmented matrix [A|b].
	var M [8][9]float64
	for i := 0; i < 8; i++ {
		for j := 0; j < 8; j++ {
			M[i][j] = A[i][j]
		}
		M[i][8] = b[i]
	}
	for col := 0; col < 8; col++ {
		// Pivot.
		pivot := col
		for r := col + 1; r < 8; r++ {
			if math.Abs(M[r][col]) > math.Abs(M[pivot][col]) {
				pivot = r
			}
		}
		M[col], M[pivot] = M[pivot], M[col]
		pv := M[col][col]
		if pv == 0 {
			continue // singular; leave as-is
		}
		for r := 0; r < 8; r++ {
			if r == col {
				continue
			}
			factor := M[r][col] / pv
			if factor == 0 {
				continue
			}
			for c := col; c < 9; c++ {
				M[r][c] -= factor * M[col][c]
			}
		}
	}
	var x [8]float64
	for i := 0; i < 8; i++ {
		if M[i][i] != 0 {
			x[i] = M[i][8] / M[i][i]
		}
	}
	return x
}
