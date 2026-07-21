// Package common holds PP-OCR preprocessing, postprocessing, CTC decoding,
// and macOS Vision text-detection code shared across OCR inference backends
// (ONNX Runtime, TFLite, NCNN). All code here is pure Go (plus the optional
// macOS Vision CGO bridge) and independent of any specific inference engine.
package common

import (
	"strings"

	_ "embed"
)

// ppocrKeys holds the PP-OCR v3 Chinese character dictionary (6624 chars).
// Index 0 = CTC blank token (prepended, not in file), indices 1..6624 = chars.
//
//go:embed ppocr_keys_v3.txt
var ppocrKeys string

// CTCDecoder performs CTC greedy decoding for PP-OCR recognition output.
// The recognition model outputs probabilities of shape [B, T, 6625] after
// softmax. Decoding steps:
//  1. argmax over class dim → index sequence
//  2. Remove blank tokens (index 0)
//  3. Remove consecutive duplicate indices
//  4. Map indices → characters via dictionary
//  5. Confidence = mean of max probabilities over decoded positions
type CTCDecoder struct {
	chars []string // index → character (index 0 = blank)
}

// NewCTCDecoder initializes the CTC decoder with the embedded PP-OCR dictionary.
func NewCTCDecoder() *CTCDecoder {
	chars := strings.Split(strings.TrimSpace(ppocrKeys), "\n")
	// PP-OCR convention: first entry in file = first character (index 1).
	// CTC blank is NOT in the file; we prepend it at index 0.
	chars = append([]string{""}, chars...)
	return &CTCDecoder{chars: chars}
}

// DecodeFlat performs greedy CTC decoding directly on a flat logits slice
// laid out as [B][T][nClass], decoding batch item `batchIdx`.
// Avoids constructing the [][]float32 view that Decode requires.
func (d *CTCDecoder) DecodeFlat(logits []float32, batchIdx, T, nClass int) (string, float32) {
	if len(logits) == 0 || T == 0 || nClass == 0 {
		return "", 0
	}

	var b strings.Builder
	var confSum float32
	count := 0
	prevIdx := -1
	base := batchIdx * T * nClass

	for t := 0; t < T; t++ {
		rowStart := base + t*nClass
		// argmax over class dim
		maxIdx := 0
		maxVal := logits[rowStart]
		for c := 1; c < nClass; c++ {
			v := logits[rowStart+c]
			if v > maxVal {
				maxVal = v
				maxIdx = c
			}
		}

		if maxIdx == 0 {
			prevIdx = -1
			continue
		}
		if maxIdx == prevIdx {
			continue
		}
		if maxIdx < len(d.chars) {
			b.WriteString(d.chars[maxIdx])
			confSum += maxVal
			count++
		}
		prevIdx = maxIdx
	}

	if count == 0 {
		return "", 0
	}
	conf := confSum / float32(count)
	if conf > 1.0 {
		conf = 1.0
	}
	return b.String(), conf
}
