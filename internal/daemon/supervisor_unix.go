//go:build !windows

package daemon

import (
	"os"
	"syscall"
	"time"
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

func devNullFile() (*os.File, error) {
	return os.OpenFile("/dev/null", os.O_RDWR, 0)
}

func killDaemon(pid int) {
	proc, _ := os.FindProcess(pid)
	if proc != nil {
		proc.Signal(syscall.SIGTERM)
		// Wait up to 3s for graceful shutdown (session cleanup, IME restore, etc.)
		if WaitForProcessExit(pid, 3*time.Second) {
			return
		}
		// Force kill if still alive
		proc.Signal(syscall.SIGKILL)
	}
}
