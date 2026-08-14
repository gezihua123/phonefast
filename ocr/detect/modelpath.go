//go:build !windows

package detect

// modelpath.go — resolves PP-OCR model files on disk for builds that do not
// embed them. Only the -full variant embeds models (ocr/assets/embed_default.go);
// plain/cgo1/apple build with NO_OCR_MODELS (DetModel/RecModel are nil) and
// load the models from disk at runtime instead.
//
// Precedence per model (first existing file wins):
//  1. PHONEFAST_OCR_DET_MODEL / PHONEFAST_OCR_REC_MODEL env vars (explicit,
//     same pattern as PHONEFAST_NCNN_PARAM/BIN)
//  2. ~/.phonefast/models/ppocr-{det,rec}.onnx (default install dir)
//  3. ./ocr/assets/ppocr-{det,rec}.onnx (dev convenience: the repo dir that
//     `bash ocr/scripts/download.sh models` populates)
//
// A set-but-missing env var falls through to the defaults (matching the
// PHONEFAST_NCNN_LIB candidate-list pattern) rather than hard-failing.

import (
	"os"
	"path/filepath"
)

const (
	modelDetName = "ppocr-det.onnx"
	modelRecName = "ppocr-rec.onnx"
)

// ResolveDetModelPath returns the on-disk det model path, or "" if none.
func ResolveDetModelPath() string {
	return resolveModelPath("PHONEFAST_OCR_DET_MODEL", modelDetName)
}

// ResolveRecModelPath returns the on-disk rec model path, or "" if none.
func ResolveRecModelPath() string {
	return resolveModelPath("PHONEFAST_OCR_REC_MODEL", modelRecName)
}

func resolveModelPath(envVar, name string) string {
	if p := os.Getenv(envVar); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".phonefast", "models", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p := filepath.Join("ocr", "assets", name); fileExists(p) {
		return p
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ModelHint is the actionable tail of ErrNotAvailable errors when no model
// source exists: not embedded (non-full build) and not found on disk.
func ModelHint() string {
	return "download models with `bash ocr/scripts/download.sh models` " +
		"and either set PHONEFAST_OCR_DET_MODEL/PHONEFAST_OCR_REC_MODEL, install to " +
		"~/.phonefast/models/, or build the -full self-contained variant"
}
