package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWriteReadPIDRoundTrip verifies WritePID persists the current process
// PID and ReadPID parses it back.
func TestWriteReadPIDRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phonefast.pid")
	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("ReadPID = %d, want %d", pid, os.Getpid())
	}
}

// TestReadPIDMissingFile verifies the (0, nil) contract for a nonexistent
// PID file — callers treat 0 as "no daemon running".
func TestReadPIDMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.pid")
	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID on missing file = (%d, %v), want (0, nil)", pid, err)
	}
	if pid != 0 {
		t.Errorf("ReadPID on missing file = %d, want 0", pid)
	}
}

// TestReadPIDGarbage verifies non-numeric content is rejected with an error
// (not silently parsed as 0).
func TestReadPIDGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.pid")
	if err := os.WriteFile(path, []byte("not-a-pid\n"), 0644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	if _, err := ReadPID(path); err == nil {
		t.Error("ReadPID on garbage content returned nil error")
	}
}

// TestRemovePIDMissingFile verifies RemovePID is a no-op (nil error) when the
// file does not exist.
func TestRemovePIDMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.pid")
	if err := RemovePID(path); err != nil {
		t.Errorf("RemovePID on missing file = %v, want nil", err)
	}
}

// TestRemovePIDRemovesFile verifies RemovePID deletes an existing PID file.
func TestRemovePIDRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phonefast.pid")
	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := RemovePID(path); err != nil {
		t.Fatalf("RemovePID: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("PID file still exists after RemovePID (stat err = %v)", err)
	}
}

// TestIsProcessAlive verifies the pid<=0 guard and the signal-0 existence
// check for self and a nonexistent PID.
func TestIsProcessAlive(t *testing.T) {
	tests := []struct {
		pid  int
		want bool
		desc string
	}{
		{os.Getpid(), true, "self"},
		{0, false, "zero pid"},
		{-1, false, "negative pid"},
		{999999, false, "nonexistent pid"},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			if got := IsProcessAlive(tc.pid); got != tc.want {
				t.Errorf("IsProcessAlive(%d) = %v, want %v", tc.pid, got, tc.want)
			}
		})
	}
}

// TestWaitForProcessExit verifies both the immediate-exit and the timeout
// paths of the polling loop.
func TestWaitForProcessExit(t *testing.T) {
	// Dead PID: must return true without sleeping out the full timeout.
	start := time.Now()
	if !WaitForProcessExit(999999, 2*time.Second) {
		t.Error("WaitForProcessExit(dead pid) = false, want true")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("WaitForProcessExit(dead pid) took %v, want immediate return", elapsed)
	}

	// Live PID (self): must hit the timeout and return false.
	if WaitForProcessExit(os.Getpid(), 50*time.Millisecond) {
		t.Error("WaitForProcessExit(self) = true, want false (process is alive)")
	}
}
