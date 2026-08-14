//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

// acquireLock opens the daemon's PID file and acquires an exclusive
// non-blocking flock on it. The PID file serves double duty: it records the
// daemon's PID for status checks AND acts as the mutual-exclusion lock that
// prevents a second daemon from starting. Returns an error if another daemon
// already holds the lock. The file handle is stored in d.lockFile and must
// remain open for the daemon's lifetime — the flock is released on process
// exit or cleanup().
func (d *Daemon) acquireLock() error {
	f, err := os.OpenFile(d.pidFile, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open pid file for lock: %w", err)
	}
	// LOCK_EX|LOCK_NB: fail immediately if another daemon holds the lock.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return fmt.Errorf("another daemon is already running (pid file locked: %s)", d.pidFile)
	}
	d.lockFile = f
	return nil
}

// releaseLock closes the PID file, which releases the flock. Idempotent.
func (d *Daemon) releaseLock() {
	if d.lockFile != nil {
		d.lockFile.Close()
		d.lockFile = nil
	}
}
