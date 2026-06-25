package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// WritePID atomically writes the current PID to path via temp file + rename.
func WritePID(path string) error {
	tmpPath := path + ".tmp"
	content := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename pid file: %w", err)
	}
	return nil
}

// ReadPID reads a PID from path. Returns 0 if the file doesn't exist.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse pid: %w", err)
	}
	return pid, nil
}

// RemovePID removes the PID file. No-op if it doesn't exist.
func RemovePID(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove pid file: %w", err)
	}
	return nil
}

// IsProcessAlive checks if a process with the given PID exists.
// Uses signal 0 (null signal) which only checks permissions and existence.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// signal 0: check if we can signal the process (existence + permission)
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
