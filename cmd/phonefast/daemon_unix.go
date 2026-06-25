//go:build !windows

package main

import (
	"os"
	"syscall"
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
		timeSleep(500)
		proc.Signal(syscall.SIGKILL)
	}
}
