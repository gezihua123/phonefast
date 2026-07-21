//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/gezihua123/phonefast/internal/daemon"
)

func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func daemonDevNull() (*os.File, error) {
	devNull, err := os.OpenFile("NUL", os.O_RDWR, 0)
	return devNull, err
}

func daemonKill(pid int) {
	proc, _ := os.FindProcess(pid)
	if proc != nil {
		// Wait up to 3s for graceful shutdown (session cleanup, IME restore).
		// On Windows daemon mode is blocked by init(), but if that changes,
		// this gives session cleanup time to run before hard kill.
		if daemon.WaitForProcessExit(pid, 3*time.Second) {
			return
		}
		proc.Kill()
	}
}

// On Windows, daemon mode is not fully supported yet.
func init() {
	if len(os.Args) >= 2 && os.Args[1] == "daemon" {
		fmt.Fprintln(os.Stderr, "Error: daemon mode is not supported on Windows")
		fmt.Fprintln(os.Stderr, "Use direct mode instead: phonefast <command>")
		os.Exit(1)
	}
}
