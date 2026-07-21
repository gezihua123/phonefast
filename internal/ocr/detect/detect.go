//go:build !windows

// Package detect provides the shared text-detection layer used by all OCR
// engines (onnx, ncnn). Detection is decoupled from recognition: a Detector
// finds text bounding boxes; the engine then recognizes each box with its own
// rec backend.
//
// A Detector runs macOS Vision (ANE, <1ms) as a fast-path when available and
// falls back to the PP-OCR v3 ONNX detection model otherwise — so detection
// works on every platform the onnx engine does (macOS/Linux, any arch), not
// just macOS-with-Vision. This lets the ncnn engine (macOS-only rec) share the
// same cross-platform detection instead of being hard-wired to Vision.
package detect

import (
	"context"
	"fmt"
	"image"
	"os"
	"sync"

	ocrassets "github.com/gezihua123/phonefast/assets/ocr"
	"github.com/gezihua123/phonefast/internal/ocr/common"
	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
	"github.com/shota3506/onnxruntime-purego/onnxruntime"
)

// Process-global ORT Runtime/Env singleton. onnxruntime-purego's Runtime is
// process-global (one dlopen'd lib with shared global state) — creating a
// second Runtime/Env fails with "domain already exist". So every Detector and
// the onnx rec share this one runtime/env. Initialized once via sync.Once.
var (
	rtOnce    sync.Once
	rtShared  *onnxruntime.Runtime
	envShared *onnxruntime.Env
	rtErr     error
)

// initRuntime loads the ORT library + creates a single Runtime/Env (idempotent
// via sync.Once). The temp lib path (if extracted from embed) is returned so
// the caller can track it for cleanup; subsequent callers get ("", false, nil).
func initRuntime() (libPath string, isTemp bool, err error) {
	rtOnce.Do(func() {
		var p string
		var isT bool
		p, isT, rtErr = loadRuntimeLib()
		if rtErr != nil {
			rtErr = fmt.Errorf("%w: %v", pkgocr.ErrNotAvailable, rtErr)
			return
		}
		libPath, isTemp = p, isT
		rtShared, rtErr = onnxruntime.NewRuntime(p, 23)
		if rtErr != nil {
			rtErr = fmt.Errorf("onnxruntime init: %w", rtErr)
			return
		}
		envShared, rtErr = rtShared.NewEnv("phonefast-ocr", onnxruntime.LoggingLevelWarning)
		if rtErr != nil {
			rtShared.Close()
			rtShared = nil
			rtErr = fmt.Errorf("onnxruntime env: %w", rtErr)
			return
		}
	})
	// On the first call rtOnce populated libPath/isTemp via closure; on later
	// calls they stay ""/false (the temp file is tracked by the first caller).
	err = rtErr
	return
}

// Detector finds text bounding boxes in an image. It is the shared detection
// layer: Vision fast-path (macOS ANE) with an ONNX det model fallback.
//
// Single-goroutine contract: not safe for concurrent use (the ONNX det session
// is not concurrency-safe; callers serialize via the engine/Service mutex).
type Detector struct {
	rt        *onnxruntime.Runtime
	env       *onnxruntime.Env
	detSess   *onnxruntime.Session
	useVision bool

	// tempFiles are the extracted det model + runtime-lib temp paths; removed
	// in Close.
	tempFiles []string
}

// NewDetector loads the PP-OCR v3 detection model and the ONNX Runtime.
// useVision enables the macOS Vision fast-path (ignored when unavailable).
// Returns pkgocr.ErrNotAvailable if the det model or ORT library is missing.
func NewDetector(useVision bool) (*Detector, error) {
	if len(ocrassets.DetModel) == 0 {
		return nil, fmt.Errorf("%w: PP-OCR det model not embedded (run build.sh with model download)", pkgocr.ErrNotAvailable)
	}

	libPath, libIsTemp, err := initRuntime() // process-global singleton
	if err != nil {
		return nil, err
	}

	d := &Detector{rt: rtShared, env: envShared}
	if libIsTemp {
		d.tempFiles = append(d.tempFiles, libPath)
	}

	detPath, err := WriteTempFile(ocrassets.DetModel, "ppocr-det-*.onnx")
	if err != nil {
		d.cleanup()
		return nil, fmt.Errorf("write det model: %w", err)
	}
	d.tempFiles = append(d.tempFiles, detPath)

	if d.detSess, err = rtShared.NewSession(envShared, detPath, nil); err != nil {
		d.cleanup()
		return nil, fmt.Errorf("load det session: %w", err)
	}

	if useVision && !common.VisionDetectAvailable() {
		useVision = false
	}
	d.useVision = useVision
	return d, nil
}

