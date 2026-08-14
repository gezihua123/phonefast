//go:build ignore

// Command ocr_image runs the ONNX OCR engine on a local image and prints all
// recognized text boxes — handy for eyeballing recognition quality.
//
// Run: go run ocr/scripts/ocr_image.go <image-path>
package main

import (
	"fmt"
	"os"

	ocrsvc "github.com/gezihua123/phonefast/ocr"
	_ "github.com/gezihua123/phonefast/ocr/onnx" // register ONNX backend
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ocr_image.go <image-path>")
		os.Exit(1)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	svc := ocrsvc.NewService(ocrsvc.Config{Engine: ocrsvc.EngineONNX, UseVision: false})
	defer svc.Close()

	results, err := svc.Recognize(data)
	if err != nil {
		panic(err)
	}
	fmt.Printf("OCR: %d text regions\n", len(results))
	for _, r := range results {
		cx, cy := r.Center()
		fmt.Printf("  text=%q conf=%.2f center=(%.0f,%.0f)\n", r.Text, r.Confidence, cx, cy)
	}
}
