//go:build windows

package detect

import (
	"errors"
	"fmt"
	"image"
)

var errNotAvailable = errors.New("ocr: engine not available")

type Detector struct{}

func NewDetector(useVision bool) (*Detector, error) {
	return nil, fmt.Errorf("%w: OCR detection not supported on Windows", errNotAvailable)
}

func (d *Detector) Detect(img image.Image, pngData []byte) ([][4][2]float64, error) {
	return nil, fmt.Errorf("%w: unavailable", errNotAvailable)
}

func (d *Detector) Close() error { return nil }
