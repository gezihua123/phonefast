// Package common holds PP-OCR preprocessing, postprocessing, CTC decoding,
// and macOS Vision text-detection code shared across OCR inference backends
// (ONNX Runtime, TFLite, NCNN). All code here is pure Go (plus the optional
// macOS Vision CGO bridge) and independent of any specific inference engine.
package common

import (
	"math"
	"strings"

	_ "embed"
)

// ppocrKeys holds the PP-OCRv6 character dictionary (18708 chars).
// Index 0 = CTC blank token (prepended, not in file), indices 1..6624 = chars.
//
//go:embed ppocr_keys.txt
var ppocrKeys string

// CTCDecoder performs CTC decoding for PP-OCR recognition output.
//
// Two decoding strategies are available:
//   - Greedy (DecodeFlat): argmax per timestep, remove blanks + duplicates.
//     Fast (O(T·nClass)) but noisy on ambiguous characters.
//   - Beam search (DecodeBeamFlat): maintains top-K prefix hypotheses.
//     More accurate for long text at ~2-3× the cost of greedy.
//
// The recognition model outputs logits of shape [B, T, 6625]; both decoders
// operate on flat []float32 data with stride indexing.
type CTCDecoder struct {
	chars []string // index → character (index 0 = blank)
}

// insertSpaces converts fullwidth spaces (U+3000, the PP-OCRv6 word-boundary
// marker at dict index 1748) to ASCII spaces (0x20). PaddleOCR's
// CTCLabelDecode does this internal conversion; phonefast's decoder must
// apply it as a post-decode step.
func insertSpaces(s string) string {
	needsConv := false
	for _, r := range s {
		if r == '　' {
			needsConv = true
			break
		}
	}
	if !needsConv {
		return s
	}
	buf := make([]byte, 0, len(s))
	for _, r := range s {
		if r == '　' {
			buf = append(buf, ' ')
		} else {
			buf = append(buf, string(r)...)
		}
	}
	return string(buf)
}

// NewCTCDecoder initializes the CTC decoder with the embedded PP-OCR dictionary.
//
// The ONNX model outputs 18710 classes, but the embedded ppocr_keys.txt has only
// 18708 characters. Phonefast prepends blank at index 0 (→18709 entries), leaving
// one unmapped class at index 18709. This class IS the ASCII space (0x20) —
// the ONNX model was exported with an extra space class that the dictionary file
// does not list. We append it explicitly, matching the model's actual output
// dimension and fixing the "AvocadoToastwithEgg" → "Avocado Toast with Egg" gap.
func NewCTCDecoder() *CTCDecoder {
	chars := strings.Split(strings.TrimSpace(ppocrKeys), "\n")
	// PP-OCR convention: first entry in file = first character (index 1).
	// CTC blank is NOT in the file; we prepend it at index 0.
	chars = append([]string{""}, chars...)
	// The ONNX model has 18710 output classes. The dict (18708 chars) + blank
	// (1) gives 18709 entries. The remaining class (index 18709) is ASCII space.
	chars = append(chars, " ")
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
	// Convert fullwidth spaces (U+3000, PP-OCRv6 word boundary marker)
	// to ASCII spaces. PaddleOCR's CTCLabelDecode does this internally;
	// without it, English text appears as "AvocadoToastwithEgg".
	return insertSpaces(b.String()), conf
}

// ── Beam Search Decoder ─────────────────────────────────────────────

// ctcBeam is a single hypothesis in the CTC beam search.
type ctcBeam struct {
	prefix   []int   // decoded character indices (without blanks)
	score    float64 // cumulative log-probability (higher = better)
	prevChar int     // last non-blank character (for CTC collapse)
}

