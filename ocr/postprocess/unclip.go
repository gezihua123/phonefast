package postprocess

import "math"

// ContourArea is cv2.contourArea for a closed polygon — the absolute value of
// the shoelace sum. PaddleOCR's unclip (processors.py:423) uses it on the
// 4-point minAreaRect box to compute the offset distance.
func ContourArea(box [][2]float64) float64 {
	n := len(box)
	if n < 3 {
		return 0
	}
	var s float64
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		s += box[i][0]*box[j][1] - box[j][0]*box[i][1]
	}
	return math.Abs(s) / 2
}

// ArcLength is cv2.arcLength(box, True) — the perimeter of a closed polygon,
// the sum of consecutive edge lengths including the closing edge. PaddleOCR's
// unclip (processors.py:424) uses it as the length in distance = area·ratio/length.
func ArcLength(box [][2]float64) float64 {
	n := len(box)
	if n < 2 {
		return 0
	}
	var s float64
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		dx := box[j][0] - box[i][0]
		dy := box[j][1] - box[i][1]
		s += math.Hypot(dx, dy)
	}
	return s
}

// OffsetConvexPolygon expands a convex polygon outward by `distance` along
// each edge's outward normal (miter join at vertices). This is output-faithful
// to pyclipper's JT_ROUND offset for PaddleOCR's use: unclip feeds the offset
// polygon straight into get_mini_boxes (minAreaRect), and the round corners
// JT_ROUND produces lie strictly inside the miter corners, so both joins yield
// the same min-area bounding rectangle. (PaddleOCR calls
// pyclipper.PyclipperOffset().AddPath(box, JT_ROUND, ET_CLOSEDPOLYGON).Execute(distance)
// at processors.py:426-429.)
//
// The outward normal is chosen as the one pointing away from the polygon
// centroid, so the orientation (CW vs CCW) of the input does not matter — the
// polygon always expands, matching pyclipper's positive-distance expansion.
func OffsetConvexPolygon(pts [][2]float64, distance float64) [][2]float64 {
	n := len(pts)
	if n < 3 || distance == 0 {
		out := make([][2]float64, len(pts))
		copy(out, pts)
		return out
	}

	// Centroid (mean of vertices — sufficient for picking the outward normal
	// on a convex polygon, which is all PaddleOCR's minAreaRect quads are).
	var cx, cy float64
	for _, p := range pts {
		cx += p[0]
		cy += p[1]
	}
	cx /= float64(n)
	cy /= float64(n)

	// Shift each edge outward by `distance` along its outward normal.
	shifted := make([]offsetLine, n)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		ex := pts[j][0] - pts[i][0]
		ey := pts[j][1] - pts[i][1]
		el := math.Hypot(ex, ey)
		if el == 0 {
			shifted[i] = offsetLine{pts[i][0], pts[i][1], 1, 0}
			continue
		}
		ux := ex / el
		uy := ey / el
		// Two candidate normals: (uy, -ux) and (-uy, ux). Pick the one whose
		// dot with (edge midpoint - centroid) is positive → points outward.
		mx := (pts[i][0] + pts[j][0]) / 2
		my := (pts[i][1] + pts[j][1]) / 2
		nx := uy
		ny := -ux
		if nx*(mx-cx)+ny*(my-cy) < 0 {
			nx = -nx
			ny = -ny
		}
		shifted[i] = offsetLine{pts[i][0] + nx*distance, pts[i][1] + ny*distance, ux, uy}
	}

	// Intersect consecutive shifted edges → offset polygon vertices.
	out := make([][2]float64, n)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		out[i] = intersectLines(shifted[i], shifted[j])
	}
	return out
}

// intersectLines returns the intersection of two lines, each given as a point
// and a unit direction. Parallel lines (degenerate offset) fall back to the
// shared endpoint.
// offsetLine is a shifted edge: a point on it plus its unit direction. Used
// internally by OffsetConvexPolygon and intersectLines.
type offsetLine struct {
	px, py float64 // a point on the shifted edge
	dx, dy float64 // shifted edge direction (unit)
}

func intersectLines(a, b offsetLine) [2]float64 {
	denom := a.dx*b.dy - a.dy*b.dx
	if math.Abs(denom) < 1e-12 {
		// Parallel — return a's origin (degenerate; downstream minAreaRect
		// handles collinear input).
		return [2]float64{a.px, a.py}
	}
	t := ((b.px-a.px)*b.dy - (b.py-a.py)*b.dx) / denom
	return [2]float64{a.px + a.dx*t, a.py + a.dy*t}
}

// UnclipBoxPaddle is the faithful port of PaddleOCR's DBPostProcess.unclip
// (processors.py:421-432): distance = area·ratio/length (area=cv2.contourArea,
// length=cv2.arcLength), then pyclipper JT_ROUND offset. Returns the expanded
// polygon (a 4-point quad for the minAreaRect inputs PaddleOCR feeds it).
func UnclipBoxPaddle(box [4][2]float64, ratio float64) [][2]float64 {
	pts := [][2]float64{box[0], box[1], box[2], box[3]}
	area := ContourArea(pts)
	length := ArcLength(pts)
	if length == 0 {
		return pts
	}
	distance := area * ratio / length
	return OffsetConvexPolygon(pts, distance)
}
