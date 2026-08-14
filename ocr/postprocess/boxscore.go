package postprocess

import "math"

// BoxScoreFast computes the mean detection-probability inside a box's bounding
// rectangle, masked by the box polygon. This is a faithful port of PaddleOCR's
// DBPostProcess.box_score_fast, used as the precision gate (box_thresh) that
// decides whether a detected component is a real text region.
//
//	PaddleOCR:  fill the polygon into a mask, then
//	            cv2.mean(bitmap[bbox_region], mask)[0]
//
// For axis-aligned boxes the polygon mask equals the whole bounding rectangle,
// so this reduces to the mean of probMap over [x0,x1)×[y0,y1). For general
// quadrilaterals we rasterize the polygon (point-in-polygon per pixel) to match
// PaddleOCR exactly.
//
// Parameters:
//   - probMap: the detection model's output probability map, row-major [mapH][mapW]
//   - mapW, mapH: probability map dimensions
//   - box: 4-point quadrilateral in map coordinates
func BoxScoreFast(probMap []float32, mapW, mapH int, box [4][2]float64) float64 {
	if len(probMap) == 0 || mapW <= 0 || mapH <= 0 {
		return 0
	}

	xmin := min4f(box[0][0], box[1][0], box[2][0], box[3][0])
	xmax := max4f(box[0][0], box[1][0], box[2][0], box[3][0])
	ymin := min4f(box[0][1], box[1][1], box[2][1], box[3][1])
	ymax := max4f(box[0][1], box[1][1], box[2][1], box[3][1])

	ix0 := int(math.Floor(xmin))
	ix1 := int(math.Ceil(xmax))
	iy0 := int(math.Floor(ymin))
	iy1 := int(math.Ceil(ymax))
	if ix0 < 0 {
		ix0 = 0
	}
	if iy0 < 0 {
		iy0 = 0
	}
	if ix1 > mapW {
		ix1 = mapW
	}
	if iy1 > mapH {
		iy1 = mapH
	}
	if ix0 >= ix1 || iy0 >= iy1 {
		return 0
	}

	// Fast path: axis-aligned rectangle (all four corners share x-pairs and
	// y-pairs). The polygon mask is the whole bounding rect → plain mean.
	if isAxisAligned(box) {
		var sum float64
		count := 0
		for y := iy0; y < iy1; y++ {
			row := y * mapW
			for x := ix0; x < ix1; x++ {
				sum += float64(probMap[row+x])
				count++
			}
		}
		if count == 0 {
			return 0
		}
		return sum / float64(count)
	}

	// General path: rasterize the polygon (scanline point-in-polygon).
	var sum float64
	count := 0
	// Precompute polygon edges for the crossing-number test.
	pts := [4][2]float64{box[0], box[1], box[2], box[3]}
	for y := iy0; y < iy1; y++ {
		yc := float64(y) + 0.5
		row := y * mapW
		for x := ix0; x < ix1; x++ {
			xc := float64(x) + 0.5
			if pointInPolygon(xc, yc, pts) {
				sum += float64(probMap[row+x])
				count++
			}
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// isAxisAligned reports whether a 4-point box is an axis-aligned rectangle
// (corners share x-coordinates in pairs and y-coordinates in pairs), in which
// case the polygon mask equals the bounding rectangle and the fast path applies.
func isAxisAligned(box [4][2]float64) bool {
	const eps = 1e-6
	xs := [4]float64{box[0][0], box[1][0], box[2][0], box[3][0]}
	ys := [4]float64{box[0][1], box[1][1], box[2][1], box[3][1]}
	// Exactly two distinct x values and two distinct y values.
	xd := distinctCount(xs)
	yd := distinctCount(ys)
	return xd <= 2 && yd <= 2 && (xd == 2 || yd == 2 || (xd == 1 && yd == 1))
}

func distinctCount(v [4]float64) int {
	n := 1
	for i := 1; i < 4; i++ {
		dup := false
		for j := 0; j < i; j++ {
			if abs64(v[i]-v[j]) < 1e-6 {
				dup = true
				break
			}
		}
		if !dup {
			n++
		}
	}
	return n
}

// pointInPolygon is the classic crossing-number (ray-cast) test for a point
// inside a 4-vertex polygon.
func pointInPolygon(x, y float64, poly [4][2]float64) bool {
	inside := false
	j := 3
	for i := 0; i < 4; i++ {
		yi := poly[i][1]
		yj := poly[j][1]
		xi := poly[i][0]
		xj := poly[j][0]
		if (yi > y) != (yj > y) {
			xCross := (xj-xi)*(y-yi)/(yj-yi) + xi
			if x < xCross {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func min4f(a, b, c, d float64) float64 { return min4(a, b, c, d) }
func max4f(a, b, c, d float64) float64 { return max4(a, b, c, d) }

// min4/max4 return the smallest/largest of four float64s. Used by min4f/max4f
// for BoxScoreFast's axis-aligned bounding-box fast path.
func min4(a, b, c, d float64) float64 {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	if d < m {
		m = d
	}
	return m
}

func max4(a, b, c, d float64) float64 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	if d > m {
		m = d
	}
	return m
}
