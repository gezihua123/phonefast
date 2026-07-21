package common

import "image"

// BoxText is one recognized box: the text + its confidence. The box itself is
// tracked by the caller (BaseEngine zips boxes[ i ] with BoxText[ i ]), so the
// recognizer only returns text+conf — keeping the Recognizer interface free of
// box geometry and letting recognizers focus purely on the rec step.
type BoxText struct {
	Text       string
	Confidence float32
}

// Recognizer is the engine-specific recognition step. Given the cropped text
// regions (one per detected box, in order), it returns one BoxText per crop.
//
// Implementations differ in how they run rec inference:
//   - onnx: batches all crops into one ORT rec session call
//   - ncnn: runs rec one box at a time via purego-dlopen'd libncnn
//
// The interface is intentionally minimal (crops in, texts out) so the shared
// BaseEngine owns the full Recognize flow (decode → detect → crop → recognize
// → filter) and engines only supply the rec-specific part.
type Recognizer interface {
	// RecognizeBoxes recognizes all crops. The returned slice must be the same
	// length as crops (one BoxText per crop, in order); empty Text is allowed
	// (BaseEngine filters empties). An error aborts the whole Recognize.
	RecognizeBoxes(crops []image.Image) ([]BoxText, error)

	// Close releases rec-specific resources (rec sessions, ncnn net, etc.).
	Close() error
}
