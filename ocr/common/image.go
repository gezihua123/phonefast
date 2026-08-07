package common

import (
	"image"
	"math"
)

// ── Image Utilities ──────────────────────────────────────────────

// ResizeImage resizes an image to the given width and height using
// bilinear interpolation in pure Go with direct Pix access (no per-pixel
// interface dispatch). The source is converted to a flat RGBA buffer once
// upfront, which eliminates 1.46M allocations per Recognize call that
// the old per-pixel src.At() path caused.
func ResizeImage(src image.Image, dstW, dstH int) *image.RGBA {
	if dstW <= 0 || dstH <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	// Convert source to a flat RGBA slice once — the key optimization.
	srcPix, srcStride := imageToRGBA(src, srcBounds, srcW, srcH)

	// Fast path: if dimensions are within 2%, just copy directly.
	if srcW == dstW && srcH == dstH {
		dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
		copy(dst.Pix, srcPix)
		return dst
	}
	if float64(srcW)*0.98 < float64(dstW) && float64(dstW) < float64(srcW)*1.02 &&
		float64(srcH)*0.98 < float64(dstH) && float64(dstH) < float64(srcH)*1.02 {
		return copyResizeFromPix(srcPix, srcStride, srcW, srcH, dstW, dstH)
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	dstPix := dst.Pix
	dstStride := dst.Stride

	scaleX := float64(srcW) / float64(dstW)
	scaleY := float64(srcH) / float64(dstH)

	// Pre-computed weights use float32 for speed; precision loss is <0.5 LSB.
	for dy := 0; dy < dstH; dy++ {
		srcY := float64(dy)*scaleY + 0.5*scaleY - 0.5
		sy0 := int(math.Floor(srcY))
		sy1 := sy0 + 1
		if sy0 < 0 {
			sy0 = 0
		}
		if sy1 >= srcH {
			sy1 = srcH - 1
		}
		fy := float32(srcY - float64(sy0))
		fy1 := 1 - fy

		row0 := srcPix[sy0*srcStride:]
		row1 := srcPix[sy1*srcStride:]
		dstRow := dstPix[dy*dstStride:]

		for dx := 0; dx < dstW; dx++ {
			srcX := float64(dx)*scaleX + 0.5*scaleX - 0.5
			sx0 := int(math.Floor(srcX))
			sx1 := sx0 + 1
			if sx0 < 0 {
				sx0 = 0
			}
			if sx1 >= srcW {
				sx1 = srcW - 1
			}
			fx := float32(srcX - float64(sx0))
			fx1 := 1 - fx

			// Pre-compute per-channel weights.
			w00 := fy1 * fx1
			w10 := fy1 * fx
			w01 := fy * fx1
			w11 := fy * fx

			si00 := sx0 * 4
			si10 := sx1 * 4
			di := dx * 4

			dstRow[di+0] = uint8(float32(row0[si00+0])*w00 + float32(row0[si10+0])*w10 + float32(row1[si00+0])*w01 + float32(row1[si10+0])*w11 + 0.5)
			dstRow[di+1] = uint8(float32(row0[si00+1])*w00 + float32(row0[si10+1])*w10 + float32(row1[si00+1])*w01 + float32(row1[si10+1])*w11 + 0.5)
			dstRow[di+2] = uint8(float32(row0[si00+2])*w00 + float32(row0[si10+2])*w10 + float32(row1[si00+2])*w01 + float32(row1[si10+2])*w11 + 0.5)
			dstRow[di+3] = 255
		}
	}
	return dst
}

// imageToRGBA converts any image.Image to a flat RGBA pixel buffer + stride,
// reusing the source Pix when already *image.RGBA (zero-copy), or converting
// once when it's another type. This is the key optimization: do the type-switch
// once per image instead of calling src.At() 4× per pixel.
// YCbCr fast-path added for JPEG screenshots — image.Decode of JPEG returns
// *image.YCbCr with 4:2:0 subsampling, not *image.RGBA.
func imageToRGBA(src image.Image, bounds image.Rectangle, w, h int) ([]uint8, int) {
	switch s := src.(type) {
	case *image.RGBA:
		return s.Pix, s.Stride
	case *image.NRGBA:
		// Inline convert: NRGBA has non-premultiplied alpha; for our
		// screenshots we treat alpha as opaque and just copy RGB.
		dst := make([]uint8, w*h*4)
		for y := 0; y < h; y++ {
			srcRow := s.Pix[y*s.Stride:]
			dstRow := dst[y*w*4:]
			for x := 0; x < w; x++ {
				si := x * 4
				di := x * 4
				dstRow[di] = srcRow[si]
				dstRow[di+1] = srcRow[si+1]
				dstRow[di+2] = srcRow[si+2]
				dstRow[di+3] = 255
			}
		}
		return dst, w * 4
	case *image.YCbCr:
		// JPEG decode returns YCbCr. Fast inline YCbCr→RGB using direct
		// plane access, avoiding the per-pixel At().RGBA() overhead.
		// Uses BT.601 conversion with clamped integer arithmetic.
		dst := make([]uint8, w*h*4)
		cStride := s.CStride
		for y := 0; y < h; y++ {
			yy := s.Y[y*s.YStride:]
			ci := s.Cb[(y/2)*cStride:]
			cj := s.Cr[(y/2)*cStride:]
			dstRow := dst[y*w*4:]
			for x := 0; x < w; x++ {
				yyv := int32(yy[x])
				cb := int32(ci[x/2]) - 128
				cr := int32(cj[x/2]) - 128
				// BT.601 full-range YCbCr -> RGB, clamped
				r := yyv + ((1436 * cr) >> 10)
				g := yyv - ((352*cb + 731*cr) >> 10)
				b := yyv + ((1814 * cb) >> 10)
				if r < 0 {
					r = 0
				}
				if r > 255 {
					r = 255
				}
				if g < 0 {
					g = 0
				}
				if g > 255 {
					g = 255
				}
				if b < 0 {
					b = 0
				}
				if b > 255 {
					b = 255
				}
				di := x * 4
				dstRow[di] = uint8(r)
				dstRow[di+1] = uint8(g)
				dstRow[di+2] = uint8(b)
				dstRow[di+3] = 255
			}
		}
		return dst, w * 4
	default:
		dst := make([]uint8, w*h*4)
		stride := w * 4
		for y := 0; y < h; y++ {
			dstRow := dst[y*stride:]
			for x := 0; x < w; x++ {
				r, g, b, _ := src.At(x+bounds.Min.X, y+bounds.Min.Y).RGBA()
				di := x * 4
				dstRow[di] = uint8(r >> 8)
				dstRow[di+1] = uint8(g >> 8)
				dstRow[di+2] = uint8(b >> 8)
				dstRow[di+3] = 255
			}
		}
		return dst, stride
	}
}

// copyResizeFromPix is the direct-Pix version of copyResize, avoiding the
// per-pixel interface dispatch in the old code path.
func copyResizeFromPix(srcPix []uint8, srcStride int, srcW, srcH, dstW, dstH int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	dstPix := dst.Pix
	dstStride := dst.Stride

	for dy := 0; dy < dstH; dy++ {
		sy := dy * srcH / dstH
		srcRow := srcPix[sy*srcStride:]
		dstRow := dstPix[dy*dstStride:]
		for dx := 0; dx < dstW; dx++ {
			sx := dx * srcW / dstW
			si := sx * 4
			di := dx * 4
			dstRow[di] = srcRow[si]
			dstRow[di+1] = srcRow[si+1]
			dstRow[di+2] = srcRow[si+2]
			dstRow[di+3] = 255
		}
	}
	return dst
}

// ── Box Crop ─────────────────────────────────────────────────────

// CropBox extracts the bounding box region from the source image.
// Supports 4-point quadrilaterals — uses the axis-aligned bounding rect.
func CropBox(src image.Image, box [4][2]float64) image.Image {
	bounds := src.Bounds()

	// Compute axis-aligned bounding box of the quadrilateral
	minX := box[0][0]
	maxX := box[0][0]
	minY := box[0][1]
	maxY := box[0][1]
	for _, p := range box[1:] {
		if p[0] < minX {
			minX = p[0]
		}
		if p[0] > maxX {
			maxX = p[0]
		}
		if p[1] < minY {
			minY = p[1]
		}
		if p[1] > maxY {
			maxY = p[1]
		}
	}

	// Clamp to image bounds
	ix0 := int(math.Floor(minX))
	iy0 := int(math.Floor(minY))
	ix1 := int(math.Ceil(maxX))
	iy1 := int(math.Ceil(maxY))

	if ix0 < 0 {
		ix0 = 0
	}
	if iy0 < 0 {
		iy0 = 0
	}
	if ix1 > bounds.Max.X {
		ix1 = bounds.Max.X
	}
	if iy1 > bounds.Max.Y {
		iy1 = bounds.Max.Y
	}

	if ix0 >= ix1 || iy0 >= iy1 {
		// Return 1x1 empty image if invalid
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}

	// Crop
	cropRect := image.Rect(ix0, iy0, ix1, iy1)
	switch src := src.(type) {
	case *image.RGBA:
		// SubImage keeps the full image alive until both are GC'd.
		// Copy cropped pixels immediately so the full image can be freed early
		// (BaseEngine sets img=nil after cropping). For a 1080×2400 screenshot
		// (~10 MB), this saves ~10 MB peak by letting the decoder's RGBA die
		// before detection/recognition allocate their tensors.
		sub := src.SubImage(cropRect).(*image.RGBA)
		dst := image.NewRGBA(image.Rect(0, 0, sub.Bounds().Dx(), sub.Bounds().Dy()))
		copy(dst.Pix, sub.Pix)
		return dst
	case *image.YCbCr:
		// Convert YCbCr crop to RGBA inline using BT.601 — avoids
		// per-pixel At().RGBA() overhead on SubImage'd region.
		sub := src.SubImage(cropRect).(*image.YCbCr)
		subBounds := sub.Bounds()
		sw, sh := subBounds.Dx(), subBounds.Dy()
		dst := image.NewRGBA(image.Rect(0, 0, sw, sh))
		for y := 0; y < sh; y++ {
			srcY := sub.Y[sub.YOffset(subBounds.Min.X, subBounds.Min.Y+y):]
			ci := sub.Cb[sub.COffset(subBounds.Min.X, subBounds.Min.Y+y):]
			cj := sub.Cr[sub.COffset(subBounds.Min.X, subBounds.Min.Y+y):]
			dstRow := dst.Pix[y*dst.Stride:]
			for x := 0; x < sw; x++ {
				yy := int32(srcY[x])
				cb := int32(ci[x/2]) - 128
				cr := int32(cj[x/2]) - 128
				r := yy + ((1436 * cr) >> 10)
				g := yy - ((352*cb + 731*cr) >> 10)
				b := yy + ((1814 * cb) >> 10)
				if r < 0 {
					r = 0
				}
				if r > 255 {
					r = 255
				}
				if g < 0 {
					g = 0
				}
				if g > 255 {
					g = 255
				}
				if b < 0 {
					b = 0
				}
				if b > 255 {
					b = 255
				}
				di := x * 4
				dstRow[di] = uint8(r)
				dstRow[di+1] = uint8(g)
				dstRow[di+2] = uint8(b)
				dstRow[di+3] = 255
			}
		}
		return dst
	case *image.NRGBA:
		sub := src.SubImage(cropRect).(*image.NRGBA)
		dst := image.NewRGBA(image.Rect(0, 0, sub.Bounds().Dx(), sub.Bounds().Dy()))
		for y := 0; y < sub.Bounds().Dy(); y++ {
			srcRow := sub.Pix[y*sub.Stride:]
			dstRow := dst.Pix[y*dst.Stride:]
			for x := 0; x < sub.Bounds().Dx(); x++ {
				si := x * 4
				di := x * 4
				dstRow[di] = srcRow[si]
				dstRow[di+1] = srcRow[si+1]
				dstRow[di+2] = srcRow[si+2]
				dstRow[di+3] = 255
			}
		}
		return dst
	default:
		// Convert to RGBA for generic image types
		dst := image.NewRGBA(image.Rect(0, 0, ix1-ix0, iy1-iy0))
		for y := iy0; y < iy1; y++ {
			for x := ix0; x < ix1; x++ {
				r, g, b, _ := src.At(x, y).RGBA()
				dst.Pix[(y-iy0)*dst.Stride+(x-ix0)*4] = uint8(r >> 8)
				dst.Pix[(y-iy0)*dst.Stride+(x-ix0)*4+1] = uint8(g >> 8)
				dst.Pix[(y-iy0)*dst.Stride+(x-ix0)*4+2] = uint8(b >> 8)
				dst.Pix[(y-iy0)*dst.Stride+(x-ix0)*4+3] = 255
			}
		}
		return dst
	}
}
