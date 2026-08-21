package adb

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gezihua123/phonefast/assets"
)

const (
	// PFIMEPackage is the PhoneFast IME application ID. Exported for
	// callers that probe the device (e.g. session's post-switch pidof poll).
	PFIMEPackage      = "com.phonefast.ime"
	pfimeService      = "com.phonefast.ime/.PFIME"
	pfimeDevicePath   = "/data/local/tmp/pfime.apk"
	pfimeBroadcastB64 = "com.phonefast.ime.INPUT_B64"
)

// imeShellTimeout bounds the quick IME/settings/broadcast commands on the
// type path. Each of these normally completes in well under a second; the
// 10s bound exists purely so a hung adb server fails fast instead of
// wedging the daemon's device actor past its 60s reply window.
const imeShellTimeout = 10 * time.Second

// installShellTimeout bounds the pm install of the PFIME APK. Install is the
// one legitimately slow command on this path (cold pm on low-end devices).
const installShellTimeout = 60 * time.Second

// EnsurePFIME checks whether the PhoneFast IME is installed and enabled.
// If not, it pushes and installs the embedded APK, then enables the IME.
// Returns the currently-active IME (before any changes) for later restore.
func EnsurePFIME(serial string) (originalIME string, err error) {
	originalIME, err = getCurrentIME(serial)
	if err != nil {
		return "", err
	}

	installed, err := isPackageInstalled(serial, PFIMEPackage)
	if err != nil {
		// A hung/slow pm must not be misread as "not installed": installing
		// on a hung transport routes into an unbounded push and wedges the
		// actor (see installPfimeApk). Fail closed — connect treats this as
		// non-fatal, and the type path surfaces it if PFIME is truly missing.
		return originalIME, fmt.Errorf("check pfime installed: %w", err)
	}
	if !installed {
		if err := installPfimeApk(serial); err != nil {
			return originalIME, err
		}
	}

	// Enable (idempotent)
	_, err = ADBShellTimeout(serial, imeShellTimeout, "ime", "enable", pfimeService)
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
	ADBShellTimeout(serial, imeShellTimeout, "ime", "enable", pfimeService)

	// Try ime set
	_, err = ADBShellTimeout(serial, imeShellTimeout, "ime", "set", pfimeService)
	if err == nil {
		return nil
	}

	// Fallback: force-set via settings (Android 14 sometimes needs this)
	_, err2 := ADBShellTimeout(serial, imeShellTimeout, "settings", "put", "secure", "default_input_method", pfimeService)
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
	ADBShellTimeout(serial, imeShellTimeout, "ime", "set", ime) // best-effort
	return nil
}

// TypeTextB64 sends text through the PFIME via base64 broadcast.
//
// The 10s bound is the anti-wedge measure: a broadcast that hangs is the one
// scenario where the request may still have been delivered (timeout kills
// adb after the device processed the broadcast), so callers must not retry
// blindly. The error wraps context.DeadlineExceeded on timeout.
func TypeTextB64(serial, text string) error {
	b64 := base64.StdEncoding.EncodeToString([]byte(text))
	_, err := ADBShellTimeout(serial, imeShellTimeout, "am", "broadcast", "-a", pfimeBroadcastB64, "--es", "msg", b64)
	if err != nil {
		return fmt.Errorf("pfime broadcast: %w", err)
	}
	return nil
}

// ── helpers ──

func getCurrentIME(serial string) (string, error) {
	out, err := ADBShellTimeout(serial, imeShellTimeout, "settings", "get", "secure", "default_input_method")
	if err != nil {
		return "", fmt.Errorf("get current ime: %w", err)
	}
	return out, nil
}

func isPackageInstalled(serial, pkg string) (bool, error) {
	out, err := ADBShellTimeout(serial, imeShellTimeout, "pm", "list", "packages", pkg)
	if err != nil {
		// Propagate instead of swallowing: a DeadlineExceeded here means a
		// hung transport, and "not installed" would trigger a re-push/reinstall
		// on every Connect — or worse, route the hang into the push.
		return false, fmt.Errorf("pm list packages: %w", err)
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

	// Push to device (adb push is not an adb shell command). Bounded like
	// every other quick command on this path: an unbounded push on a hung
	// adb server wedges the daemon's device actor synchronously (connect),
	// which violates the anti-wedge invariant ADBShellTimeout provides.
	pushCtx, cancel := context.WithTimeout(context.Background(), imeShellTimeout)
	defer cancel()
	pushCmd := exec.CommandContext(pushCtx, adbPath, "-s", serial, "push", apkPath, pfimeDevicePath)
	if out, err := pushCmd.CombinedOutput(); err != nil {
		if pushCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("push pfime: %w", context.DeadlineExceeded)
		}
		return fmt.Errorf("push pfime: %s: %w", string(out), err)
	}

	// Install (use -r to replace, -t for test sign). The only command on the
	// IME path that may legitimately run long (cold pm on low-end devices),
	// hence its own generous bound.
	_, err = ADBShellTimeout(serial, installShellTimeout, "pm", "install", "-r", "-t", pfimeDevicePath)
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
