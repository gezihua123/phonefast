//go:build !(darwin && arm64 && ocr_embed)

// lib_nolib.go is the catch-all for builds that do NOT embed the ONNX Runtime
// shared library: every platform when `ocr_embed` is unset, and non-darwin/arm64
// even when it is set. RuntimeLib is nil — the engine loads the system-installed
// libonnxruntime at runtime (brew install onnxruntime / apt), or returns
// ErrNotAvailable if not found.
//
// To embed for a platform: create lib_<goos>_<goarch>.go with
// //go:build <goos> && <goarch> && ocr_embed and //go:embed the library file,
// then add the platform to this file's exclusion list.
package ocr

var RuntimeLib []byte // nil — use system-installed library
