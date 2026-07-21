//go:build !(darwin && cgo && ncnn)

package ncnn

import (
	"fmt"

	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

// NewEngine returns ErrNotAvailable when the `ncnn` build tag is not set.
//
// The real NCNN backend (ncnn.go) is macOS-only and gated behind the `ncnn`
// build tag. It loads libncnn at runtime via purego (no CGO link), so the
// default build has zero ncnn dependency. Enable with:
//
//	bash scripts/setup-ncnn.sh                # brew install ncnn + build model
//	CGO_ENABLED=1 go build -tags ncnn ./...   # CGO needed for Vision detection
//
// useVision enables the macOS Vision detection fast-path. The converted
// .param/.bin model paths come from PHONEFAST_NCNN_PARAM / PHONEFAST_NCNN_BIN
// env vars (set by setup-ncnn.sh's output instructions).
func NewEngine(useVision bool) (pkgocr.Engine, error) {
	return nil, fmt.Errorf("%w: NCNN OCR engine not built in (macOS-only; run scripts/setup-ncnn.sh then build with -tags ncnn)", pkgocr.ErrNotAvailable)
}
