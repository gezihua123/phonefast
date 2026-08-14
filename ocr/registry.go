package ocr

import (
	"fmt"
)

// Engine constructors registered by backends via init(). This follows
// the database/sql driver pattern: backends (onnx, ncnn, tesseract) call
// Register() in their init() to add themselves to the registry. The main
// package blank-imports each backend to trigger registration.
//
// This inversion breaks the import cycle: ocr no longer imports onnx/ncnn
// directly; instead, onnx/ncnn import ocr (for BaseEngine + Register). The
// registry is then queried at runtime by name.
var engineRegistry = make(map[string]func(bool) (Engine, error))

// Register adds an engine constructor to the global registry. Called from
// backend init() functions (e.g. onnx.init(), ncnn.init()). Must be called
// before any NewEngine invocation — typically satisfied by Go's init()
// ordering: main imports backends, init runs, then daemon starts.
func RegisterEngine(name string, fn func(bool) (Engine, error)) {
	engineRegistry[name] = fn
}

// NewEngine constructs the configured OCR engine by querying the registry.
// Name is the engine selector ("onnx", "ncnn", "tesseract"); empty defaults
// to "onnx". useVision enables the macOS Vision detection fast-path.
func NewEngine(cfg Config) (Engine, error) {
	name := cfg.Engine
	if name == "" {
		name = EngineONNX
	}
	fn, ok := engineRegistry[name]
	if !ok {
		return nil, fmt.Errorf("%w: unknown OCR engine %q (want %q or %q)",
			ErrNotAvailable, name, EngineONNX, EngineNCNN)
	}
	return fn(cfg.UseVision)
}
