// Package ocr defines the OCR (Optical Character Recognition) interface
// for recognizing text in images and returning text with positions.
//
// This is the base/interface layer. Implementations live in separate
// packages (e.g. internal/ocr/onnx). Callers depend only on this
// package, not on any specific implementation.
//
// The interface follows the same single-goroutine contract as
// pkg/avcodec.Decoder: the session actor model serializes access,
// so implementations need no internal mutex.
package ocr
