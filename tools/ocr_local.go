// +build ignore

// Local OCR tool: run with `go run tools/ocr_local.go <image.png>`
// Uses the internal phonefast OCR engine directly on a local image file.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	ocrsvc "github.com/gezihua123/phonefast/internal/ocr"
	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: go run tools/ocr_local.go <image.png>\n")
		os.Exit(1)
	}
	imagePath := os.Args[1]
	pngData, err := os.ReadFile(imagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading image %q: %v\n", imagePath, err)
		os.Exit(1)
	}
	fmt.Printf("Image: %s (%d bytes)\n\n", imagePath, len(pngData))

	svc := ocrsvc.NewService(ocrsvc.Config{
		Engine:    pkgocr.EngineONNX,
		UseVision: false,
	})
	defer svc.Close()

	results, err := svc.Recognize(pngData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "OCR failed: %v\n", err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Println("No text recognized.")
		return
	}

	// Sort by Y then X (top-to-bottom, left-to-right)
	sort.Slice(results, func(i, j int) bool {
		_, yi := results[i].Center()
		_, yj := results[j].Center()
		if int(yi/50) != int(yj/50) {
			return yi < yj
		}
		xi, _ := results[i].Center()
		xj, _ := results[j].Center()
		return xi < xj
	})

	fmt.Printf("=== Recognized %d text regions ===\n\n", len(results))
	for i, r := range results {
		cx, cy := r.Center()
		fmt.Printf("[%2d] %q  (conf=%.2f, center=(%.0f,%.0f))\n", i+1, r.Text, r.Confidence, cx, cy)

		// Print box region for completeness
		minX, minY, maxX, maxY := r.Box[0][0], r.Box[0][1], r.Box[0][0], r.Box[0][1]
		for _, p := range r.Box {
			if p[0] < minX { minX = p[0] }
			if p[1] < minY { minY = p[1] }
			if p[0] > maxX { maxX = p[0] }
			if p[1] > maxY { maxY = p[1] }
		}
		_ = size{minX, minY, maxX, maxY}
	}

	// Also output as JSON for programmatic use
	jsonOutput, _ := json.MarshalIndent(results, "", "  ")
	fmt.Printf("\n=== JSON ===\n%s\n", string(jsonOutput))
}

type size struct{ minX, minY, maxX, maxY float64 }
