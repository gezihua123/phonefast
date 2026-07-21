//go:build darwin && cgo && ncnn

// Package ncnn implements the ocr.Engine interface using Tencent's NCNN
// inference framework, loaded at runtime via purego (no CGO, no compile-time
// link). NCNN is a macOS-only opt-in backend, activated by the `ncnn` build tag.
//
// Setup (installs the brew lib + builds the converted model):
//
//	bash scripts/setup-ncnn.sh
//	CGO_ENABLED=0 go build -tags ncnn ./...   # no CGO needed!
//
// The library is dlopen'd at runtime (like the onnx engine loads libonnxruntime):
// if libncnn is not present, NewEngine returns ErrNotAvailable instead of
// crashing — the default binary runs fine on a Mac without brew ncnn installed,
// and the ncnn engine simply stays unavailable until the user runs setup-ncnn.sh.
//
// Detection reuses the macOS Vision ANE path from internal/ocr/common (same as
// the onnx engine). Only recognition is swapped to NCNN. NCNN does not batch, so
// recognition runs one box at a time at a fixed input width (common.RecMaxWidth);
// still ~22% faster end-to-end than onnx on Apple Silicon — see docs/DEV.md.
package ncnn

import (
	"fmt"
	"image"
	"os"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/gezihua123/phonefast/internal/ocr/common"
	"github.com/gezihua123/phonefast/internal/ocr/detect"
	"github.com/gezihua123/phonefast/internal/ocr/engine"
	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

// recNClass is the PP-OCR v3 recognition output class count (CTC blank + 6624 chars).
const recNClass = 6625

// NCNN C API function bindings (resolved at runtime via purego.Dlsym).
// Each is a Go function variable bound to the corresponding ncnn C symbol.
var (

	ncnnNetCreate   func() uintptr                                          // ncnn_net_t ncnn_net_create()
	ncnnNetDestroy  func(net uintptr)                                       // void ncnn_net_destroy(ncnn_net_t)
	ncnnNetSetOpt   func(net, opt uintptr)                                  // void ncnn_net_set_option(ncnn_net_t, ncnn_option_t)
	ncnnNetLoadParam func(net uintptr, path *byte) int                      // int ncnn_net_load_param(ncnn_net_t, const char*)
	ncnnNetLoadModel func(net uintptr, path *byte) int                      // int ncnn_net_load_model(ncnn_net_t, const char*)

	ncnnOptCreate     func() uintptr                       // ncnn_option_t ncnn_option_create()
	ncnnOptDestroy    func(opt uintptr)                    // void ncnn_option_destroy(ncnn_option_t)
	ncnnOptSetThreads func(opt uintptr, n int32)           // void ncnn_option_set_num_threads(ncnn_option_t, int)
	ncnnOptSetFP16    func(opt uintptr, enable int32)      // void ncnn_option_set_use_fp16_arithmetic(ncnn_option_t, int)

	ncnnMatCreateExt3D func(w, h, c int32, data unsafe.Pointer, alc uintptr) uintptr // ncnn_mat_t ncnn_mat_create_external_3d(...)
	ncnnMatDestroy     func(mat uintptr)                                               // void ncnn_mat_destroy(ncnn_mat_t)
	ncnnMatGetW        func(mat uintptr) int32                                         // int ncnn_mat_get_w(ncnn_mat_t)
	ncnnMatGetH        func(mat uintptr) int32                                         // int ncnn_mat_get_h(ncnn_mat_t)
	ncnnMatGetData     func(mat uintptr) unsafe.Pointer                                // void* ncnn_mat_get_data(ncnn_mat_t)

	ncnnExtractorCreate  func(net uintptr) uintptr                                     // ncnn_extractor_t ncnn_extractor_create(ncnn_net_t)
	ncnnExtractorDestroy func(ex uintptr)                                              // void ncnn_extractor_destroy(ncnn_extractor_t)
	ncnnExtractorSetOpt  func(ex, opt uintptr)                                         // void ncnn_extractor_set_option(ncnn_extractor_t, ncnn_option_t)
	ncnnExtractorInput   func(ex uintptr, name *byte, mat uintptr) int                 // int ncnn_extractor_input(ncnn_extractor_t, const char*, ncnn_mat_t)
	ncnnExtractorExtract func(ex uintptr, name *byte, mat *uintptr) int                // int ncnn_extractor_extract(ncnn_extractor_t, const char*, ncnn_mat_t*)
)

// libLoaded is the runtime-resolved ncnn library handle + readiness flag.
var libLoaded bool

// loadLib dlopens libncnn and binds the C API. Idempotent; returns nil on
// success or if already loaded. Returns ErrNotAvailable if the library can't
// be found — the caller surfaces it so the engine stays gracefully off.
func loadLib() error {
	if libLoaded {
		return nil
	}
	path := findLib()
	if path == "" {
		return fmt.Errorf("%w: libncnn not found (run `brew install ncnn` or scripts/setup-ncnn.sh)", pkgocr.ErrNotAvailable)
	}
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("%w: dlopen libncnn: %v", pkgocr.ErrNotAvailable, err)
	}
	// Bind each ncnn C symbol to its Go function variable.
	binds := []struct {
		name string
		fn   any
	}{
		{"ncnn_net_create", &ncnnNetCreate},
		{"ncnn_net_destroy", &ncnnNetDestroy},
		{"ncnn_net_set_option", &ncnnNetSetOpt},
		{"ncnn_net_load_param", &ncnnNetLoadParam},
		{"ncnn_net_load_model", &ncnnNetLoadModel},
		{"ncnn_option_create", &ncnnOptCreate},
		{"ncnn_option_destroy", &ncnnOptDestroy},
		{"ncnn_option_set_num_threads", &ncnnOptSetThreads},
		{"ncnn_option_set_use_fp16_arithmetic", &ncnnOptSetFP16},
		{"ncnn_mat_create_external_3d", &ncnnMatCreateExt3D},
		{"ncnn_mat_destroy", &ncnnMatDestroy},
		{"ncnn_mat_get_w", &ncnnMatGetW},
		{"ncnn_mat_get_h", &ncnnMatGetH},
		{"ncnn_mat_get_data", &ncnnMatGetData},
		{"ncnn_extractor_create", &ncnnExtractorCreate},
		{"ncnn_extractor_destroy", &ncnnExtractorDestroy},
		{"ncnn_extractor_set_option", &ncnnExtractorSetOpt},
		{"ncnn_extractor_input", &ncnnExtractorInput},
		{"ncnn_extractor_extract", &ncnnExtractorExtract},
	}
	for _, b := range binds {
		sym, err := purego.Dlsym(handle, b.name)
		if err != nil {
			return fmt.Errorf("%w: dlsym %s: %v", pkgocr.ErrNotAvailable, b.name, err)
		}
		purego.RegisterFunc(b.fn, sym)
	}
	libLoaded = true
	return nil
}

