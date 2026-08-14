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

import (
	"math"
	"sort"

	"unsafe"
)

// VisionDetectAvailable reports whether macOS Vision text detection is
// available. Always true on macOS with CGO (frameworks are always present).
func VisionDetectAvailable() bool {
	return true
}

// VisionDetect runs VNDetectTextRectanglesRequest on image bytes (ANE, <1ms).
// Returns pixel-coordinate bounding boxes after merging character-level
// boxes into word/line boxes. nil if Vision fails or finds nothing.
func VisionDetect(imgData []byte, imgW, imgH int) [][4][2]float64 {
	if len(imgData) == 0 {
		return nil
	}

	var count C.int
	p := C.detectTextRegions(
		(*C.uint8_t)(unsafe.Pointer(&imgData[0])),
		C.size_t(len(imgData)),
		&count,
	)
	if p == nil || count == 0 {
		return nil
	}
	defer C.freeTextRegions(p)

	// Convert C array to Go slice
	n := int(count)
	regions := unsafe.Slice((*C.TextRegion)(unsafe.Pointer(p)), n)

	raw := make([][4][2]float64, 0, n)
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

		// Accept character-level boxes (≥0.5px)
		if x2-x1 < 0.5 || y2-y1 < 0.5 {
			continue
		}

		raw = append(raw, [4][2]float64{
			{x1, y1}, // top-left
			{x2, y1}, // top-right
			{x2, y2}, // bottom-right
			{x1, y2}, // bottom-left
		})
	}

	if len(raw) == 0 {
		return nil
	}
	return mergeCharBoxes(raw)
}

// mergeCharBoxes merges nearby character-level boxes from Vision into
// word/line boxes. Boxes on the same row (Y-center within 1.5× char height)
// and close horizontally (gap < 2× char height) are merged.
func mergeCharBoxes(boxes [][4][2]float64) [][4][2]float64 {
	if len(boxes) <= 1 {
		return boxes
	}

	sort.Slice(boxes, func(i, j int) bool {
		yi := (boxes[i][0][1] + boxes[i][2][1]) / 2
		yj := (boxes[j][0][1] + boxes[j][2][1]) / 2
		if math.Abs(yi-yj) < 5 {
			return (boxes[i][0][0]+boxes[i][1][0])/2 < (boxes[j][0][0]+boxes[j][1][0])/2
		}
		return yi < yj
	})

	var merged [][4][2]float64
	i := 0
	for i < len(boxes) {
		box := boxes[i]
		h := (box[2][1] - box[0][1])
		if h < 1 {
			h = 1
		}
		minX, maxX := box[0][0], box[1][0]
		minY, maxY := box[0][1], box[2][1]
		rowY := (minY + maxY) / 2

		j := i + 1
		for j < len(boxes) {
			next := boxes[j]
			nh := next[2][1] - next[0][1]
			if nh < 1 {
				nh = 1
			}
			ny := (next[0][1] + next[2][1]) / 2
			// Same row?
			if math.Abs(ny-rowY) > h*1.5 {
				break
			}
			// Close enough?
			if next[0][0]-maxX > h*2.0 {
				break
			}
			if next[0][0] < minX {
				minX = next[0][0]
			}
			if next[1][0] > maxX {
				maxX = next[1][0]
			}
			if next[0][1] < minY {
				minY = next[0][1]
			}
			if next[2][1] > maxY {
				maxY = next[2][1]
			}
			j++
		}
		merged = append(merged, [4][2]float64{
			{minX, minY}, {maxX, minY}, {maxX, maxY}, {minX, maxY},
		})
		i = j
	}
	return merged
}
