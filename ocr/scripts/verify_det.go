//go:build ignore

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/png"
	"os"

	ocrassets "github.com/gezihua123/phonefast/ocr/assets"
	"github.com/gezihua123/phonefast/ocr/common"
	"github.com/gezihua123/phonefast/ocr/detect"
	"github.com/shota3506/onnxruntime-purego/onnxruntime"
)

func main() {
	// Load test image
	f, _ := os.Open("/tmp/test_text.png")
	defer f.Close()
	img, _, _ := image.Decode(f)

	// Build det tensor (same as Detector.Detect does)
	tensor, rw, rh, shape := common.DetPreprocess(img, 1024)
	fmt.Printf("Det tensor: shape=%v resize=%dx%d len=%d\n", shape, rw, rh, len(tensor))

	det, _ := detect.NewDetector(false)
	defer det.Close()
	fmt.Printf("ORT API version: %d\n", det.Runtime().GetAPIVersion())

	// Load det model fresh and run directly
	detPath, _ := detect.WriteTempFile(ocrassets.DetModel, "verify-det-*.onnx")
	defer os.Remove(detPath)
	detSess, err := det.Runtime().NewSession(det.Env(), detPath, nil)
	if err != nil {
		fmt.Printf("NewSession: %v\n", err)
		os.Exit(1)
	}
	defer detSess.Close()

	inputValue, _ := onnxruntime.NewTensorValue(det.Runtime(), tensor, shape)
	outputs, err := detSess.Run(context.Background(), map[string]*onnxruntime.Value{"x": inputValue})
	inputValue.Close()
	if err != nil {
		fmt.Printf("Run: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		for _, v := range outputs {
			v.Close()
		}
	}()

	onames := detSess.OutputNames()
	probData, outShape, _ := onnxruntime.GetTensorData[float32](outputs[onames[0]])
	fmt.Printf("Det output shape: %v len=%d\n", outShape, len(probData))

	// Count non-zero / >0.3 cells (text regions)
	nonblank, aboveThresh := 0, 0
	for _, v := range probData {
		if v > 0 {
			nonblank++
		}
		if v > 0.3 {
			aboveThresh++
		}
	}
	fmt.Printf("Det prob: min=%.4f max=%.4f  nonblank=%d  >0.3=%d\n",
		tensorMin(probData), tensorMax(probData), nonblank, aboveThresh)

	// Also export the rec tensor for comparison and run rec
	data, _ := os.ReadFile("/tmp/go_rec_tensor.bin")
	recTensor := make([]float32, len(data)/4)
	for i := range recTensor {
		recTensor[i] = float32(binary.LittleEndian.Uint32(data[i*4:]))
	}
	recPath, _ := detect.WriteTempFile(ocrassets.RecModel, "verify-rec-*.onnx")
	defer os.Remove(recPath)
	recSess, _ := det.Runtime().NewSession(det.Env(), recPath, nil)
	defer recSess.Close()

	recShape := []int64{1, 3, 48, 320}
	recInput, _ := onnxruntime.NewTensorValue(det.Runtime(), recTensor, recShape)
	recOut, _ := recSess.Run(context.Background(), map[string]*onnxruntime.Value{"x": recInput})
	recInput.Close()
	defer func() {
		for _, v := range recOut {
			v.Close()
		}
	}()
	recOnames := recSess.OutputNames()
	recLogits, recOutShape, _ := onnxruntime.GetTensorData[float32](recOut[recOnames[0]])
	fmt.Printf("\nRec output shape: %v len=%d\n", recOutShape, len(recLogits))
	fmt.Printf("Rec logits: min=%.4f max=%.4f\n", tensorMin(recLogits), tensorMax(recLogits))

	// Compare first 5 timesteps argmax
	T := int(recOutShape[1])
	nClass := int(recOutShape[2])
	_ = T
	fmt.Print("Go rec argmax (first 5): ")
	for t := 0; t < 5; t++ {
		base := t * nClass
		maxIdx, maxVal := 0, recLogits[base]
		for c := 1; c < nClass; c++ {
			if recLogits[base+c] > maxVal {
				maxVal = recLogits[base+c]
				maxIdx = c
			}
		}
		fmt.Printf("[t%d:%d=%.3f] ", t, maxIdx, maxVal)
	}
	fmt.Println()
}

func tensorMin(d []float32) float32 {
	if len(d) == 0 {
		return 0
	}
	m := d[0]
	for _, v := range d {
		if v < m {
			m = v
		}
	}
	return m
}
func tensorMax(d []float32) float32 {
	if len(d) == 0 {
		return 0
	}
	m := d[0]
	for _, v := range d {
		if v > m {
			m = v
		}
	}
	return m
}
