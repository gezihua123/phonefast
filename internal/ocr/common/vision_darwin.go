//go:build darwin && cgo

package common

/*
#cgo LDFLAGS: -framework Foundation -framework Vision -framework CoreGraphics -framework AppKit
#include <stdlib.h>

typedef struct {
	float x, y, width, height;
} TextRegion;

TextRegion* detectTextRegions(const uint8_t *pngData, size_t pngLen, int *outCount);
void freeTextRegions(TextRegion *regions);
*/
import "C"

import "unsafe"

// VisionDetectAvailable reports whether macOS Vision text detection is
// available. Always true on macOS with CGO (frameworks are always present).
func VisionDetectAvailable() bool {
	return true
}

// VisionDetect runs VNDetectTextRectanglesRequest on PNG bytes (ANE, <1ms).
// Returns pixel-coordinate bounding boxes. nil if Vision fails or finds nothing.
func VisionDetect(pngData []byte, imgW, imgH int) [][4][2]float64 {
	if len(pngData) == 0 {
		return nil
	}

	var count C.int
	p := C.detectTextRegions(
		(*C.uint8_t)(unsafe.Pointer(&pngData[0])),
		C.size_t(len(pngData)),
		&count,
	)
	if p == nil || count == 0 {
		return nil
	}
	defer C.freeTextRegions(p)

	// Convert C array to Go slice
	n := int(count)
	regions := unsafe.Slice((*C.TextRegion)(unsafe.Pointer(p)), n)

	boxes := make([][4][2]float64, 0, n)
	for _, r := range regions {
		x1 := float64(r.x)
		y1 := float64(r.y)
		x2 := x1 + float64(r.width)
		y2 := y1 + float64(r.height)

		// Clamp to image bounds
		if x1 < 0 {
			x1 = 0
		}
		if y1 < 0 {
			y1 = 0
		}
		if x2 > float64(imgW) {
			x2 = float64(imgW)
		}
		if y2 > float64(imgH) {
			y2 = float64(imgH)
		}

		// Skip tiny regions
		if x2-x1 < 2 || y2-y1 < 2 {
			continue
		}

		boxes = append(boxes, [4][2]float64{
			{x1, y1}, // top-left
			{x2, y1}, // top-right
			{x2, y2}, // bottom-right
			{x1, y2}, // bottom-left
		})
	}

	return boxes
}
