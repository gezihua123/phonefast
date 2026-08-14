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
	"sort"

	"github.com/gezihua123/phonefast/ocr"
	pkgocr "github.com/gezihua123/phonefast/ocr"
	ocrassets "github.com/gezihua123/phonefast/ocr/assets"
	"github.com/gezihua123/phonefast/ocr/common"
	"github.com/gezihua123/phonefast/ocr/detect"
	"github.com/shota3506/onnxruntime-purego/onnxruntime"
)

// Register this backend with the engine registry so the OCR service can
// construct it by name at runtime (via PHONEFAST_OCR_ENGINE=onnx).
func init() {
	ocr.RegisterEngine(pkgocr.EngineONNX, func(useVision bool) (pkgocr.Engine, error) {
		return NewEngine(useVision)
	})
}

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

	// recBuf is a reusable float32 scratch buffer for the recognition input
	// tensor. The OCR Service serializes all Recognize calls (mutex), so only
	// one RecognizeBoxes is ever in flight — the buffer is reused across calls.
	// ORT references it zero-copy; safe to reuse once the input Value is closed
	// (runInference closes it via defer before returning). Grown on demand.
	recBuf []float32
}

// NewEngine builds the shared detector + the ONNX rec recognizer and returns
// them assembled into an ocr.BaseEngine.
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
	return &ocr.BaseEngine{Det: det, Rec: r}, nil
}

// recMaxBatch is PaddleOCR's recognition batch size: PP-OCRv6_medium_rec's TRT
// dynamic shapes declare batch 1→8, and the pipeline batches ≤8. Chunking to
// ≤8 bounds the rec output [B,T,nClass] memory (worst case 8×T×18710 floats).
const recMaxBatch = 8

// RecognizeBoxes runs recognition in ≤8-wide chunks (PaddleOCR's rec batch
// size). Crops are sorted by width first — matching PaddleOCR's pipeline
// (pipeline.py:465-470 sorts by aspect ratio before rec) so similar-width crops
// share a batch and minimize padding. Results are returned in the original crop
// order (empty Text allowed; BaseEngine filters).
func (r *OnnxRecognizer) RecognizeBoxes(crops []image.Image) ([]common.BoxText, error) {
	n := len(crops)
	if n == 0 {
		return nil, nil
	}

	// Sort by width (stable → deterministic chunk membership) and keep the
	// permutation to restore original order after decoding.
	order := make([]int, n)
	widths := make([]int, n)
	for i, c := range crops {
		order[i] = i
		widths[i] = c.Bounds().Dx()
	}
	sort.SliceStable(order, func(i, j int) bool {
		return widths[order[i]] < widths[order[j]]
	})

	out := make([]common.BoxText, n)
	for start := 0; start < n; start += recMaxBatch {
		end := start + recMaxBatch
		if end > n {
			end = n
		}
		chunk := make([]image.Image, end-start)
		for k := start; k < end; k++ {
			chunk[k-start] = crops[order[k]]
		}

		tensorData, batchW := common.RecBatchPreprocessChunkInto(chunk, r.recBuf)
		r.recBuf = tensorData // keep the (possibly grown) backing array for reuse
		shape := []int64{int64(len(chunk)), 3, common.RecHeight, int64(batchW)}

		logits, outShape, err := r.runInference(r.recSess, tensorData, shape)
		if err != nil {
			return nil, fmt.Errorf("rec batch inference: %w", err)
		}

		var T, nClass int
		if len(outShape) == 3 {
			// outShape = [B, T, nClass]
			T = int(outShape[1])
			nClass = int(outShape[2])
		} else {
			return nil, fmt.Errorf("unexpected rec output shape: %v", outShape)
		}

		for b := 0; b < len(chunk); b++ {
			// Greedy CTC decoding — faithful to PaddleOCR's CTCLabelDecode
			// (processors.py:293-314): argmax per timestep → drop blank[0] →
			// merge consecutive dups → mean of per-position max probs.
			// DecodeFlat implements exactly that. (Beam search is available via
			// DecodeBeamFlat but PaddleOCR ships greedy; we match the source.)
			text, conf := r.ctc.DecodeFlat(logits, b, T, nClass)
			out[order[start+b]] = common.BoxText{Text: text, Confidence: conf}
		}
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
