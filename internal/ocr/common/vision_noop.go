//go:build !(darwin && cgo)

package common

// VisionDetectAvailable returns false on non-macOS or non-CGO builds.
func VisionDetectAvailable() bool {
	return false
}

// VisionDetect is a no-op stub for non-macOS platforms.
func VisionDetect(pngData []byte, imgW, imgH int) [][4][2]float64 {
	return nil
}
