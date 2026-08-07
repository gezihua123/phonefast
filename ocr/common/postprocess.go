package common

import (
	"math"
	"sort"
)

// DedupBoxes removes overlapping bounding boxes, keeping the larger one when
// two boxes overlap significantly (IoU > 0.5). Used to merge Vision + ONNX
// detection results.
func DedupBoxes(boxes [][4][2]float64, _ float64) [][4][2]float64 {
	if len(boxes) <= 1 {
		return boxes
	}

	// Sort by size descending (larger boxes kept when overlapping)
	sort.Slice(boxes, func(i, j int) bool {
		ai := (boxes[i][2][0] - boxes[i][0][0]) * (boxes[i][2][1] - boxes[i][0][1])
		aj := (boxes[j][2][0] - boxes[j][0][0]) * (boxes[j][2][1] - boxes[j][0][1])
		return ai > aj
	})

	kept := boxes[:0]
	for _, box := range boxes {
		dup := false
		for _, k := range kept {
			if boxIoU(box, k) > 0.5 {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, box)
		}
	}
	return kept
}

func boxIoU(a, b [4][2]float64) float64 {
	// Intersection over Union for axis-aligned bounding boxes
	ax1 := min(a[0][0], a[1][0], a[2][0], a[3][0])
	ax2 := max(a[0][0], a[1][0], a[2][0], a[3][0])
	ay1 := min(a[0][1], a[1][1], a[2][1], a[3][1])
	ay2 := max(a[0][1], a[1][1], a[2][1], a[3][1])
	bx1 := min(b[0][0], b[1][0], b[2][0], b[3][0])
	bx2 := max(b[0][0], b[1][0], b[2][0], b[3][0])
	by1 := min(b[0][1], b[1][1], b[2][1], b[3][1])
	by2 := max(b[0][1], b[1][1], b[2][1], b[3][1])

	x1 := math.Max(ax1, bx1)
	y1 := math.Max(ay1, by1)
	x2 := math.Min(ax2, bx2)
	y2 := math.Min(ay2, by2)
	intersection := math.Max(0, x2-x1) * math.Max(0, y2-y1)

	union := (ax2-ax1)*(ay2-ay1) + (bx2-bx1)*(by2-by1) - intersection
	if union == 0 {
		return 0
	}
	return intersection / union
}

// ── Postprocessing: Box Extraction ───────────────────────────────