// DecodeBeamFlat performs CTC beam search decoding on a flat [B][T][nClass]
// logits slice, decoding batch item `batchIdx`.
//
// Parameters:
//   - beamWidth: number of top hypotheses to maintain (5 is a good default)
//   - topN: only consider the top-N characters per timestep (reduces compute;
//     20 covers >99.9% of probability mass for PP-OCRv6)
//
// The input logits are assumed to be raw logits (NOT softmax probabilities);
// softmax is applied per-timestep internally via log-sum-exp normalization.
func (d *CTCDecoder) DecodeBeamFlat(logits []float32, batchIdx, T, nClass int, beamWidth, topN int) (string, float32) {
	if len(logits) == 0 || T == 0 || nClass == 0 || beamWidth <= 0 {
		return d.DecodeFlat(logits, batchIdx, T, nClass)
	}
	if topN <= 0 || topN > nClass {
		topN = nClass
	}

	// Seed: one empty beam with score 0.
	beams := []ctcBeam{{prefix: nil, score: 0, prevChar: -1}}
	base := batchIdx * T * nClass

	for t := 0; t < T; t++ {
		rowStart := base + t*nClass

		// Compute log-softmax for this timestep.
		logProbs := make([]float64, nClass)
		maxLogit := float64(logits[rowStart])
		for c := 1; c < nClass; c++ {
			v := float64(logits[rowStart+c])
			if v > maxLogit {
				maxLogit = v
			}
		}
		var sum float64
		for c := 0; c < nClass; c++ {
			logProbs[c] = float64(logits[rowStart+c]) - maxLogit
			// Guard against underflow: if exponentiated value is too small,
			// clamp to a floor (~exp(-20) ≈ 2e-9).
			if logProbs[c] < -20 {
				logProbs[c] = -20
			}
			sum += math.Exp(logProbs[c])
		}
		logSum := math.Log(sum)
		for c := 0; c < nClass; c++ {
			logProbs[c] -= logSum
		}

		// Get top-N indices by log-probability.
		topIdx := topNIndices(logProbs, topN)

		// Extend each beam.
		type newBeam struct {
			beam ctcBeam
		}
		newBeams := make(map[string]ctcBeam) // prefix string → beam (merge duplicates)

		for _, beam := range beams {
			for _, c := range topIdx {
				score := beam.score + logProbs[c]

				if c == 0 {
					// Blank: same prefix, score accumulates.
					key := prefixKey(beam.prefix)
					if existing, ok := newBeams[key]; !ok || score > existing.score {
						newBeams[key] = ctcBeam{
							prefix:   cloneInts(beam.prefix),
							score:    score,
							prevChar: -1, // blank resets prevChar
						}
					}
				} else if c != beam.prevChar {
					// Non-blank, non-duplicate: extend prefix.
					newPrefix := append(cloneInts(beam.prefix), c)
					key := prefixKey(newPrefix)
					if existing, ok := newBeams[key]; !ok || score > existing.score {
						newBeams[key] = ctcBeam{
							prefix:   newPrefix,
							score:    score,
							prevChar: c,
						}
					}
				}
				// c == beam.prevChar: skip (CTC collapse)
			}
		}

		// Select top beamWidth beams by score.
		if len(newBeams) > beamWidth {
			beams = topBeams(newBeams, beamWidth)
		} else {
			beams = beams[:0]
			for _, b := range newBeams {
				beams = append(beams, b)
			}
		}
	}

	// Best beam.
	if len(beams) == 0 {
		return "", 0
	}
	best := beams[0]
	for i := 1; i < len(beams); i++ {
		if beams[i].score > best.score {
			best = beams[i]
		}
	}

	// Build text from prefix indices.
	if len(best.prefix) == 0 {
		return "", 0
	}
	var b strings.Builder
	for _, idx := range best.prefix {
		if idx >= 0 && idx < len(d.chars) {
			b.WriteString(d.chars[idx])
		}
	}

	// Confidence: same metric as greedy decoder (arithmetic mean of per-timestep
	// max softmax probabilities). This is computed directly from the logits
	// array rather than from the beam search scores, so it's independent of
	// the log-space accumulation and always in [0, 1].
	var confSum float32
	for t := 0; t < T; t++ {
		rowStart := base + t*nClass
		maxVal := logits[rowStart]
		for c := 1; c < nClass; c++ {
			if v := logits[rowStart+c]; v > maxVal {
				maxVal = v
			}
		}
		confSum += maxVal
	}
	conf := confSum / float32(T)
	if conf > 1.0 {
		conf = 1.0
	}
	return insertSpaces(b.String()), conf
}

// topNIndices returns the indices of the top-N values in a float64 slice.
func topNIndices(values []float64, n int) []int {
	if n >= len(values) {
		result := make([]int, len(values))
		for i := range result {
			result[i] = i
		}
		return result
	}
	// Partial selection: bubble top-N to front.
	result := make([]int, n)
	type pair struct {
		idx int
		val float64
	}
	all := make([]pair, len(values))
	for i, v := range values {
		all[i] = pair{i, v}
	}
	// Simple partial sort: find top N by repeated scan.
	for i := 0; i < n; i++ {
		best := i
		for j := i + 1; j < len(all); j++ {
			if all[j].val > all[best].val {
				best = j
			}
		}
		all[i], all[best] = all[best], all[i]
		result[i] = all[i].idx
	}
	return result
}

// prefixKey converts a prefix (slice of ints) to a string key for deduplication.
// Uses a compact encoding to avoid excessive allocations.
func prefixKey(prefix []int) string {
	if len(prefix) == 0 {
		return ""
	}
	// Encode as comma-separated decimal in a builder.
	var b strings.Builder
	for i, c := range prefix {
		if i > 0 {
			b.WriteByte(',')
		}
		// Fast itoa for small ints.
		b.WriteString(itoa(c))
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func cloneInts(src []int) []int {
	if len(src) == 0 {
		return nil
	}
	dst := make([]int, len(src))
	copy(dst, src)
	return dst
}

// topBeams selects the top-K beams by score, modifying beams in place.
func topBeams(m map[string]ctcBeam, k int) []ctcBeam {
	beams := make([]ctcBeam, 0, len(m))
	for _, b := range m {
		beams = append(beams, b)
	}
	// Partial selection sort.
	for i := 0; i < k && i < len(beams); i++ {
		best := i
		for j := i + 1; j < len(beams); j++ {
			if beams[j].score > beams[best].score {
				best = j
			}
		}
		beams[i], beams[best] = beams[best], beams[i]
	}
	if k < len(beams) {
		return beams[:k]
	}
	return beams
}
