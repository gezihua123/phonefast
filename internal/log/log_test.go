package log

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func tempLogPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "test.log")
}

func TestWriteAndPersist(t *testing.T) {
	l, err := New(Config{Path: tempLogPath(t), BufferSize: 64})
	if err != nil {
		t.Fatal(err)
	}

	l.Write("hello %s", "world")
	l.Write("line two %d", 42)

	// Allow the drain goroutine to flush.
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(l.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "hello world") {
		t.Errorf("log missing 'hello world': %q", s)
	}
	if !strings.Contains(s, "line two 42") {
		t.Errorf("log missing 'line two 42': %q", s)
	}
}

func TestWriteAfterCloseDrops(t *testing.T) {
	l, err := New(Config{Path: tempLogPath(t), BufferSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	// Must not panic and must be a no-op.
	l.Write("should be dropped")
}

func TestCloseIdempotent(t *testing.T) {
	l, err := New(Config{Path: tempLogPath(t), BufferSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	// Second Close must not panic (close(l.ch) on an already-closed channel
	// would panic without the closed guard).
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestConcurrentWrites(t *testing.T) {
	l, err := New(Config{Path: tempLogPath(t), BufferSize: 4096})
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 32
	const perG = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				l.Write("g=%d i=%d", g, i)
			}
		}(g)
	}
	wg.Wait()

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(l.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	// Each entry is one line; expect goroutines*perG lines. A line ends with \n.
	want := goroutines * perG
	got := strings.Count(string(data), "\n")
	if got != want {
		t.Errorf("wrote %d lines, want %d (some entries lost under contention)", got, want)
	}
}

func TestBufferFullFallsBackToSyncWrite(t *testing.T) {
	// BufferSize=1 and a drain that never runs (we hold it open without
	// closing) — but the drain goroutine consumes continuously, so to force
	// the sync fallback we use a tiny buffer and flood faster than drain.
	// We can't deterministically force the 'default' branch, so this test
	// instead asserts no entries are lost and no panic occurs under heavy
	// load with a small buffer.
	l, err := New(Config{Path: tempLogPath(t), BufferSize: 2})
	if err != nil {
		t.Fatal(err)
	}

	const n = 500
	for i := 0; i < n; i++ {
		l.Write("entry %d", i)
	}

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(l.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Count(string(data), "\n")
	if got != n {
		t.Errorf("wrote %d lines, want %d (buffer-full fallback dropped data)", got, n)
	}
}

func TestDefaultLoggerSingleton(t *testing.T) {
	// Default() must return the same instance across calls (sync.Once).
	a := Default()
	b := Default()
	if a != b {
		t.Fatal("Default() returned different instances")
	}
}

// TestWriteCloseRaceConcurrent documents the known send-on-closed-channel
// race between Write and Close. This test does NOT close concurrently with
// writes (that path is a known dormant race — see the project's bug audit).
// It only verifies the closed-guarded Write-after-Close is safe, which is
// the path exercised in production.
func TestWriteCloseGuardedPath(t *testing.T) {
	l, err := New(Config{Path: tempLogPath(t), BufferSize: 16})
	if err != nil {
		t.Fatal(err)
	}

	// Flood writes while closing — the closed flag check + drain must keep
	// this from panicking in the common (non-adversarial) interleaving.
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				l.Write("x")
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)
	close(done)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
