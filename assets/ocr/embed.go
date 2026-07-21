// Package ocr provides embedded OCR model files and platform-specific
// ONNX Runtime shared libraries for single-binary distribution.
//
// Model files (ppocr-det.onnx, ppocr-rec.onnx) are platform-independent
// and always embedded. The ONNX Runtime shared library is platform-specific
// and embedded via build-tag-gated files (lib_*.go).
//
// Build artifacts: the .onnx and library files are downloaded by
// scripts/build.sh and are .gitignore'd. When the files are empty
// (development build without download), the OCR implementation checks
// len(DetModel)==0 and returns ocr.ErrNotAvailable.
package ocr

import _ "embed"

// DetModel holds the PP-OCR text detection model (DB-based).
// ~2.4MB when populated by the build script.
// Empty in development builds without model download.
//
//go:embed ppocr-det.onnx
var DetModel []byte

// RecModel holds the PP-OCR text recognition model (CRNN + CTC).
// ~10MB when populated by the build script.
// Empty in development builds without model download.
//
//go:embed ppocr-rec.onnx
var RecModel []byte

// RuntimeLib is declared in build-tag-gated files (lib_*.go) per platform.
// When no platform file matches, lib_nolib.go provides a nil default.
