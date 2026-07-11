// Package avcodec provides H.264 keyframe → image decoding.
//
// Primary path: CGO bindings to FFmpeg libavcodec/libswscale via go-astiav.
// Fallback path: ffmpeg CLI subprocess (delegated to internal/session).
//
// The package is designed so that the CGO code lives in decode_astiav.go
// (build tag "cgo") and the non-CGO stub lives in decode_nocgo.go (build tag
// "!cgo").  Callers always import avcodec; the right implementation is
// selected at compile time.
package avcodec

import (
	"errors"
	"fmt"
)

// ImageFormat specifies the output image format.
type ImageFormat int

const (
	FormatPNG ImageFormat = iota // default
	FormatJPEG
)

// String returns the MIME type for the format ("image/png" or "image/jpeg").
func (f ImageFormat) String() string {
	switch f {
	case FormatJPEG:
		return "image/jpeg"
	default:
		return "image/png"
	}
}

// Extension returns the file extension without a leading dot ("png" or "jpeg").
func (f ImageFormat) Extension() string {
	switch f {
	case FormatJPEG:
		return "jpeg"
	default:
		return "png"
	}
}

// FormatFromString converts a format name to ImageFormat.
// Recognized values: "jpeg", "jpg" → FormatJPEG; anything else → FormatPNG.
func FormatFromString(s string) ImageFormat {
	switch s {
	case "jpeg", "jpg":
		return FormatJPEG
	default:
		return FormatPNG
	}
}

// ErrNotAvailable is returned by NewDecoder when the CGO decoder is not
// compiled in (CGO_ENABLED=0).  The caller should fall back to the ffmpeg CLI
// subprocess.
var ErrNotAvailable = errors.New("avcodec: CGO decoder not available (build without -tags cgo)")

// A DecodeError wraps an error returned by the underlying decoder.
type DecodeError struct {
	Op  string // operation that failed (e.g. "send_packet", "receive_frame", "scale", "encode")
	Err error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("avcodec %s: %v", e.Op, e.Err)
}

func (e *DecodeError) Unwrap() error { return e.Err }

// newDecodeError is a small helper for constructing decode errors.
func newDecodeError(op string, err error) error {
	return &DecodeError{Op: op, Err: err}
}
