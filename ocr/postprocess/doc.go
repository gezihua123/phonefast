// Package postprocess provides OCR detection-postprocessing utilities ported
// faithfully from PaddleOCR's DB (Differentiable Binarization) pipeline. These
// close the quality gap to PaddleOCR when both use the same PP-OCRv6 medium
// model weights — the gap is purely in pre/postprocessing, not the model.
//
// Constants and flow are read from the model's own config, NOT hardcoded from
// the DBPostProcess class defaults. Source:
//
//	~/.paddlex/official_models/PP-OCRv6_medium_det/inference.yml
//	    PostProcess: { thresh: 0.2, box_thresh: 0.45,
//	                    unclip_ratio: 1.4, max_candidates: 3000 }
//
// (overrides the class defaults 0.3/0.7/2.0/1000 via build_postprocess's
// `thresh or default` fallback in predictor.py:201-214).
//
//   - GetMiniBoxes: port of DBPostProcess.get_mini_boxes (processors.py:434-454)
//     — cv2.minAreaRect (rotating-calipers via convex-hull edge scan) → boxPoints
//     → x-sort → [TL,TR,BR,BL] winding; returns sside=min(w,h) for the min_size gate.
//
//   - BoxScoreFast: port of DBPostProcess.box_score_fast — mean detection
//     probability inside the (rotated) box via polygon fill. The box_thresh=0.45
//     precision gate that filters low-confidence components.
//
//   - UnclipBoxPaddle: port of DBPostProcess.unclip (processors.py:421-432) —
//     distance = area·ratio/perimeter (cv2.contourArea / cv2.arcLength), then
//     pyclipper JT_ROUND offset (output-faithful via miter offset + downstream
//     minAreaRect). ratio=1.4 from inference.yml.
//
// All three are wired into common.ExtractTextBoxes following PaddleOCR's
// boxes_from_bitmap order: threshold → connected components → get_mini_boxes
// (sside gate) → box_score_fast (box_thresh gate) → unclip → get_mini_boxes
// (sside+2 gate). No dedup/merge — PaddleOCR returns all surviving boxes.
package postprocess
