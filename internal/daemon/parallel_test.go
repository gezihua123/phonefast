package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gezihua123/phonefast/internal/session"
)

// --- Test helpers ---

// fakeConnect counts invocations and returns a session with the given serial.
// delay simulates the ~2.5s scrcpy handshake so concurrent callers actually
// overlap; use a small value (e.g. 5ms) to keep tests fast.
func fakeConnect(serial string, delay time.Duration, calls *int32) func(string, int) (*session.Session, error) {
	return func(s string, scid int) (*session.Session, error) {
		atomic.AddInt32(calls, 1)
		if delay > 0 {
			time.Sleep(delay)
		}
		return &session.Session{Serial: serial}, nil
	}
}

// --- Concurrent same-device connect serialization ---

// TestParallelSameDeviceSerialized verifies that N concurrent getOrCreateActor
// calls for the SAME device result in exactly ONE connect (the per-serial
// mutex serializes them; losers find the winner via the re-check).
func TestParallelSameDeviceSerialized(t *testing.T) {
	var calls int32
	restore := withFakeConnect(fakeConnect("dev-A", 10*time.Millisecond, &calls))
	defer restore()

	d := New(Config{})
	d.ctx, d.cancel = testCtx(t)

	const N = 8
	var wg sync.WaitGroup
	actors := make([]*DeviceActor, N)
	errs := make([]error, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			a, err := d.getOrCreateActor("dev-A")
			actors[idx] = a
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("connect called %d times for same device, want 1", n)
	}
	for i := 0; i < N; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d got error: %v", i, errs[i])
		}
		if actors[i] == nil {
			t.Fatalf("caller %d got nil actor", i)
		}
		if actors[i] != actors[0] {
			t.Fatalf("caller %d got different actor pointer (want shared)", i)
		}
	}
	if len(d.devices) != 1 {
		t.Fatalf("devices map has %d entries, want 1", len(d.devices))
	}
}

// --- Concurrent different-device connect parallelism ---

// TestParallelDifferentDevicesConnectInParallel verifies that N concurrent
// getOrCreateActor calls for DIFFERENT devices proceed in parallel (no shared
// lock between them).
func TestParallelDifferentDevicesConnectInParallel(t *testing.T) {
	var calls int32
	// Each connect takes 20ms. If serialized, 4 devices = 80ms. If parallel,
	// wall clock should be ~20ms + scheduling overhead.
	restore := withFakeConnect(func(s string, scid int) (*session.Session, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond)
		return &session.Session{Serial: s}, nil
	})
	defer restore()

	d := New(Config{})
	d.ctx, d.cancel = testCtx(t)

	const N = 4
	serials := []string{"dev-0", "dev-1", "dev-2", "dev-3"}
	var wg sync.WaitGroup
	start := time.Now()

	for _, s := range serials {
		wg.Add(1)
		go func(serial string) {
			defer wg.Done()
			if _, err := d.getOrCreateActor(serial); err != nil {
				t.Errorf("getOrCreateActor(%s): %v", serial, err)
			}
		}(s)
	}
	wg.Wait()
	elapsed := time.Since(start)

	if n := atomic.LoadInt32(&calls); n != N {
		t.Fatalf("connect called %d times, want %d", n, N)
	}
	if len(d.devices) != N {
		t.Fatalf("devices map has %d entries, want %d", len(d.devices), N)
	}
	// Parallel: ~20ms. Serial: ~80ms. Allow generous margin for CI.
	if elapsed > 60*time.Millisecond {
		t.Fatalf("connects took %v — appears serialized (want parallel <60ms)", elapsed)
	}
}

// --- Different caller serials resolving to same device ---

// TestParallelDifferentCallerSerialsSameDevice verifies the double-check in
// getOrCreateActor: two callers with different serial params that resolve to
// the same device must NOT create duplicate actors.
func TestParallelDifferentCallerSerialsSameDevice(t *testing.T) {
	var calls int32
	// Both "" and "dev-X" resolve to "dev-X".
	restore := withFakeConnect(func(s string, scid int) (*session.Session, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(5 * time.Millisecond)
		resolved := s
		if resolved == "" {
			resolved = "dev-X" // auto-detect
		}
		return &session.Session{Serial: resolved}, nil
	})
	defer restore()

	d := New(Config{})
	d.ctx, d.cancel = testCtx(t)

	var wg sync.WaitGroup
	actors := make([]*DeviceActor, 2)

	wg.Add(2)
	go func() { defer wg.Done(); a, _ := d.getOrCreateActor(""); actors[0] = a }()
	go func() { defer wg.Done(); a, _ := d.getOrCreateActor("dev-X"); actors[1] = a }()
	wg.Wait()

	// Both may have called connect (different per-serial mutexes), but only one
	// actor must survive in the map. The double-check discards the duplicate.
	if len(d.devices) != 1 {
		t.Fatalf("devices map has %d entries, want 1 (double-check failed)", len(d.devices))
	}
	if actors[0] != actors[1] {
		t.Fatal("two different actors returned for same device")
	}
}

// --- Actor lookup (direct map access) ---

// TestActorLookup verifies the devices map lookup used by getOrCreateActor.
// findActorLocked was inlined in the simplify pass; these tests verify the
// underlying map operations directly.
func TestActorLookup(t *testing.T) {
	d := New(Config{})
	a := &DeviceActor{serial: "dev-A", reqCh: make(chan actorRequest)}
	d.devices = map[string]*DeviceActor{"dev-A": a}

	if got, ok := d.devices["dev-A"]; !ok || got != a {
		t.Fatalf("direct lookup failed, got %v", got)
	}
	if _, ok := d.devices["nonexistent"]; ok {
		t.Fatal("nonexistent key should not exist")
	}
}

// --- snapshotDaemon consistency ---

// TestSnapshotDaemonConsistency verifies that snapshotDaemon returns
// consistent data (serials and connected from the same critical section).
func TestSnapshotDaemonConsistency(t *testing.T) {
	d := New(Config{})
	a1 := &DeviceActor{serial: "dev-A", reqCh: make(chan actorRequest)}
	a1.status.Store(&ActorStatus{Connected: true, Serial: "dev-A", DeviceWidth: 1080, DeviceHeight: 2400})
	a2 := &DeviceActor{serial: "dev-B", reqCh: make(chan actorRequest)}
	a2.status.Store(&ActorStatus{Connected: false, Serial: "dev-B"})
	a3 := &DeviceActor{serial: "dev-C", reqCh: make(chan actorRequest)}
	// a3 has no status (edge case)

	d.devices = map[string]*DeviceActor{
		"dev-A": a1,
		"dev-B": a2,
		"dev-C": a3,
	}

	snap := d.snapshotDaemon()

	if len(snap.serials) != 3 {
		t.Fatalf("serials has %d entries, want 3", len(snap.serials))
	}
	if len(snap.connected) != 1 {
		t.Fatalf("connected has %d entries, want 1", len(snap.connected))
	}
	if snap.connected[0].Serial != "dev-A" {
		t.Fatalf("connected[0].Serial = %q, want %q", snap.connected[0].Serial, "dev-A")
	}
	// serials must be sorted
	for i := 1; i < len(snap.serials); i++ {
		if snap.serials[i] < snap.serials[i-1] {
			t.Fatalf("serials not sorted: %v", snap.serials)
		}
	}
}

// --- testCtx creates a cancellable context for tests ---

func testCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx, cancel
}
