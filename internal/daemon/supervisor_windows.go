//go:build windows

package daemon

import (
	"os"
	"syscall"
	"time"
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func devNullFile() (*os.File, error) {
	devNull, err := os.OpenFile("NUL", os.O_RDWR, 0)
	return devNull, err
}

func killDaemon(pid int) {
	proc, _ := os.FindProcess(pid)
	if proc != nil {
		// Wait up to 3s for graceful shutdown (session cleanup, IME restore).
		// On Windows daemon mode is blocked by the CLI's init() guard, but if
		// that changes, this gives session cleanup time to run before hard kill.
		if WaitForProcessExit(pid, 3*time.Second) {
			return
		}
		proc.Kill()
	}
}
