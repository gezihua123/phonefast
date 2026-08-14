//go:build !(darwin && cgo)

package apple

import (
	"fmt"

	pkgocr "github.com/gezihua123/phonefast/ocr"
)

// NewEngine returns ErrNotAvailable on non-macOS or non-CGO builds.
func NewEngine(useVision bool) (pkgocr.Engine, error) {
	return nil, fmt.Errorf("%w: Apple Vision OCR requires macOS with CGO", pkgocr.ErrNotAvailable)
}
