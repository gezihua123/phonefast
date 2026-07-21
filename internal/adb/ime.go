package adb

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gezihua123/phonefast/assets"
)

const (
	pfimePackage      = "com.phonefast.ime"
	pfimeService      = "com.phonefast.ime/.PFIME"
	pfimeDevicePath   = "/data/local/tmp/pfime.apk"
	pfimeBroadcastB64 = "com.phonefast.ime.INPUT_B64"
)

// EnsurePFIME checks whether the PhoneFast IME is installed and enabled.
// If not, it pushes and installs the embedded APK, then enables the IME.
// Returns the currently-active IME (before any changes) for later restore.
func EnsurePFIME(serial string) (originalIME string, err error) {
	originalIME, err = getCurrentIME(serial)
	if err != nil {
		return "", err
	}

	installed, _ := isPackageInstalled(serial, pfimePackage)
	if !installed {
		if err := installPfimeApk(serial); err != nil {
			return originalIME, err
		}
	}

	// Enable (idempotent)
	_, err = ADBShell(serial, "ime", "enable", pfimeService)
	if err != nil && !strings.Contains(err.Error(), "already enabled") {
		return originalIME, fmt.Errorf("ime enable: %w", err)
	}

	return originalIME, nil
}

// SetPFIME switches the active IME to the PhoneFast IME if not already active.
// Tries ime set first, falls back to settings put.
func SetPFIME(serial string) error {
	current, err := getCurrentIME(serial)
	if err == nil && current == pfimeService {
		return nil
	}

	// Enable (idempotent, ignore errors)
	ADBShell(serial, "ime", "enable", pfimeService)

	// Try ime set
	_, err = ADBShell(serial, "ime", "set", pfimeService)
	if err == nil {
		return nil
	}

	// Fallback: force-set via settings (Android 14 sometimes needs this)
	_, err2 := ADBShell(serial, "settings", "put", "secure", "default_input_method", pfimeService)
	if err2 != nil {
		return fmt.Errorf("ime set pfime: %v / %v", err, err2)
	}
	return nil
}

// RestoreIME restores the previously active IME.
func RestoreIME(serial, ime string) error {
	if ime == "" || ime == pfimeService {
		return nil
	}
	ADBShell(serial, "ime", "set", ime) // best-effort
	return nil
}

// TypeTextB64 sends text through the PFIME via base64 broadcast.
func TypeTextB64(serial, text string) error {
	b64 := base64.StdEncoding.EncodeToString([]byte(text))
	_, err := ADBShell(serial, "am", "broadcast", "-a", pfimeBroadcastB64, "--es", "msg", b64)
	if err != nil {
		return fmt.Errorf("pfime broadcast: %w", err)
	}
	return nil
}

// ── helpers ──

func getCurrentIME(serial string) (string, error) {
	out, err := ADBShell(serial, "settings", "get", "secure", "default_input_method")
	if err != nil {
		return "", fmt.Errorf("get current ime: %w", err)
	}
	return out, nil
}

func isPackageInstalled(serial, pkg string) (bool, error) {
	out, err := ADBShell(serial, "pm", "list", "packages", pkg)
	if err != nil {
		return false, nil
	}
	return strings.Contains(out, pkg), nil
}

func installPfimeApk(serial string) error {
	apkData := assets.PfimeApk
	var apkPath string

	if len(apkData) > 0 {
		tmpFile, err := os.CreateTemp("", "pfime-*.apk")
		if err != nil {
			return fmt.Errorf("create temp apk: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		if _, err := tmpFile.Write(apkData); err != nil {
			tmpFile.Close()
			return fmt.Errorf("write temp apk: %w", err)
		}
		tmpFile.Close()
		apkPath = tmpFile.Name()
	} else {
		apkPath = findPfimeApk()
		if apkPath == "" {
			return fmt.Errorf("pfime.apk not found (build with assets/ or set PHONE_FAST_HOME)")
		}
	}

	adbPath, _, err := ADB(serial)
	if err != nil {
		return err
	}

	// Push to device (adb push is not an adb shell command)
	pushCmd := exec.Command(adbPath, "-s", serial, "push", apkPath, pfimeDevicePath)
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("push pfime: %s: %w", string(out), err)
	}

	// Install (use -r to replace, -t for test sign)
	_, err = ADBShell(serial, "pm", "install", "-r", "-t", pfimeDevicePath)
	if err != nil {
		return fmt.Errorf("install pfime: %w", err)
	}

	return nil
}

func findPfimeApk() string {
	if pfHome := os.Getenv("PHONE_FAST_HOME"); pfHome != "" {
		p := filepath.Join(pfHome, "pfime.apk")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if exe, err := os.Executable(); err == nil {
		for _, p := range []string{
			filepath.Join(filepath.Dir(exe), "pfime.apk"),
			filepath.Join(filepath.Dir(filepath.Dir(exe)), "assets", "pfime.apk"),
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
