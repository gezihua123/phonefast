//go:build !windows

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAcquireLockExcludesSecondDaemon verifies the flock-based mutual
// exclusion that prevents two daemons from starting on the same PID file:
// the second acquireLock must fail while the first holds the lock, and must
// succeed after the first releases it. This is the mechanism that decides
// the "concurrent auto-start" race between two daemon processes.
func TestAcquireLockExcludesSecondDaemon(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "phonefast.pid")

	d1 := New(Config{})
	d1.pidFile = pidFile
	defer d1.releaseLock()

	if err := d1.acquireLock(); err != nil {
		t.Fatalf("first acquireLock: %v", err)
	}

	d2 := New(Config{})
	d2.pidFile = pidFile
	defer d2.releaseLock()

	err := d2.acquireLock()
	if err == nil {
		t.Fatal("second daemon acquired the lock on a PID file already held by the first")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("second acquireLock error = %q, want it to mention 'already running'", err.Error())
	}

	d1.releaseLock()

	if err := d2.acquireLock(); err != nil {
		t.Fatalf("acquireLock after release: %v", err)
	}
}

// TestStartListenFailureCleansUp covers Start's listen-failure path: when
// net.Listen fails, Start must run teardownActor (cancel ctx, release the
// flock) and leave no listener behind — a leaked lock would block every
// later daemon start on this pidfile.
func TestStartListenFailureCleansUp(t *testing.T) {
	dir := shortSocketDir(t)
	pidFile := filepath.Join(dir, "daemon.pid")
	// A socket path inside a nonexistent directory makes net.Listen fail
	// (Start removes stale socket FILES, but cannot create directories).
	socket := filepath.Join(dir, "missing", "daemon.sock")

	d := New(Config{})
	d.socketPath = socket
	d.pidFile = pidFile

	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("want listen error, got nil")
	}
	if !strings.Contains(err.Error(), "listen unix socket") {
		t.Errorf("err = %v, want it to mention 'listen unix socket'", err)
	}
	if d.listener != nil {
		t.Error("listener left set after failed Start")
	}

	// The flock must have been released: a second daemon can acquire it.
	d2 := New(Config{})
	d2.pidFile = pidFile
	defer d2.releaseLock()
	if err := d2.acquireLock(); err != nil {
		t.Fatalf("lock not released after failed Start: %v", err)
	}
}

// TestStartCancelCleansUp covers the graceful-shutdown path: cancelling ctx
// makes Start return nil and cleanup remove the socket + pidfile and release
// the flock.
func TestStartCancelCleansUp(t *testing.T) {
	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "daemon.sock")
	pidFile := filepath.Join(dir, "daemon.pid")

	d := New(Config{})
	d.socketPath = socket
	d.pidFile = pidFile

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Start(ctx) }()

	// Wait for the socket to appear (Start is serving).
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The pidfile records the running daemon's pid (this process).
	if pid, err := ReadPID(pidFile); err != nil || pid != os.Getpid() {
		t.Errorf("ReadPID = (%d, %v), want (%d, nil)", pid, err, os.Getpid())
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error on cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within 5s of cancel — serve goroutine or wg.Wait leaked")
	}

	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Error("socket file not removed by cleanup")
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("pidfile not removed by cleanup")
	}
	// The flock was released: a new daemon can start on the same pidfile.
	d2 := New(Config{})
	d2.pidFile = pidFile
	defer d2.releaseLock()
	if err := d2.acquireLock(); err != nil {
		t.Fatalf("lock not released after shutdown: %v", err)
	}
}

// TestStartPprofShutdown verifies cleanup shuts the pprof server down when
// PHONEFAST_PPROF is set (a leaked pprofServer would hold its port past
// daemon exit).
func TestStartPprofShutdown(t *testing.T) {
	t.Setenv("PHONEFAST_PPROF", "127.0.0.1:0") // :0 = any free port

	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "daemon.sock")
	pidFile := filepath.Join(dir, "daemon.pid")

	d := New(Config{})
	d.socketPath = socket
	d.pidFile = pidFile

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Start(ctx) }()

	// Wait for the socket: pprofServer is assigned early in Start (before
	// acquireLock), so it is set by the time the daemon is serving.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error on cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return within 5s of cancel")
	}
	// Read after Start's return is synchronized via errCh.
	if d.pprofServer != nil {
		t.Error("pprof server not shut down by cleanup")
	}
}
