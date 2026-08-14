//go:build darwin && cgo

package ocr

/*
#cgo LDFLAGS: -framework Foundation -framework Vision -framework CoreGraphics -framework AppKit
#include <stdlib.h>

typedef struct {
    char *text;
    float x, y, width, height;
    float confidence;
} RecognizedText;

RecognizedText* recognizeText(const uint8_t *imgData, size_t imgLen,
                               int *outCount, const char *langs);
void freeRecognizedText(RecognizedText *items, int count);
*/
import "C"

import (
	"os"
	"unsafe"

)

// VisionFullOCRAvailable reports whether VNRecognizeTextRequest is available.
func VisionFullOCRAvailable() bool {
	return true
}

// VisionFullOCR runs VNRecognizeTextRequest on image bytes and returns
// recognized text with bounding boxes. lang is comma-separated language
// codes (default "zh-Hans,zh-Hant,en-US"). Uses Apple ANE acceleration.
func VisionFullOCR(imgData []byte, imgW, imgH int) []TextResult {
	if len(imgData) == 0 {
		return nil
	}

	lang := os.Getenv("PHONEFAST_VISION_LANGS")
	var cLang *C.char
	if lang != "" {
		cLang = C.CString(lang)
		defer C.free(unsafe.Pointer(cLang))
	}

	var count C.int
	p := C.recognizeText(
		(*C.uint8_t)(unsafe.Pointer(&imgData[0])),
		C.size_t(len(imgData)),
		&count,
		cLang,
	)
	if p == nil || count == 0 {
		return nil
	}
	defer C.freeRecognizedText(p, count)

	n := int(count)
	items := unsafe.Slice((*C.RecognizedText)(unsafe.Pointer(p)), n)

	results := make([]TextResult, 0, n)
	for _, item := range items {
		text := C.GoString(item.text)
		if text == "" {
			continue
		}
		x1 := float64(item.x)
		y1 := float64(item.y)
		x2 := x1 + float64(item.width)
		y2 := y1 + float64(item.height)

		if x1 < 0 {
			x1 = 0
		}
		if y1 < 0 {
			y1 = 0
		}
		if x2 > float64(imgW) {
			x2 = float64(imgW)
		}
		if y2 > float64(imgH) {
			y2 = float64(imgH)
		}
		if x2-x1 < 1 || y2-y1 < 1 {
			continue
		}

		results = append(results, TextResult{
			Text: text,
			Box: [4][2]float64{
				{x1, y1}, {x2, y1}, {x2, y2}, {x1, y2},
			},
			Confidence: float32(item.confidence),
		})
	}
	return results
}
