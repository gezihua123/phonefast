//go:build !windows

// Package onnx implements the recognition step of the OCR engine using ONNX
// Runtime via onnxruntime-purego (pure Go, no CGO). Detection is handled by
// the shared internal/ocr/detect.Detector (Vision fast-path + onnx det
// fallback); this package owns only the rec session and the batch recognition.
//
// It exposes NewEngine, which builds a detect.Detector + an OnnxRecognizer and
// returns them assembled into an ocr.BaseEngine. The onnxruntime-purego binding
// uses dlopen, which is unavailable on Windows — Windows gets a stub
// (onnx_windows.go) returning ErrNotAvailable.
package onnx

import (
	"context"
	"fmt"
	"image"
	"os"

	ocrassets "github.com/gezihua123/phonefast/assets/ocr"
	"github.com/gezihua123/phonefast/internal/ocr/common"
	"github.com/gezihua123/phonefast/internal/ocr/detect"
	"github.com/gezihua123/phonefast/internal/ocr/engine"
	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
	"github.com/shota3506/onnxruntime-purego/onnxruntime"
)

// OnnxRecognizer implements common.Recognizer using an ONNX Runtime rec session.
// It batches all crops into one rec inference call.
//
// The rec session reuses the detector's ORT Runtime/Env (onnxruntime-purego's
// Runtime is process-global — a second Runtime/Env would conflict).
//
// Single-goroutine contract: not safe for concurrent use.
type OnnxRecognizer struct {
	rt        *onnxruntime.Runtime // shared with the detector (not closed here)
	recSess   *onnxruntime.Session
	ctc       *common.CTCDecoder
	tempFiles []string // rec model temp path (lib + det model owned by the detector)
}

// NewEngine builds the shared detector + the ONNX rec recognizer and returns
// them assembled into an engine.BaseEngine.
//
// useVision enables the macOS Vision detection fast-path (ignored when
// unavailable). Returns pkgocr.ErrNotAvailable if the rec/det models are empty
// (build script didn't download them) or the ONNX Runtime library can't load.
func NewEngine(useVision bool) (pkgocr.Engine, error) {
	if len(ocrassets.RecModel) == 0 {
		return nil, fmt.Errorf("%w: PP-OCR rec model not embedded (run build.sh with model download)", pkgocr.ErrNotAvailable)
	}

	// Detection layer (loads ORT + det model; owns the ORT lib lifecycle).
	det, err := detect.NewDetector(useVision)
	if err != nil {
		return nil, err
	}

	// Rec session reuses the detector's Runtime/Env (process-global; a second
	// Runtime would conflict). Only the rec model temp file is owned here.
	recPath, err := writeTempFile(ocrassets.RecModel, "ppocr-rec-*.onnx")
	if err != nil {
		det.Close()
		return nil, fmt.Errorf("write rec model: %w", err)
	}
	recSess, err := det.Runtime().NewSession(det.Env(), recPath, nil)
	if err != nil {
		os.Remove(recPath)
		det.Close()
		return nil, fmt.Errorf("load rec session: %w", err)
	}

	r := &OnnxRecognizer{
		rt:        det.Runtime(),
		recSess:   recSess,
		ctc:       common.NewCTCDecoder(),
		tempFiles: []string{recPath},
	}
	return &engine.BaseEngine{Det: det, Rec: r}, nil
}

// RecognizeBoxes batches all crops into one rec inference call and returns one
// BoxText per crop (empty Text allowed; BaseEngine filters).
func (r *OnnxRecognizer) RecognizeBoxes(crops []image.Image) ([]common.BoxText, error) {
	if len(crops) == 0 {
		return nil, nil
	}
	tensorData, batchW := common.RecBatchPreprocess(crops)
	shape := []int64{int64(len(crops)), 3, common.RecHeight, int64(batchW)}

	logits, outShape, err := r.runInference(r.recSess, tensorData, shape)
	if err != nil {
		return nil, fmt.Errorf("rec batch inference: %w", err)
	}

	var B, T, nClass int
	if len(outShape) == 3 {
		B = int(outShape[0])
		T = int(outShape[1])
		nClass = int(outShape[2])
	} else {
		return nil, fmt.Errorf("unexpected rec output shape: %v", outShape)
	}
	if B != len(crops) {
		return nil, fmt.Errorf("batch size mismatch: expected %d, got %d", len(crops), B)
	}

	out := make([]common.BoxText, B)
	for b := 0; b < B; b++ {
		// Decode each batch item directly from flat logits [B*T*nClass] via
		// stride math — no per-box [][]float32 allocation.
		text, conf := r.ctc.DecodeFlat(logits, b, T, nClass)
		out[b] = common.BoxText{Text: text, Confidence: conf}
	}
	return out, nil
}

// runInference runs the rec session on a float32 input tensor and returns the
// decoded float32 output data plus its shape. Owns the input/output ONNX value
// lifecycle.
func (r *OnnxRecognizer) runInference(sess *onnxruntime.Session, tensorData []float32, shape []int64) ([]float32, []int64, error) {
	inputValue, err := onnxruntime.NewTensorValue(r.rt, tensorData, shape)
	if err != nil {
		return nil, nil, err
	}
	defer inputValue.Close()

	outputs, err := sess.Run(context.Background(), map[string]*onnxruntime.Value{"x": inputValue})
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		for _, v := range outputs {
			v.Close()
		}
	}()

	outputNames := sess.OutputNames()
	if len(outputNames) == 0 {
		return nil, nil, fmt.Errorf("rec model has no outputs")
	}
	return onnxruntime.GetTensorData[float32](outputs[outputNames[0]])
}

// Close releases the rec session/env/runtime and removes temp files.
func (r *OnnxRecognizer) Close() error {
	r.cleanup()
	return nil
}

func (r *OnnxRecognizer) cleanup() {
	// Only the rec session + rec model temp file are owned here; the ORT
	// runtime/env are owned by the detector and closed by it.
	if r.recSess != nil {
		r.recSess.Close()
	}
	for _, p := range r.tempFiles {
		os.Remove(p)
	}
	r.tempFiles = nil
}
