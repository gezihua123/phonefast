// Package assets provides embedded scrcpy-server resources for single-binary distribution.
//
// The embedded files are build artifacts copied from android/ by the build scripts.
// Source of truth: android/scrcpy-server.jar (built by build-server.sh).
package assets

import _ "embed"

// ScrcpyJar holds the scrcpy-server.jar bytes, embedded at build time.
// Fallback path when no jar file is found on disk (single-binary distribution).
//
//go:embed scrcpy-server.jar
var ScrcpyJar []byte

// ScrcpyVersion holds the scrcpy-server version string (e.g. "3.3.4").
//
//go:embed scrcpy-server.version
var ScrcpyVersion []byte
