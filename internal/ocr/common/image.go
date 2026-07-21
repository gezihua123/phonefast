package common

import (
	"image"
	"image/color"
	"math"
)

// ── Image Utilities ──────────────────────────────────────────────

// copyResize does a fast nearest-neighbor copy for small resizes.
// Uses direct pixel buffer access (no interface dispatch per pixel).
func copyResize(src image.Image, dstW, dstH int, srcBounds image.Rectangle, srcW, srcH int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	// Convert to RGBA for direct pix access (avoid per-pixel At() interface call)
	var srcPix []uint8
	var srcStride int
	switch s := src.(type) {
	case *image.RGBA:
		srcPix = s.Pix
		srcStride = s.Stride
	case *image.NRGBA:
		srcPix = s.Pix
		srcStride = s.Stride
	default:
		// Fallback: convert to RGBA
		rgba := image.NewRGBA(srcBounds)
		for y := 0; y < srcH; y++ {
			for x := 0; x < srcW; x++ {
				rgba.Set(x, y, src.At(x+srcBounds.Min.X, y+srcBounds.Min.Y))
			}
		}
		srcPix = rgba.Pix
		srcStride = rgba.Stride
	}

	for dy := 0; dy < dstH; dy++ {
		sy := dy * srcH / dstH
		srcRow := srcPix[sy*srcStride:]
		dstRow := dst.Pix[dy*dst.Stride:]
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

// ResizeImage resizes an image to the given width and height using
// bilinear interpolation in pure Go.
func ResizeImage(src image.Image, dstW, dstH int) *image.RGBA {
	if dstW <= 0 || dstH <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	// Fast path: if dimensions are within 2%, just copy directly
	if srcW == dstW && srcH == dstH {
		// Exact match — direct copy
		if rgba, ok := src.(*image.RGBA); ok {
			dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
			copy(dst.Pix, rgba.Pix)
			return dst
		}
	}
	if float64(srcW)*0.98 < float64(dstW) && float64(dstW) < float64(srcW)*1.02 &&
		float64(srcH)*0.98 < float64(dstH) && float64(dstH) < float64(srcH)*1.02 {
		return copyResize(src, dstW, dstH, srcBounds, srcW, srcH)
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	scaleX := float64(srcW) / float64(dstW)
	scaleY := float64(srcH) / float64(dstH)

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
		fy := srcY - float64(sy0)

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
			fx := srcX - float64(sx0)

			// Bilinear interpolation per channel
			r00, g00, b00, _ := src.At(sx0+srcBounds.Min.X, sy0+srcBounds.Min.Y).RGBA()
			r10, g10, b10, _ := src.At(sx1+srcBounds.Min.X, sy0+srcBounds.Min.Y).RGBA()
			r01, g01, b01, _ := src.At(sx0+srcBounds.Min.X, sy1+srcBounds.Min.Y).RGBA()
			r11, g11, b11, _ := src.At(sx1+srcBounds.Min.X, sy1+srcBounds.Min.Y).RGBA()

			// Bilinear interpolation in 16-bit space, then convert
			// to 8-bit: value / 257 (since 65535 / 257 = 255)
			rf := float64(r00)*(1-fx)*(1-fy) + float64(r10)*fx*(1-fy) +
				float64(r01)*(1-fx)*fy + float64(r11)*fx*fy
			gf := float64(g00)*(1-fx)*(1-fy) + float64(g10)*fx*(1-fy) +
				float64(g01)*(1-fx)*fy + float64(g11)*fx*fy
			bf := float64(b00)*(1-fx)*(1-fy) + float64(b10)*fx*(1-fy) +
				float64(b01)*(1-fx)*fy + float64(b11)*fx*fy

			idx := dst.PixOffset(dx, dy)
			dst.Pix[idx+0] = uint8(rf / 257)
			dst.Pix[idx+1] = uint8(gf / 257)
			dst.Pix[idx+2] = uint8(bf / 257)
			dst.Pix[idx+3] = 255
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
		return src.SubImage(cropRect)
	case *image.NRGBA:
		return src.SubImage(cropRect)
	case *image.YCbCr:
		return src.SubImage(cropRect)
	default:
		// Convert to RGBA for generic image types
		dst := image.NewRGBA(image.Rect(0, 0, ix1-ix0, iy1-iy0))
		for y := iy0; y < iy1; y++ {
			for x := ix0; x < ix1; x++ {
				c := color.RGBAModel.Convert(src.At(x, y)).(color.RGBA)
				dst.SetRGBA(x-ix0, y-iy0, c)
			}
		}
		return dst
	}
}
