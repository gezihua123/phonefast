//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
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
