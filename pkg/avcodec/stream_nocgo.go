//go:build !cgo

package avcodec

import "fmt"

// NewStreamDecoder returns ErrNotAvailable when CGO is disabled. The caller
// should fall back to the keyframe reset path (ffmpeg CLI decode).
func NewStreamDecoder() (StreamDecoder, error) {
	return nil, fmt.Errorf("%w", ErrNotAvailable)
}