// findLib returns the first existing libncnn candidate path, or "" if none.
func findLib() string {
	name := "libncnn.dylib"
	candidates := []string{
		"/opt/homebrew/lib/" + name, // Apple Silicon brew
		"/usr/local/lib/" + name,    // Intel brew / manual
	}
	if p := os.Getenv("PHONEFAST_NCNN_LIB"); p != "" {
		candidates = append([]string{p}, candidates...)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// NcnnRecognizer implements common.Recognizer using NCNN (runtime dlopen via
// purego). It runs rec one box at a time at a fixed input width.
//
// Detection is NOT part of this recognizer — the shared detect.Detector
// (Vision fast-path + onnx det fallback) feeds it cropped boxes. This frees
// ncnn from a hard Vision dependency: detection works wherever the onnx
// detector does; ncnn itself is currently macOS-only (purego dlopen of
// libncnn.dylib).
//
// Single-goroutine contract: not safe for concurrent use.
type NcnnRecognizer struct {
	net     uintptr // ncnn_net_t
	opt     uintptr // ncnn_option_t
	ctc     *common.CTCDecoder
	scratch []float32 // reused input buffer (RecMaxWidth*3*RecHeight floats)
	cIn     *byte     // cached "in0" blob name (NUL-terminated)
	cOut    *byte     // cached "out0" blob name
}

// NewEngine loads the NCNN library (runtime dlopen) and the recognition model
// (.param/.bin from PHONEFAST_NCNN_PARAM/BIN env vars), plus the shared
// detector, and returns them assembled into an engine.BaseEngine.
// useVision enables the macOS Vision detection fast-path (ignored when
// unavailable). Returns ErrNotAvailable if libncnn or the det model is missing
// or the rec model paths are unset — the engine then stays off, gracefully.
func NewEngine(useVision bool) (pkgocr.Engine, error) {
	// Detection layer (Vision fast-path + onnx det fallback). Loaded first so a
	// missing det model / ORT lib fails fast without having dlopen'd ncnn.
	det, err := detect.NewDetector(useVision)
	if err != nil {
		return nil, err
	}

	if err := loadLib(); err != nil {
		det.Close()
		return nil, err
	}
	paramPath := os.Getenv("PHONEFAST_NCNN_PARAM")
	binPath := os.Getenv("PHONEFAST_NCNN_BIN")
	if paramPath == "" || binPath == "" {
		det.Close()
		return nil, fmt.Errorf("%w: ncnn engine requires PHONEFAST_NCNN_PARAM and PHONEFAST_NCNN_BIN env vars (run scripts/setup-ncnn.sh)", pkgocr.ErrNotAvailable)
	}

	net := ncnnNetCreate()
	opt := ncnnOptCreate()
	ncnnOptSetThreads(opt, 8)
	ncnnOptSetFP16(opt, 1) // ARM NEON FP16 — speedup for rec conv/matmul, negligible accuracy loss
	ncnnNetSetOpt(net, opt)

	cParam := append([]byte(paramPath), 0)
	cBin := append([]byte(binPath), 0)
	if ret := ncnnNetLoadParam(net, &cParam[0]); ret != 0 {
		ncnnOptDestroy(opt)
		ncnnNetDestroy(net)
		det.Close()
		return nil, fmt.Errorf("%w: ncnn load param failed (%d)", pkgocr.ErrNotAvailable, ret)
	}
	if ret := ncnnNetLoadModel(net, &cBin[0]); ret != 0 {
		ncnnOptDestroy(opt)
		ncnnNetDestroy(net)
		det.Close()
		return nil, fmt.Errorf("%w: ncnn load model failed (%d)", pkgocr.ErrNotAvailable, ret)
	}

	in := append([]byte("in0"), 0)
	out := append([]byte("out0"), 0)
	r := &NcnnRecognizer{
		net:     net,
		opt:     opt,
		ctc:     common.NewCTCDecoder(),
		scratch: make([]float32, 3*common.RecHeight*common.RecMaxWidth),
		cIn:     &in[0],
		cOut:    &out[0],
	}
	return &engine.BaseEngine{Det: det, Rec: r}, nil
}

// RecognizeBoxes runs NCNN rec on each crop (one at a time, fixed width) and
// returns one BoxText per crop (empty Text allowed; BaseEngine filters).
func (r *NcnnRecognizer) RecognizeBoxes(crops []image.Image) ([]common.BoxText, error) {
	out := make([]common.BoxText, len(crops))
	for i, crop := range crops {
		// Preprocess into the reusable scratch buffer at the model's fixed
		// width (pnnx-converted model is shape-specialized to 320 — NCNN
		// aborts on a different width at runtime). Right-side zero padding to
		// 320 can let CTC decode a phantom tail char; known minor trade-off
		// vs the onnx dynamic-width batch path (see docs/DEV.md).
		common.RecPreprocessFixedInto(crop, common.RecMaxWidth, r.scratch)
		text, conf := r.recognizeBox()
		out[i] = common.BoxText{Text: text, Confidence: conf}
	}
	return out, nil
}

// recognizeBox runs NCNN rec on a single CHW [3, 48, RecMaxWidth] tensor in the
// scratch buffer. Reuses cached blob-name pointers — no per-call allocation.
func (r *NcnnRecognizer) recognizeBox() (string, float32) {
	inMat := ncnnMatCreateExt3D(
		int32(common.RecMaxWidth), int32(common.RecHeight), int32(3),
		unsafe.Pointer(&r.scratch[0]),
		0,
	)
	defer ncnnMatDestroy(inMat)

	ex := ncnnExtractorCreate(r.net)
	defer ncnnExtractorDestroy(ex)
	ncnnExtractorSetOpt(ex, r.opt)

	// Set input by blob name ("in0") and extract output by name ("out0").
	// extract_index(0) would return blob 0 = the input, not the output.
	if ret := ncnnExtractorInput(ex, r.cIn, inMat); ret != 0 {
		return "", 0
	}

	var outMat uintptr
	if ret := ncnnExtractorExtract(ex, r.cOut, &outMat); ret != 0 {
		return "", 0
	}
	if outMat == 0 {
		return "", 0
	}
	defer ncnnMatDestroy(outMat)

	// Softmax output is always 2-D for this model: [T, 6625] (w=6625, h=T)
	// where T = ceil(width/8). Read flat data; T follows from the element count.
	w := int(ncnnMatGetW(outMat))
	h := int(ncnnMatGetH(outMat))
	total := w * h
	if total == 0 || total%recNClass != 0 {
		return "", 0
	}
	T := total / recNClass

	outData := unsafe.Slice((*float32)(ncnnMatGetData(outMat)), total)
	// CTC decode reads from outData directly (NCNN-owned, freed by MatDestroy
	// above after return).
	return r.ctc.DecodeFlat(outData, 0, T, recNClass)
}

// Close releases the NCNN net + option. (The blob-name pointers are Go-owned
// byte slices, so no C free needed — GC handles them once the recognizer is
// dropped.)
func (r *NcnnRecognizer) Close() error {
	if r.opt != 0 {
		ncnnOptDestroy(r.opt)
	}
	if r.net != 0 {
		ncnnNetDestroy(r.net)
	}
	runtime.KeepAlive(r)
	return nil
}
