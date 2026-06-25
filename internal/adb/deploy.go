package adb

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gezihua123/phonefast/assets"
)

// ScrcpyServerInfo holds paths and metadata for scrcpy-server deployment.
type ScrcpyServerInfo struct {
	JarPath     string
	DevicePath  string
	ServerClass string
}

// DefaultScrcpyServer returns default scrcpy-server configuration.
func DefaultScrcpyServer() *ScrcpyServerInfo {
	return &ScrcpyServerInfo{
		JarPath:     findScrcpyJar(),
		DevicePath:  "/data/local/tmp/scrcpy-server.apk",
		ServerClass: "com.genymobile.scrcpy.Server",
	}
}

func findScrcpyJar() string {
	// 1. PHONE_FAST_HOME environment variable (highest priority).
	//    Lets users point at a shared phonefast home directory.
	if pfHome := os.Getenv("PHONE_FAST_HOME"); pfHome != "" {
		for _, p := range []string{
			filepath.Join(pfHome, "scrcpy-server.jar"),
			filepath.Join(pfHome, "android", "scrcpy-server.jar"),
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}

	// 2. Embedded jar (single-binary distribution).
	//    Extracted to a temp file and cached by size.
	if p := extractEmbeddedJar(); p != "" {
		return p
	}

	// 3. Executable-relative search: works in deployed binaries.
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		exeParent := filepath.Dir(exeDir)
		for _, p := range []string{
			filepath.Join(exeDir, "scrcpy-server.jar"),
			filepath.Join(exeDir, "android", "scrcpy-server.jar"),
			filepath.Join(exeParent, "android", "scrcpy-server.jar"),
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}

	// 4. Legacy well-known development paths (final fallback).
	home := os.Getenv("HOME")
	for _, p := range []string{
		filepath.Join(home, "Desktop", "phonefast", "android", "scrcpy-server.jar"),
		filepath.Join(home, "Desktop", "code", "scrcpy", "server", "build", "outputs", "apk", "release", "server-release-unsigned.apk"),
	} {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}

	return ""
}

// extractEmbeddedJar writes the embedded scrcpy-server.jar to a temp file
// and returns its path. If the temp file already exists with matching size,
// it is reused. Returns "" if no embedded jar is available.
func extractEmbeddedJar() string {
	if len(assets.ScrcpyJar) == 0 {
		return ""
	}
	cachedPath := filepath.Join(os.TempDir(), "phonefast-scrcpy-server.jar")
	// Reuse cached temp file if size matches embedded jar.
	if fi, err := os.Stat(cachedPath); err == nil && fi.Size() == int64(len(assets.ScrcpyJar)) {
		return cachedPath
	}
	if err := os.WriteFile(cachedPath, assets.ScrcpyJar, 0644); err != nil {
		return ""
	}
	return cachedPath
}

// Deploy pushes the scrcpy-server jar to the device.
func Deploy(serial string, info *ScrcpyServerInfo) error {
	if info.JarPath == "" {
		return fmt.Errorf("scrcpy-server.jar not found; build scrcpy first or place the jar in android/scrcpy-server.jar")
	}

	adbPath, err := findADB()
	if err != nil {
		return err
	}

	// Check if server already deployed and matches
	// For now, always push
	fmt.Fprintf(os.Stderr, "[phonefast] deploying %s (%d bytes) to device %s\n",
		filepath.Base(info.JarPath), fileSize(info.JarPath), serial)

	cmd := exec.Command(adbPath, "-s", serial, "push", info.JarPath, info.DevicePath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("push scrcpy-server: %w", err)
	}

	return nil
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// ScrcpyArgs holds the scrcpy server command-line arguments.
type ScrcpyArgs struct {
	Scid          int
	LogLevel      string
	MaxSize       int
	BitRate       int
	MaxFPS        int
	NoAudio       bool
	TunnelForward bool
}

// DefaultScrcpyArgs returns phonefast-optimized server args.
func DefaultScrcpyArgs() ScrcpyArgs {
	return ScrcpyArgs{
		Scid:          0x3f,
		LogLevel:      "info",
		MaxSize:       1080,
		BitRate:       8,
		MaxFPS:        15,
		NoAudio:       true,
		TunnelForward: true, // device acts as server, PC connects
	}
}

// scrcpyVersion returns the version string for the scrcpy server jar.
// Search order: .version sidecar → classes.dex scan → hardcoded fallback.
func scrcpyVersion(jarPath string) string {
	// 1. Sidecar file: scrcpy-server.version next to the jar
	sidecar := strings.TrimSuffix(jarPath, filepath.Ext(jarPath)) + ".version"
	if data, err := os.ReadFile(sidecar); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" {
			return v
		}
	}

	// 2. Embedded version file (single-binary distribution fallback).
	if v := strings.TrimSpace(string(assets.ScrcpyVersion)); v != "" {
		if looksLikeVersion(v) {
			return v
		}
	}

	// 3. Scan classes.dex for the version string embedded in the error message.
	//    scrcpy's Server.java stores: "The server version (%s) does not match..."
	//    where %s is BuildConfig.VERSION_NAME. We find the marker and read the
	//    semver string that precedes it.
	r, err := zip.OpenReader(jarPath)
	if err == nil {
		defer r.Close()
		for _, f := range r.File {
			if f.Name != "classes.dex" {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(rc)
			rc.Close()

			// The error string is: "The server version (3.3.4) does not match..."
			// Extract the version between "(" and ")" in this message.
			anchor := []byte("The server version (")
			idx := bytes.Index(data, anchor)
			if idx >= 0 {
				start := idx + len(anchor)
				end := bytes.IndexByte(data[start:], ')')
				if end > 0 && end < 20 {
					candidate := string(data[start : start+end])
					if looksLikeVersion(candidate) {
						return candidate
					}
				}
			}
			break
		}
	}

	// 4. Hardcoded fallback
	return "3.3.4"
}

// looksLikeVersion returns true if s looks like a semver "N.N.N" string.
func looksLikeVersion(s string) bool {
	if len(s) == 0 || len(s) > 20 {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 5 {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// StartServer launches scrcpy server on device in background via app_process.
func StartServer(serial string, info *ScrcpyServerInfo, args ScrcpyArgs) error {
	// Build server arg string: version first, then key=value pairs.
	// The version must match BuildConfig.VERSION_NAME in the server jar.
	version := scrcpyVersion(info.JarPath)
	serverParts := []string{version}

	// scrcpy parses scid with Integer.parseInt(value, 16) — must be hex string.
	serverParts = append(serverParts, fmt.Sprintf("scid=%x", args.Scid))
	serverParts = append(serverParts, "tunnel_forward=true")
	serverParts = append(serverParts, "control=true")
	serverParts = append(serverParts, "video=true")

	if args.LogLevel != "" {
		serverParts = append(serverParts, "log_level="+args.LogLevel)
	}
	if args.MaxSize > 0 {
		serverParts = append(serverParts, fmt.Sprintf("max_size=%d", args.MaxSize))
	}
	if args.BitRate > 0 {
		serverParts = append(serverParts, fmt.Sprintf("video_bit_rate=%d", args.BitRate))
	}
	if args.MaxFPS > 0 {
		serverParts = append(serverParts, fmt.Sprintf("max_fps=%d", args.MaxFPS))
	}
	if args.NoAudio {
		serverParts = append(serverParts, "audio=false")
	}
	// For phonefast, we read frames directly — no device meta needed
	serverParts = append(serverParts, "send_device_meta=false")
	serverParts = append(serverParts, "send_dummy_byte=true")
	serverParts = append(serverParts, "cleanup=false")

	argStr := strings.Join(serverParts, " ")

	// Build shell command: run app_process in background with nohup
	shellCmd := fmt.Sprintf(
		"CLASSPATH=%s nohup app_process / %s %s > /dev/null 2>&1 &",
		info.DevicePath,
		info.ServerClass,
		argStr,
	)

	fmt.Fprintf(os.Stderr, "[phonefast] starting server: %s\n", shellCmd)

	adbPath, _ := findADB()
	cmd := exec.Command(adbPath, "-s", serial, "shell", shellCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start server: %s: %w", string(out), err)
	}

	// Wait for server to bind sockets
	fmt.Fprintf(os.Stderr, "[phonefast] waiting for server to start...\n")
	time.Sleep(1 * time.Second)

	// Verify the process is running
	checkCmd := exec.Command(adbPath, "-s", serial, "shell", "pidof", "app_process")
	pidOut, _ := checkCmd.Output()
	pid := strings.TrimSpace(string(pidOut))
	if pid != "" {
		fmt.Fprintf(os.Stderr, "[phonefast] server running, pid=%s\n", pid)
	} else {
		fmt.Fprintf(os.Stderr, "[phonefast] warning: could not verify server pid\n")
	}

	return nil
}

// StopServer kills scrcpy server processes on the device.
func StopServer(serial string) error {
	adbPath, err := findADB()
	if err != nil {
		return err
	}

	// Kill scrcpy server specifically (don't kill all app_process)
	cmd := exec.Command(adbPath, "-s", serial, "shell",
		"pkill", "-f", "com.genymobile.scrcpy.Server")
	cmd.Run() // best-effort

	// Remove the forward to release the abstract socket
	exec.Command(adbPath, "-s", serial, "forward", "--remove-all").Run()

	// Wait for the old process/socket to fully release (poll for up to 2s)
	for i := 0; i < 10; i++ {
		time.Sleep(200 * time.Millisecond)
		check := exec.Command(adbPath, "-s", serial, "shell",
			"pgrep", "-f", "com.genymobile.scrcpy.Server")
		if check.Run() != nil {
			break // process no longer exists
		}
	}

	return nil
}
