package avcodec

// Decoder converts raw H.264 AnnexB keyframes to encoded image data.
//
// A Decoder is safe for use by a single goroutine (the session's caller)
// but is not safe for concurrent use by multiple goroutines.
type Decoder interface {
	// Decode converts a raw AnnexB H.264 keyframe (SPS+PPS+IDR) to an
	// encoded image in the requested format.
	//
	// keyframe must be a complete AnnexB bitstream beginning with SPS and
	// PPS NAL units followed by an IDR slice.
	//
	// width and height are the expected output dimensions.  If they differ
	// from the previously-decoded dimensions the decoder re-creates its
	// internal scaler.
	//
	// Returns the encoded image bytes, actual width, actual height, MIME
	// type string, and any error.
	Decode(keyframe []byte, width, height int, format ImageFormat) (data []byte, w int, h int, mime string, err error)

	// Close releases all resources held by the decoder.  After Close the
	// Decoder must not be used.
	Close() error
}
