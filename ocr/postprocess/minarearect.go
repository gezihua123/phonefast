package postprocess

import (
	"math"
	"sort"
)

// MinAreaRect computes the minimum-area bounding rectangle of a set of 2D
// points, faithfully porting cv2.minAreaRect. Like OpenCV it restricts one
// side of the rectangle to be collinear with an edge of the convex hull and
// picks the orientation of minimum area (rotating-calipers idea, evaluated
// edge-by-edge; for the small hulls text contours produce this O(h²) scan is
// negligible and far simpler to verify than full rotating calipers).
//
// Returns the 4 corner points (unordered set — callers that need a winding
// order use GetMiniBoxes) and the rectangle's (width, height). For degenerate
// input (< 2 distinct points, or all collinear) it falls back to the
// axis-aligned bounding box, matching cv2's degenerate handling.
func MinAreaRect(pts [][2]float64) (corners [4][2]float64, w, h float64) {
	if len(pts) == 0 {
		return [4][2]float64{}, 0, 0
	}
	if len(pts) == 1 {
		c := pts[0]
		return [4][2]float64{c, c, c, c}, 0, 0
	}

	hull := convexHull(pts)
	if len(hull) == 1 {
		c := hull[0]
		return [4][2]float64{c, c, c, c}, 0, 0
	}
	if len(hull) == 2 {
		// Collinear / 2-point hull: cv2 returns a zero-area rect. The
		// axis-aligned bbox of the two points is a faithful degenerate stand-in
		// (sside=0 → the downstream min_size gate drops it, as cv2 would).
		return axisAlignedBox(pts)
	}

	bestArea := math.Inf(1)
	var best [4][2]float64
	bestW, bestH := 0.0, 0.0
	n := len(hull)

	for i := 0; i < n; i++ {
		p1 := hull[i]
		p2 := hull[(i+1)%n]
		ex := p2[0] - p1[0]
		ey := p2[1] - p1[1]
		elen := math.Hypot(ex, ey)
		if elen == 0 {
			continue
		}
		ux := ex / elen
		uy := ey / elen
		vx := -uy
		vy := ux

		minU := math.Inf(1)
		maxU := math.Inf(-1)
		minV := math.Inf(1)
		maxV := math.Inf(-1)
		for _, q := range hull {
			dx := q[0] - p1[0]
			dy := q[1] - p1[1]
			pu := dx*ux + dy*uy
			pv := dx*vx + dy*vy
			if pu < minU {
				minU = pu
			}
			if pu > maxU {
				maxU = pu
			}
			if pv < minV {
				minV = pv
			}
			if pv > maxV {
				maxV = pv
			}
		}
		ww := maxU - minU
		hh := maxV - minV
		area := ww * hh
		if area < bestArea {
			bestArea = area
			bestW, bestH = ww, hh
			// Corners in (u,v) frame: (minU,minV),(maxU,minV),(maxU,maxV),(minU,maxV).
			// Map back to (x,y): point = p1 + pu*u + pv*v.
			best = [4][2]float64{
				{p1[0] + minU*ux + minV*vx, p1[1] + minU*uy + minV*vy},
				{p1[0] + maxU*ux + minV*vx, p1[1] + maxU*uy + minV*vy},
				{p1[0] + maxU*ux + maxV*vx, p1[1] + maxU*uy + maxV*vy},
				{p1[0] + minU*ux + maxV*vx, p1[1] + minU*uy + maxV*vy},
			}
		}
	}

	if bestArea == math.Inf(1) {
		// All edges were zero-length (shouldn't happen post-hull). Fall back.
		return axisAlignedBox(pts)
	}
	return best, bestW, bestH
}

// GetMiniBoxes ports PaddleOCR's DBPostProcess.get_mini_boxes
// (processors.py:434-454): minAreaRect → boxPoints → sort by x → reorder into
// a consistent [top-left, top-right, bottom-right, bottom-left] winding. The
// returned sside = min(rect width, height) is the gate dimension used by
// boxes_from_bitmap's sside < min_size checks.
func GetMiniBoxes(pts [][2]float64) (box [4][2]float64, sside float64) {
	corners, w, h := MinAreaRect(pts)

	// cv2.boxPoints then sorted(..., key=lambda x: x[0]) — ascending x.
	sort.Slice(corners[:], func(i, j int) bool {
		if corners[i][0] != corners[j][0] {
			return corners[i][0] < corners[j][0]
		}
		return corners[i][1] < corners[j][1]
	})

	// processors.py:439-453 — pick the winding from the x-sorted corners.
	// points[0],points[1] = leftmost pair; points[2],points[3] = rightmost.
	index1, index2, index3, index4 := 0, 1, 2, 3
	if corners[1][1] > corners[0][1] {
		index1 = 0
		index4 = 1
	} else {
		index1 = 1
		index4 = 0
	}
	if corners[3][1] > corners[2][1] {
		index2 = 2
		index3 = 3
	} else {
		index2 = 3
		index3 = 2
	}
	box = [4][2]float64{corners[index1], corners[index2], corners[index3], corners[index4]}

	sside = w
	if h < sside {
		sside = h
	}
	return box, sside
}

// convexHull is Andrew's monotone chain. Returns hull points in CCW order,
// first point not repeated at the end. Collinear hull-edge points are kept
// (matching cv2.convexHull default, which does not remove collinear points).
func convexHull(pts [][2]float64) [][2]float64 {
	if len(pts) <= 1 {
		return pts
	}
	sorted := make([][2]float64, len(pts))
	copy(sorted, pts)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i][0] != sorted[j][0] {
			return sorted[i][0] < sorted[j][0]
		}
		return sorted[i][1] < sorted[j][1]
	})

	// Deduplicate.
	dedup := sorted[:1]
	for i := 1; i < len(sorted); i++ {
		if sorted[i] != dedup[len(dedup)-1] {
			dedup = append(dedup, sorted[i])
		}
	}
	if len(dedup) <= 1 {
		return dedup
	}

	cross := func(o, a, b [2]float64) float64 {
		return (a[0]-o[0])*(b[1]-o[1]) - (a[1]-o[1])*(b[0]-o[0])
	}

	lower := make([][2]float64, 0, len(dedup))
	for _, p := range dedup {
		for len(lower) >= 2 && cross(lower[len(lower)-2], lower[len(lower)-1], p) <= 0 {
			lower = lower[:len(lower)-1]
		}
		lower = append(lower, p)
	}
	upper := make([][2]float64, 0, len(dedup))
	for i := len(dedup) - 1; i >= 0; i-- {
		p := dedup[i]
		for len(upper) >= 2 && cross(upper[len(upper)-2], upper[len(upper)-1], p) <= 0 {
			upper = upper[:len(upper)-1]
		}
		upper = append(upper, p)
	}
	hull := append(lower[:len(lower)-1], upper[:len(upper)-1]...)
	if len(hull) == 0 {
		return dedup
	}
	return hull
}

func axisAlignedBox(pts [][2]float64) (corners [4][2]float64, w, h float64) {
	minX, minY := pts[0][0], pts[0][1]
	maxX, maxY := minX, minY
	for _, p := range pts {
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
	return [4][2]float64{
		{minX, minY}, {maxX, minY}, {maxX, maxY}, {minX, maxY},
	}, maxX - minX, maxY - minY
}
