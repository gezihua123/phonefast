//go:build darwin && arm64 && ocr_embed

package ocr

import _ "embed"

// RuntimeLib holds the ONNX Runtime shared library for darwin/arm64, embedded
// when the `ocr_embed` build tag is set (the "-full" self-contained build).
// Populated by download-ocr-models.sh --lib --target darwin/arm64 --force;
// placeholder file exists for //go:embed to compile.
//
// Without `ocr_embed`, RuntimeLib is nil (lib_nolib.go) and the engine loads
// the system-installed libonnxruntime at runtime (brew install onnxruntime) —
// the smaller, non-self-contained build.
//
//go:embed libonnxruntime-darwin-arm64.dylib
var RuntimeLib []byte
