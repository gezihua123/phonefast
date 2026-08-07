//go:build !windows

// Package tesseract provides a Tesseract OCR backend for phonefast.
// It uses the system `tesseract` CLI as a subprocess — no CGO dependency,
// CGO_ENABLED=0 compatible. If tesseract is not installed, NewEngine returns
// ErrNotAvailable.
//
// Tesseract is best for Latin/English text. It has no Chinese support
// (0 results on Chinese text in testing — see docs/DEV.md). For mixed
// or CJK text, use the default ONNX engine instead.
//
// Usage:
//
//	PHONEFAST_OCR_ENGINE=tesseract phonefast ocr
//	PHONEFAST_TESSERACT_LANG=eng+chi_sim  (default: eng)
package tesseract

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gezihua123/phonefast/ocr"
	pkgocr "github.com/gezihua123/phonefast/ocr"
)

// Register this backend with the engine registry so the OCR service can
// construct it by name at runtime (via PHONEFAST_OCR_ENGINE=tesseract).
func init() {
	ocr.RegisterEngine(pkgocr.EngineTesseract, func(useVision bool) (pkgocr.Engine, error) {
		return NewEngine()
	})
}

// EngineTess implements pkgocr.Engine using the system tesseract CLI.
// Unlike the ONNX/NCNN engines, Tesseract handles both detection and
// recognition internally — it does not use the shared detect.Detector.
type EngineTess struct {
	binPath string // absolute path to tesseract binary
	lang    string // language codes (default "eng")
	tmpDir  string // temp dir for intermediate image files
}

// NewEngine creates a Tesseract OCR engine. Returns ErrNotAvailable if
// tesseract is not installed.
func NewEngine() (pkgocr.Engine, error) {
	bin, err := exec.LookPath("tesseract")
	if err != nil {
		return nil, fmt.Errorf("%w: tesseract CLI not found on PATH (install with `brew install tesseract` or `apt install tesseract-ocr`)", pkgocr.ErrNotAvailable)
	}
	// Resolve symlinks so the binary path is absolute.
	bin, err = filepath.Abs(bin)
	if err != nil {
		return nil, fmt.Errorf("%w: tesseract path resolution failed: %v", pkgocr.ErrNotAvailable, err)
	}

	lang := os.Getenv("PHONEFAST_TESSERACT_LANG")
	if lang == "" {
		lang = "eng"
	}

	tmpDir, err := os.MkdirTemp("", "phonefast-tesseract-*")
	if err != nil {
		return nil, fmt.Errorf("%w: create temp dir: %v", pkgocr.ErrNotAvailable, err)
	}

	return &EngineTess{binPath: bin, lang: lang, tmpDir: tmpDir}, nil
}

// Recognize runs Tesseract on the image bytes and returns text regions
// with bounding boxes. Accepts PNG or JPEG.
func (e *EngineTess) Recognize(imgBytes []byte) ([]pkgocr.TextResult, error) {
	// Decode to get image dimensions.
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("tesseract: decode image: %w", err)
	}
	bounds := img.Bounds()
	imgW := bounds.Dx()
	imgH := bounds.Dy()
	img = nil // release decoded image

	// Write image to temp file.
	inPath := filepath.Join(e.tmpDir, "ocr_in.png")
	if err := os.WriteFile(inPath, imgBytes, 0o600); err != nil {
		return nil, fmt.Errorf("tesseract: write temp image: %w", err)
	}
	defer os.Remove(inPath)

	outBase := filepath.Join(e.tmpDir, "ocr_out")

	// Run tesseract: TSV output gives us bounding boxes + text + confidence.
	cmd := exec.Command(e.binPath,
		inPath,
		outBase,
		"-l", e.lang,
		"--psm", "6", // uniform block of text (best for UI screenshots)
		"tsv",
	)
	// Tesseract writes to stderr for progress info; capture only for diagnostics.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tesseract: exec failed: %v (stderr: %s)", err, stderr.String())
	}

	// Parse TSV output.
	tsvPath := outBase + ".tsv"
	defer os.Remove(tsvPath)

	return parseTSV(tsvPath, imgW, imgH)
}

// Close removes the temp directory.
func (e *EngineTess) Close() error {
	return os.RemoveAll(e.tmpDir)
}

// parseTSV parses tesseract TSV output into TextResult slices.
// The TSV format has a header row followed by data rows. We extract
// level 5 (word) entries with their bounding boxes and confidence.
//
// Columns: level, page_num, block_num, par_num, line_num, word_num,
//
//	left, top, width, height, conf, text
func parseTSV(path string, imgW, imgH int) ([]pkgocr.TextResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("tesseract: open TSV: %w", err)
	}
	defer f.Close()

	var results []pkgocr.TextResult
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "level\t") {
			continue // skip header
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 12 {
			continue
		}

		level, err := strconv.Atoi(fields[0])
		if err != nil || level != 5 { // word level
			continue
		}

		text := fields[11]
		if text == "" {
			continue
		}
		conf, _ := strconv.ParseFloat(fields[10], 64)
		if conf < 40 { // filter very low confidence
			continue
		}

		// Bounding box in image pixel coordinates.
		left, _ := strconv.Atoi(fields[6])
		top, _ := strconv.Atoi(fields[7])
		w, _ := strconv.Atoi(fields[8])
		h, _ := strconv.Atoi(fields[9])

		if w <= 0 || h <= 0 {
			continue
		}

		// Clamp to image bounds.
		x1 := float64(max(0, left))
		y1 := float64(max(0, top))
		x2 := float64(min(float64(left)+float64(w), float64(imgW)))
		y2 := float64(min(float64(top)+float64(h), float64(imgH)))

		results = append(results, pkgocr.TextResult{
			Text: text,
			Box: [4][2]float64{
				{x1, y1}, // top-left
				{x2, y1}, // top-right
				{x2, y2}, // bottom-right
				{x1, y2}, // bottom-left
			},
			Confidence: float32(math.Round(conf*100) / 100),
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("tesseract: read TSV: %w", err)
	}

	return results, nil
}
