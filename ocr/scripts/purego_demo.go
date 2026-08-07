//go:build ignore

// Command purego_demo is a minimal smoke test for the onnxruntime-purego
// library itself, independent of phonefast's OCR layer. It initializes the
// ORT Runtime (API v23 — the only version upstream onnxruntime-purego
// implements), creates an Env, loads a model, and runs one inference with a
// zeroed input tensor.
//
// Use this to confirm the pure-Go ORT binding can dlopen libonnxruntime and
// execute a session before debugging the higher-level OCR engine. Output
// values are meaningless — the input is zeroed; this only proves the API
// path executes.
//
// Run: go run ocr/scripts/purego_demo.go <model.onnx>
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/shota3506/onnxruntime-purego/onnxruntime"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run ocr/scripts/purego_demo.go <model.onnx>")
		os.Exit(1)
	}
	modelPath := os.Args[1]

	// ORT API v23: the only adapter upstream onnxruntime-purego implements.
	// GetApi(23) version negotiation makes this work against newer
	// libonnxruntime (e.g. 1.27.1).
	rt, err := onnxruntime.NewRuntime("", 23)
	if err != nil {
		fail("NewRuntime v23: %v "+
			"(install libonnxruntime: brew install onnxruntime / apt install onnxruntime)", err)
	}
	defer rt.Close()
	fmt.Printf("ORT runtime: api=%d version=%s\n",
		rt.GetAPIVersion(), rt.GetVersionString())

	env, err := rt.NewEnv("purego-demo", onnxruntime.LoggingLevelWarning)
	if err != nil {
		fail("NewEnv: %v", err)
	}
	defer env.Close()

	sess, err := rt.NewSession(env, modelPath, nil)
	if err != nil {
		fail("NewSession %s: %v", modelPath, err)
	}
	defer sess.Close()

	fmt.Printf("model: %s\n", modelPath)
	fmt.Printf("  inputs : %v\n", sess.InputNames())
	fmt.Printf("  outputs: %v\n", sess.OutputNames())

	inNames := sess.InputNames()
	if len(inNames) == 0 {
		fail("model %s has no inputs", modelPath)
	}
	// Zeroed placeholder tensor shaped for PP-OCR rec (1x3x48x320). Adjust
	// shape to match your model if testing non-OCR graphs.
	shape := []int64{1, 3, 48, 320}
	tensor := make([]float32, 1*3*48*320)
	inputValue, err := onnxruntime.NewTensorValue(rt, tensor, shape)
	if err != nil {
		fail("NewTensorValue: %v", err)
	}
	defer inputValue.Close()

	outputs, err := sess.Run(context.Background(), map[string]*onnxruntime.Value{inNames[0]: inputValue})
	if err != nil {
		fail("Run: %v", err)
	}
	defer func() {
		for _, v := range outputs {
			v.Close()
		}
	}()
	fmt.Printf("ran inference: %d output tensors (v23 API path OK)\n", len(outputs))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "purego_demo: "+format+"\n", args...)
	os.Exit(1)
}
