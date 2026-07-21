//go:build linux && amd64 && ocr_embed

package ocr

import _ "embed"

// RuntimeLib holds the ONNX Runtime shared library for linux/amd64, embedded
// when the `ocr_embed` build tag is set (the "-full" self-contained build).
// Populated by download-ocr-models.sh --lib --target linux/amd64.
//
// Without `ocr_embed`, RuntimeLib is nil (lib_nolib.go) and the engine loads
// the system-installed libonnxruntime at runtime (apt install onnxruntime) —
// the smaller, non-self-contained build.
//
//go:embed libonnxruntime-linux-amd64.so
var RuntimeLib []byte
