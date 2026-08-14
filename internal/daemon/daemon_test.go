package daemon

import "testing"

// TestStatusOnUnstartedDaemon ensures Status() is safe to call on a Daemon
// that never started (no devices map, no actor). This guards the
// two-value type assertion on actor.status and the map lookup.
func TestStatusOnUnstartedDaemon(t *testing.T) {
	d := New(Config{})

	// No devices registered — must not panic.
	s := d.Status()
	if s.Connected {
		t.Fatal("unstarted daemon reports connected")
	}
	if s.SocketPath == "" {
		t.Fatal("socket path not populated")
	}
	if s.Pid == 0 {
		t.Fatal("pid not populated")
	}
}

// TestStatusWithNilStatusValue constructs an actor whose status atomic.Value
// was never Store'd (simulating a future "register-then-connect-async" path)
// and asserts Status() degrades gracefully instead of panicking on the
// single-value type assertion.
func TestStatusWithNilStatusValue(t *testing.T) {
	d := New(Config{})
	// Hand-register an actor that never called updateStatus.
	a := &DeviceActor{serial: "fake-serial", reqCh: make(chan actorRequest)}
	d.devices = map[string]*DeviceActor{"fake-serial": a}

	// Must not panic even though a.status was never Stored.
	s := d.Status()
	if s.Connected {
		t.Fatal("actor with no status snapshot reports connected")
	}
}

// TestRemoveDeviceSuccess verifies removeDevice's success path drains the
// full device lifecycle: the actor's per-actor context is cancelled (so its
// run loop exits), the scid is released back to the allocator (a re-Alloc
// hands it out again), and both the devices and connectMu map entries are
// removed. A leak in any of the three would be caught here.
func TestRemoveDeviceSuccess(t *testing.T) {
	d := newTestDaemon(t)
	d.connectFn = func(s string, scid int) (Device, error) { return newFakeDevice(s), nil }

	actor, err := d.getOrCreateActor("dev-A")
	if err != nil {
		t.Fatalf("getOrCreateActor: %v", err)
	}
	scid := actor.scid

	if err := d.removeDevice("dev-A"); err != nil {
		t.Fatalf("removeDevice: %v", err)
	}

	d.mu.RLock()
	_, stillMapped := d.devices["dev-A"]
	d.mu.RUnlock()
	if stillMapped {
		t.Error("devices map still contains dev-A after removeDevice")
	}

	d.connectMuMu.Lock()
	_, muMapped := d.connectMu["dev-A"]
	d.connectMuMu.Unlock()
	if muMapped {
		t.Error("connectMu still contains dev-A after removeDevice")
	}

	// Context cancellation is synchronous, so this is immediately observable.
	if actor.ctx == nil || actor.ctx.Err() == nil {
		t.Error("actor context not cancelled — removeDevice did not stop the actor")
	}

	// The released scid must be allocatable again. dev-A held the allocator's
	// first (default) scid, so a fresh Alloc returns that same value.
	got, err := d.scidAlloc.Alloc()
	if err != nil {
		t.Fatalf("Alloc after release: %v", err)
	}
	if got != scid {
		t.Errorf("re-Alloc = %#x, want the released scid %#x", got, scid)
	}
}
