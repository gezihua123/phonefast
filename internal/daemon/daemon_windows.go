//go:build windows

package daemon

// Windows stubs — daemon is not supported on Windows (no Unix sockets/flock).
// The cmd layer already gates daemon mode on Windows.

func (d *Daemon) acquireLock() error { return nil }

func (d *Daemon) releaseLock() {}
