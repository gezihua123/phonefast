//go:build !cgo

package avcodec

import "fmt"

// NewDecoder returns ErrNotAvailable when CGO is disabled.  The caller should
// fall back to the ffmpeg CLI subprocess.
func NewDecoder(_, _ int) (Decoder, error) {
	return nil, fmt.Errorf("%w", ErrNotAvailable)
}
