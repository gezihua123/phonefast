//go:build !windows

package detect

import (
	"fmt"
	"os"
	"runtime"

	ocrassets "github.com/gezihua123/phonefast/assets/ocr"
)

// systemLibPaths returns platform-specific candidate paths for the ONNX
// Runtime shared library. (This file is !windows; Windows OCR is handled
// entirely by the onnx engine's Windows stub, so no Windows case here.)
func systemLibPaths() []string {
	libName := runtimeLibName()
	var dirs []string
	switch runtime.GOOS {
	case "darwin":
		dirs = []string{
			"/opt/homebrew/lib", // Apple Silicon brew
			"/usr/local/lib",    // Intel brew / manual
			"/usr/lib",
		}
	case "linux":
		dirs = []string{"/usr/local/lib", "/usr/lib", "/lib"}
	}
	var paths []string
	for _, d := range dirs {
		paths = append(paths, d+"/"+libName)
	}
	return paths
}

func runtimeLibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libonnxruntime.dylib"
	case "linux":
		return "libonnxruntime.so"
	default:
		return "libonnxruntime.so"
	}
}

// loadRuntimeLib locates the ONNX Runtime shared library.
// Returns (path, isTemp, err): isTemp is true when the library was extracted
// from the embed (a temp file the caller must track for cleanup); false for a
// system install. Tries: (1) embedded library extracted to temp, (2) system paths.
func loadRuntimeLib() (path string, isTemp bool, err error) {
	if len(ocrassets.RuntimeLib) > 0 {
		path, err = WriteTempFile(ocrassets.RuntimeLib, "libonnxruntime-*.so")
		if err != nil {
			return "", false, fmt.Errorf("create temp lib: %w", err)
		}
		return path, true, nil
	}
	path, err = findSystemLib()
	return path, false, err
}


func findSystemLib() (string, error) {
	for _, p := range systemLibPaths() {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("onnxruntime library not found in system paths; install it (brew install onnxruntime) or bundle via build.sh")
}

// WriteTempFile writes bytes to a temp file and returns the path.
// Exported so the onnx package can reuse it (it no longer has its own copy).
func WriteTempFile(data []byte, pattern string) (string, error) {
	tmpFile, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()
	if _, err := tmpFile.Write(data); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}
	return tmpFile.Name(), nil
}