// ExtractTextBoxes extracts text bounding boxes from the detection model's
// output probability map using DB (Differentiable Binarization) postprocessing.
//
// Boxes are returned in the model's coordinate space (mapW × mapH).
// The caller must scale to original image coordinates.
//
// Pipeline:
//  1. Threshold probability map at 0.3 → binary mask
//  2. Find 8-connected components via flood fill
//  3. Fit axis-aligned quadrilaterals
//  4. Filter tiny boxes (min side < 3px)
//  5. Sort top-to-bottom, left-to-right
func ExtractTextBoxes(probMap []float32, mapW, mapH int) [][4][2]float64 {
	npixels := mapW * mapH

	// Step 1: Threshold at 0.3 — use pooled bool buffer.
	const threshold = float32(0.3)
	binary := getBool(npixels)
	for i, v := range probMap {
		binary[i] = v > threshold
	}

	// Step 2: Dilate mask to connect nearby text fragments (replaces O(n²) merge).
	dilated := getBool(npixels)
	dilateMaskInto(binary, dilated, mapW, mapH)
	putBool(binary)
	binary = nil

	// Step 3: Find connected components via flood fill.
	visited := getBool(npixels)
	// visited starts zeroed from pool? Not guaranteed — the pool may return
	// stale data. Zero it.
	for i := range visited {
		visited[i] = false
	}

	type point struct{ x, y int }
	var boxes [][4][2]float64
	const minBoxSide = 3

	for y := 0; y < mapH; y++ {
		for x := 0; x < mapW; x++ {
			idx := y*mapW + x
			if !dilated[idx] || visited[idx] {
				continue
			}

			// Flood fill — track bounding box only (no pixel list allocation).
			count := 0
			stack := []point{{x, y}}
			visited[idx] = true
			minX, minY := x, y
			maxX, maxY := x, y

			for len(stack) > 0 {
				p := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				count++

				if p.x < minX {
					minX = p.x
				}
				if p.x > maxX {
					maxX = p.x
				}
				if p.y < minY {
					minY = p.y
				}
				if p.y > maxY {
					maxY = p.y
				}

				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						if dx == 0 && dy == 0 {
							continue
						}
						nx, ny := p.x+dx, p.y+dy
						if nx < 0 || nx >= mapW || ny < 0 || ny >= mapH {
							continue
						}
						nidx := ny*mapW + nx
						if dilated[nidx] && !visited[nidx] {
							visited[nidx] = true
							stack = append(stack, point{nx, ny})
						}
					}
				}
			}

			if count < 4 {
				continue
			}

			bw := maxX - minX + 1
			bh := maxY - minY + 1
			if bw < minBoxSide || bh < minBoxSide {
				continue
			}

			expand := 2
			qMinX := minX - expand
			qMinY := minY - expand
			qMaxX := maxX + expand
			qMaxY := maxY + expand
			if qMinX < 0 {
				qMinX = 0
			}
			if qMinY < 0 {
				qMinY = 0
			}
			if qMaxX >= mapW {
				qMaxX = mapW - 1
			}
			if qMaxY >= mapH {
				qMaxY = mapH - 1
			}
			quad := fitQuadrilateral(qMinX, qMinY, qMaxX, qMaxY)
			boxes = append(boxes, quad)
		}
	}

	putBool(dilated)
	putBool(visited)
	sortBoxes(boxes)
	return boxes
}

// dilateMaskInto applies a 3×3 morphological dilation from src into dst.
// This connects nearby text fragments so flood fill produces larger,
// more coherent boxes. dst must be pre-allocated to w*h. Both src and dst
// are typically pool buffers (reused across ExtractTextBoxes calls).
func dilateMaskInto(src []bool, dst []bool, w, h int) {
	for i := range dst[:w*h] {
		dst[i] = false
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			if src[idx] {
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						nx, ny := x+dx, y+dy
						if nx >= 0 && nx < w && ny >= 0 && ny < h {
							dst[ny*w+nx] = true
						}
					}
				}
			}
		}
	}
}

// fitQuadrilateral creates a 4-point quadrilateral from bounding box coordinates.
// Uses axis-aligned rectangle for robustness.
func fitQuadrilateral(minX, minY, maxX, maxY int) [4][2]float64 {
	// For robustness with small components, use axis-aligned rect
	// Convert to 4 corners: top-left, top-right, bottom-right, bottom-left
	return [4][2]float64{
		{float64(minX), float64(minY)}, // top-left
		{float64(maxX), float64(minY)}, // top-right
		{float64(maxX), float64(maxY)}, // bottom-right
		{float64(minX), float64(maxY)}, // bottom-left
	}
}

// sortBoxes sorts boxes top-to-bottom, then left-to-right within same row.
func sortBoxes(boxes [][4][2]float64) {
	sort.Slice(boxes, func(i, j int) bool {
		yi := (boxes[i][0][1] + boxes[i][1][1] + boxes[i][2][1] + boxes[i][3][1]) / 4
		yj := (boxes[j][0][1] + boxes[j][1][1] + boxes[j][2][1] + boxes[j][3][1]) / 4
		if math.Abs(yi-yj) < 10 {
			xi := (boxes[i][0][0] + boxes[i][1][0] + boxes[i][2][0] + boxes[i][3][0]) / 4
			xj := (boxes[j][0][0] + boxes[j][1][0] + boxes[j][2][0] + boxes[j][3][0]) / 4
			return xi < xj
		}
		return yi < yj
	})
}
