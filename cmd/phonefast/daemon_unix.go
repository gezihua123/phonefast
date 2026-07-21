//go:build !windows

package main

import (
	"os"
	"syscall"
	"time"

	"github.com/gezihua123/phonefast/internal/daemon"
)

func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

func daemonDevNull() (*os.File, error) {
	return os.OpenFile("/dev/null", os.O_RDWR, 0)
}

func daemonKill(pid int) {
	proc, _ := os.FindProcess(pid)
	if proc != nil {
		proc.Signal(syscall.SIGTERM)
		// Wait up to 3s for graceful shutdown (session cleanup, IME restore, etc.)
		if daemon.WaitForProcessExit(pid, 3*time.Second) {
			return
		}
		// Force kill if still alive
		proc.Signal(syscall.SIGKILL)
	}
}
