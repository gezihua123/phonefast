// Package adb provides ADB device discovery and scrcpy-server deployment.
package adb

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Device represents a connected Android device.
type Device struct {
	Serial string
	Model  string
	Status string // "device", "offline", "unauthorized"
}

// ListDevices returns all connected Android devices via `adb devices`.
func ListDevices() ([]Device, error) {
	adb, err := findADB()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(adb, "devices", "-l")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("adb devices: %w", err)
	}

	var devices []Device
	lines := strings.Split(string(out), "\n")
	for _, line := range lines[1:] { // skip "List of devices attached"
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		d := Device{
			Serial: fields[0],
			Status: fields[1],
		}

		// parse model from fields like "model:XXX"
		for _, f := range fields[2:] {
			if strings.HasPrefix(f, "model:") {
				d.Model = strings.TrimPrefix(f, "model:")
				break
			}
		}

		if d.Status == "device" {
			devices = append(devices, d)
		}
	}

	return devices, nil
}

// GetDeviceInfo returns model info for a device.
func GetDeviceInfo(serial string) (string, error) {
	adb, err := findADB()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(adb, "-s", serial, "shell", "getprop", "ro.product.model")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("get device info: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// WaitForDevice blocks until the device is online.
func WaitForDevice(serial string) error {
	adb, err := findADB()
	if err != nil {
		return err
	}

	cmd := exec.Command(adb, "-s", serial, "wait-for-device")
	return cmd.Run()
}

// ADB returns the adb path and command prefix for a device.
func ADB(serial string) (string, []string, error) {
	adb, err := findADB()
	if err != nil {
		return "", nil, err
	}

	if serial == "" {
		return adb, []string{adb}, nil
	}
	return adb, []string{adb, "-s", serial}, nil
}

// ADBShell runs an adb shell command.
func ADBShell(serial string, args ...string) (string, error) {
	adb, err := findADB()
	if err != nil {
		return "", err
	}

	shellArgs := []string{"-s", serial, "shell"}
	shellArgs = append(shellArgs, args...)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(adb, shellArgs...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("adb shell %v: %w (stderr: %s)", args, err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// findADB locates the adb binary.
func findADB() (string, error) {
	// Try ANDROID_HOME first
	if home := os.Getenv("ANDROID_HOME"); home != "" {
		candidate := filepath.Join(home, "platform-tools", "adb")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// Try ANDROID_SDK_ROOT
	if home := os.Getenv("ANDROID_SDK_ROOT"); home != "" {
		candidate := filepath.Join(home, "platform-tools", "adb")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// Try PATH
	path, err := exec.LookPath("adb")
	if err == nil {
		return path, nil
	}

	return "", fmt.Errorf("adb not found: set ANDROID_HOME or add adb to PATH")
}
