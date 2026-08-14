package common

import (
	"math"
	"sort"

	"github.com/gezihua123/phonefast/ocr/postprocess"
)

// ── Postprocessing: Box Extraction ───────────────────────────────

// ExtractTextBoxes extracts text bounding boxes from the detection model's
// output probability map using DB (Differentiable Binarization) postprocessing,
// faithfully ported from PaddleOCR's DBPostProcess.boxes_from_bitmap
// (processors.py:366-419). Constants come from the model's inference.yml, not
// the DBPostProcess class defaults — see the const block below for the source.
//
// Boxes are returned in the model's coordinate space (mapW × mapH).
// The caller must scale to original image coordinates.
//
// Pipeline (PP-OCRv6_medium_det inference.yml: thresh=0.2, box_thresh=0.45,
// unclip_ratio=1.4, max_candidates=3000):
//  1. Threshold probability map at 0.2 → binary mask (use_dilation=False)
//  2. Find 8-connected components via flood fill (≈ cv2.findContours)
//  3. get_mini_boxes: minAreaRect quad + sside; drop if sside < min_size(3)
//  4. box_score_fast gate: drop if mean prob inside the quad < box_thresh(0.45)
//  5. unclip: expand the quad by distance = area·ratio/perimeter (ratio 1.4)
//  6. get_mini_boxes again on the expanded polygon; drop if sside < min_size+2(5)
//  7. Sort top-to-bottom, left-to-right (PaddleOCR returns all survivors, no dedup)
func ExtractTextBoxes(probMap []float32, mapW, mapH int) [][4][2]float64 {
	npixels := mapW * mapH

	// PP-OCRv6_medium_det postprocess constants — read directly from the
	// model's inference.yml (PostProcess section), NOT the DBPostProcess
	// class defaults. Source:
	//   ~/.paddlex/official_models/PP-OCRv6_medium_det/inference.yml
	//       PostProcess: { thresh: 0.2, box_thresh: 0.45,
	//                       unclip_ratio: 1.4, max_candidates: 3000 }
	// These override the class defaults (processors.py:284-294:
	// thresh=0.3/box_thresh=0.7/unclip_ratio=2.0/max_candidates=1000) because
	// build_postprocess (predictor.py:201-214) passes `thresh=self.thresh or
	// default_thresh` and the pipeline supplies no override, so the YAML wins.
	// min_size=3 is the DBPostProcess constant (processors.py:300), with the
	// two gates sside<min_size (first get_mini_boxes) and sside<min_size+2
	// (second, after unclip) at processors.py:397 and :409.
	const (
		threshold     = float32(0.2) // thresh:    pred > 0.2 → text pixel
		boxThresh     = 0.45         // box_thresh: mean-prob gate
		unclipRatio   = 1.4          // unclip_ratio: box expansion factor
		maxCandidates = 3000         // max contours scanned
		minSize       = 3            // min_size: sside gate base
	)

	// Step 1: Threshold at 0.2 (inference.yml) — use pooled bool buffer.
	binary := getBool(npixels)
	for i, v := range probMap {
		binary[i] = v > threshold
	}

	// Step 2: connected components. PaddleOCR's DBPostProcess defaults to
	// use_dilation=False — it findContours directly on `pred > thresh`. We match
	// that: skip dilation. Each 8-connected component of the thresholded prob
	// map is one contour, which we feed to get_mini_boxes.
	mask := binary

	// Step 3: Find connected components via flood fill. Each 8-connected
	// component of the thresholded prob map is one contour (cv2.findContours
	// with RETR_LIST). We collect the component's foreground pixels to feed
	// GetMiniBoxes — faithful because cv2.minAreaRect on the contour equals
	// minAreaRect on all pixels (interior points never change the convex hull,
	// and CHAIN_APPROX_SIMPLE only drops collinear boundary points).
	visited := getBool(npixels)
	// visited may hold stale data from the pool — zero it.
	for i := range visited {
		visited[i] = false
	}

	type point struct{ x, y int }
	var boxes [][4][2]float64
	var compPix [][2]float64 // reused across components (reset per component)
	var stack []point        // reused across components
	contourCount := 0

	for y := 0; y < mapH; y++ {
		for x := 0; x < mapW; x++ {
			idx := y*mapW + x
			if !mask[idx] || visited[idx] {
				continue
			}

			// PaddleOCR caps scanned contours at max_candidates
			// (boxes_from_bitmap: num_contours = min(len(contours), max_candidates)).
			if contourCount >= maxCandidates {
				break
			}
			contourCount++

			// Flood fill, collecting every foreground pixel of this component.
			compPix = compPix[:0]
			stack = stack[:0]
			stack = append(stack, point{x, y})
			visited[idx] = true

			for len(stack) > 0 {
				p := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				compPix = append(compPix, [2]float64{float64(p.x), float64(p.y)})

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
						if mask[nidx] && !visited[nidx] {
							visited[nidx] = true
							stack = append(stack, point{nx, ny})
						}
					}
				}
			}

			// boxes_from_bitmap step 1 (processors.py:396-398): get_mini_boxes
			// → rotated min-area rect quad + sside; skip if sside < min_size(3).
			quad, sside := postprocess.GetMiniBoxes(compPix)
			if sside < minSize {
				continue
			}

			// boxes_from_bitmap step 2 (processors.py:400-405): box_score_fast
			// on the rotated quad; skip if box_thresh > score. The general
			// (polygon-fill) path in BoxScoreFast handles rotated quads exactly.
			score := postprocess.BoxScoreFast(probMap, mapW, mapH, quad)
			if score < boxThresh {
				continue
			}

			// boxes_from_bitmap step 3 (processors.py:407): unclip the quad —
			// pyclipper JT_ROUND offset by distance = area·ratio/perimeter.
			expanded := postprocess.UnclipBoxPaddle(quad, unclipRatio)

			// boxes_from_bitmap step 4 (processors.py:408-410): second
			// get_mini_boxes on the expanded polygon; skip if sside < min_size+2(5).
			finalBox, sside2 := postprocess.GetMiniBoxes(expanded)
			if sside2 < minSize+2 {
				continue
			}

			boxes = append(boxes, finalBox)
		}
	}

	putBool(mask)
	putBool(visited)

	// PaddleOCR's boxes_from_bitmap returns ALL surviving boxes — it does NOT
	// dedup or line-merge. (Its DB prob map is trained solid per text line, so
	// each contour is already one line; a merge/dedup pass would over-join
	// adjacent lines whose unclip-expanded heights overlap.) phonefast matches
	// that exactly: sort and return.
	sortBoxes(boxes)
	return boxes
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
