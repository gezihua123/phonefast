package ocr

import "errors"

// TextResult is one recognized text region with its bounding box.
//
// Box is a quadrilateral (4 corner points) in image coordinate space,
// matching the output of PP-OCR / DB-based text detection. The points
// are ordered: top-left, top-right, bottom-right, bottom-left.
type TextResult struct {
	Text       string
	Box        [4][2]float64
	Confidence float32
}

// Center returns the tap center point of the bounding box, computed
// as the average of the 4 corner points. Suitable for phonefast's
// coordinate-based tap operations.
func (t TextResult) Center() (x, y float64) {
	for _, p := range t.Box {
		x += p[0]
		y += p[1]
	}
	return x / 4, y / 4
}

// Engine recognizes text in an image, returning text regions with
// their positions (bounding boxes).
//
// A single Engine instance is safe for use by one goroutine (the
// session's caller) but is not safe for concurrent use. The session
// actor model serializes access; implementations need no internal mutex.
type Engine interface {
	// Recognize performs OCR on PNG image bytes and returns recognized
	// text regions with bounding boxes in image coordinate space.
	//
	// Returns ErrNotAvailable if the engine was not initialized (models
	// or runtime library missing).
	Recognize(pngImage []byte) ([]TextResult, error)

	// Close releases all resources held by the engine (model sessions,
	// runtime library handle). After Close the Engine must not be used.
	Close() error
}

// Result is a single recognized text region in a JSON-RPC response.
// Shared between daemon server (ocr_rpc.go) and CLI client (main.go ocrCmd).
type Result struct {
	Text       string        `json:"text"`
	Box        [4][2]float64 `json:"box"`
	Center     [2]float64    `json:"center"`
	Confidence float32       `json:"confidence"`
}

// Response is the shared JSON wire-format payload for the "ocr" JSON-RPC method.
// Both the daemon server (ocr_rpc.go) and CLI client (main.go ocrCmd) use this
// type to avoid duplicate struct definitions drifting apart.
type Response struct {
	Items  []Result `json:"items"`
	Count  int      `json:"count"`
	Width  int      `json:"image_width"`
	Height int      `json:"image_height"`
}

// ErrNotAvailable is returned when no OCR engine is available —
// models not found, ONNX Runtime library missing, or the engine
// was not initialized. Callers should treat this as non-fatal and
// fall back to accessibility-only or LLM-vision approaches.
var ErrNotAvailable = errors.New("ocr: engine not available")

// Engine name constants for Config.Engine. Referenced by internal/ocr
// (dispatch) and cmd/phonefast (flag default/validation).
const (
	EngineONNX = "onnx" // default; pure-Go, embedded models, zero external deps
	EngineNCNN = "ncnn" // opt-in; -tags ncnn + brew libncnn, models via env
)

