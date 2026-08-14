//go:build windows

package main

import (
	"fmt"
	"os"
)

// On Windows, daemon mode is not fully supported yet.
func init() {
	if len(os.Args) >= 2 && os.Args[1] == "daemon" {
		fmt.Fprintln(os.Stderr, "Error: daemon mode is not supported on Windows")
		fmt.Fprintln(os.Stderr, "Use direct mode instead: phonefast <command>")
		os.Exit(1)
	}
}
