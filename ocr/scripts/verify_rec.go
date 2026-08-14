//go:build ignore

// Command verify_rec loads a pre-exported rec input tensor
// (/tmp/go_rec_tensor.bin, 1x3x48x320 float32 — a crop of "Hello World 你好世界"),
// runs it through the PP-OCRv6 rec model via the onnxruntime-purego binding,
// and CTC-decodes the result. Used to verify rec produces correct (non-garbled)
// recognition output on the current libonnxruntime build.
//
// Run: CGO_ENABLED=1 go run ocr/scripts/verify_rec.go
package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"

	ocrassets "github.com/gezihua123/phonefast/ocr/assets"
	"github.com/gezihua123/phonefast/ocr/common"
	"github.com/gezihua123/phonefast/ocr/detect"
	"github.com/shota3506/onnxruntime-purego/onnxruntime"
)

const tensorPath = "/tmp/go_rec_tensor.bin"

func main() {
	// 1. Load the exported tensor as []float32.
	data, err := os.ReadFile(tensorPath)
	if err != nil {
		fail("read tensor %s: %v", tensorPath, err)
	}
	const wantFloats = 1 * 3 * 48 * 320
	if len(data)%4 != 0 || len(data)/4 != wantFloats {
		fail("tensor size mismatch: file=%d bytes (%d float32), want %d float32 (1x3x48x320)",
			len(data), len(data)/4, wantFloats)
	}
	tensor := make([]float32, wantFloats)
	for i := 0; i < wantFloats; i++ {
		bits := binary.LittleEndian.Uint32(data[i*4:])
		tensor[i] = math.Float32frombits(bits)
	}
	fmt.Printf("loaded tensor: %d float32 (1x3x48x320)\n", len(tensor))

	// 2. Create the detector — this is what initializes the process-global
	//    ORT Runtime/Env via NewRuntime(p, 27) (the fix under test).
	det, err := detect.NewDetector(false)
	if err != nil {
		fail("new detector: %v", err)
	}
	defer det.Close()
	fmt.Printf("ORT runtime ready: api=%d version=%s\n",
		det.Runtime().GetAPIVersion(), det.Runtime().GetVersionString())

	// 3. Load the rec model into a new session on the shared runtime/env.
	recPath, err := detect.WriteTempFile(ocrassets.RecModel, "ppocr-rec-*.onnx")
	if err != nil {
		fail("write rec model: %v", err)
	}
	defer os.Remove(recPath)
	recSess, err := det.Runtime().NewSession(det.Env(), recPath, nil)
	if err != nil {
		fail("load rec session: %v", err)
	}
	defer recSess.Close()

	fmt.Printf("rec input names:  %v\n", recSess.InputNames())
	fmt.Printf("rec output names: %v\n", recSess.OutputNames())

	// 4. Run inference: input name "x", shape [1,3,48,320].
	shape := []int64{1, 3, 48, 320}
	inputValue, err := onnxruntime.NewTensorValue(det.Runtime(), tensor, shape)
	if err != nil {
		fail("new tensor value: %v", err)
	}
	defer inputValue.Close()

	outputs, err := recSess.Run(context.Background(), map[string]*onnxruntime.Value{"x": inputValue})
	if err != nil {
		fail("rec run: %v", err)
	}
	defer func() {
		for _, v := range outputs {
			v.Close()
		}
	}()

	// Pick the output value. The task names "fetch_name_0"; fall back to the
	// first declared output name (the production rec path uses [0]).
	outNames := recSess.OutputNames()
	outName := "fetch_name_0"
	if _, ok := outputs[outName]; !ok && len(outNames) > 0 {
		outName = outNames[0]
	}
	outVal, ok := outputs[outName]
	if !ok {
		fail("output %q not found in run results (available: %v)", outName, outputs)
	}
	logits, outShape, err := onnxruntime.GetTensorData[float32](outVal)
	if err != nil {
		fail("get tensor data: %v", err)
	}
	fmt.Printf("rec output %q shape: %v (len=%d)\n", outName, outShape, len(logits))

	// 5. CTC-decode batch item 0. Output layout is [B, T, nClass].
	var T, nClass int
	switch len(outShape) {
	case 3:
		T = int(outShape[1])
		nClass = int(outShape[2])
	case 2:
		// Some rec graphs squeeze the batch dim.
		T = int(outShape[0])
		nClass = int(outShape[1])
	default:
		fail("unexpected rec output rank: %v", outShape)
	}
	dec := common.NewCTCDecoder()
	text, conf := dec.DecodeFlat(logits, 0, T, nClass)
	fmt.Printf("Go decoded text  : %q (confidence=%.4f)\n", text, conf)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "verify_rec: "+format+"\n", args...)
	os.Exit(1)
}