// Detect returns text bounding boxes (quadrilaterals in image coordinates).
// Vision fast-path first (macOS ANE); falls back to the ONNX det model.
func (d *Detector) Detect(img image.Image, pngData []byte) ([][4][2]float64, error) {
	origBounds := img.Bounds()
	origW, origH := origBounds.Dx(), origBounds.Dy()

	if d.useVision {
		if boxes := common.VisionDetect(pngData, origW, origH); len(boxes) > 0 {
			return boxes, nil
		}
	}

	tensorData, resizeW, resizeH, shape := common.DetPreprocess(img, 1024)
	probData, outShape, err := d.runInference(tensorData, shape)
	if err != nil {
		return nil, fmt.Errorf("det inference: %w", err)
	}

	var mapH, mapW int
	if len(outShape) == 4 {
		mapH = int(outShape[2])
		mapW = int(outShape[3])
	} else {
		return nil, fmt.Errorf("unexpected det output shape: %v", outShape)
	}

	boxes := common.ExtractTextBoxes(probData, mapW, mapH)

	// Scale boxes from model input space to original image space.
	scaleX := float64(origW) / float64(resizeW)
	scaleY := float64(origH) / float64(resizeH)
	for i := range boxes {
		for j := range boxes[i] {
			boxes[i][j][0] *= scaleX
			boxes[i][j][1] *= scaleY
		}
	}
	return boxes, nil
}

// runInference runs the det session on a float32 input tensor and returns the
// decoded float32 output data plus its shape. Owns the input/output ONNX value
// lifecycle.
func (d *Detector) runInference(tensorData []float32, shape []int64) ([]float32, []int64, error) {
	inputValue, err := onnxruntime.NewTensorValue(d.rt, tensorData, shape)
	if err != nil {
		return nil, nil, err
	}
	defer inputValue.Close()

	outputs, err := d.detSess.Run(context.Background(), map[string]*onnxruntime.Value{"x": inputValue})
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		for _, v := range outputs {
			v.Close()
		}
	}()

	outputNames := d.detSess.OutputNames()
	if len(outputNames) == 0 {
		return nil, nil, fmt.Errorf("det model has no outputs")
	}
	return onnxruntime.GetTensorData[float32](outputs[outputNames[0]])
}

// Close releases the det session, ONNX env/runtime, and removes temp files.
func (d *Detector) Close() error {
	d.cleanup()
	return nil
}

// Runtime returns the ONNX Runtime handle. Shared so an engine's rec layer can
// create its own session on the SAME runtime/env — onnxruntime-purego's
// Runtime is process-global (one dlopen'd lib), and a second Runtime/Env would
// conflict ("domain already exist"). Engines that need an ORT session (e.g. the
// onnx rec) must reuse this rather than creating their own.
func (d *Detector) Runtime() *onnxruntime.Runtime { return d.rt }

// Env returns the ONNX environment, shared for the same reason as Runtime.
func (d *Detector) Env() *onnxruntime.Env { return d.env }

func (d *Detector) cleanup() {
	// Only the det session + det model temp file are owned here; the ORT
	// runtime/env are process-global singletons (shared across Detectors + the
	// onnx rec), never closed by one Detector.
	if d.detSess != nil {
		d.detSess.Close()
	}
	for _, p := range d.tempFiles {
		os.Remove(p)
	}
	d.tempFiles = nil
}
