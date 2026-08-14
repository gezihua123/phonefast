//go:build !(darwin && cgo)

package ocr

func VisionFullOCRAvailable() bool                              { return false }
func VisionFullOCR(imgData []byte, imgW, imgH int) []TextResult { return nil }
