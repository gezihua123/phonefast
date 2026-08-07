//go:build !windows

package detect

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"sync"

	ocrassets "github.com/gezihua123/phonefast/ocr/assets"
	"github.com/gezihua123/phonefast/ocr/common"
	"github.com/shota3506/onnxruntime-purego/onnxruntime"
)

var errNotAvailable = errors.New("ocr: engine not available")

var (
	rtOnce    sync.Once
	rtShared  *onnxruntime.Runtime
	envShared *onnxruntime.Env
	rtErr     error
)

func initRuntime() (libPath string, isTemp bool, err error) {
	rtOnce.Do(func() {
		var p string
		var isT bool
		p, isT, rtErr = loadRuntimeLib()
		if rtErr != nil {
			rtErr = fmt.Errorf("%w: %v", errNotAvailable, rtErr)
			return
		}
		libPath, isTemp = p, isT
		// v23: onnxruntime-purego (upstream) only implements the v23 C API
		// adapter. ORT's GetApi(23) version negotiation makes a v23 binding
		// work against newer libonnxruntime (e.g. 1.27.1) — the returned
		// OrtApi struct's v23-offset fields keep their original layout/signature.
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
	err = rtErr
	return
}

type Detector struct {
	rt        *onnxruntime.Runtime
	env       *onnxruntime.Env
	detSess   *onnxruntime.Session
	useVision bool

	detBuf    []float32
	tempFiles []string
}

func NewDetector(useVision bool) (*Detector, error) {
	if len(ocrassets.DetModel) == 0 {
		return nil, fmt.Errorf("%w: PP-OCR det model not embedded", errNotAvailable)
	}

	libPath, libIsTemp, err := initRuntime()
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

func (d *Detector) Detect(img image.Image, imgData []byte) ([][4][2]float64, error) {
	origBounds := img.Bounds()
	origW, origH := origBounds.Dx(), origBounds.Dy()

	if d.useVision {
		if boxes := common.VisionDetect(imgData, origW, origH); len(boxes) > 0 {
			return boxes, nil
		}
	}

	tensorData, resizeW, resizeH, shape := common.DetPreprocessInto(img, 1024, d.detBuf)
	d.detBuf = tensorData
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

func (d *Detector) Close() error {
	d.cleanup()
	return nil
}

func (d *Detector) Runtime() *onnxruntime.Runtime { return d.rt }
func (d *Detector) Env() *onnxruntime.Env         { return d.env }

func (d *Detector) cleanup() {
	if d.detSess != nil {
		d.detSess.Close()
	}
	for _, p := range d.tempFiles {
		os.Remove(p)
	}
	d.tempFiles = nil
}
