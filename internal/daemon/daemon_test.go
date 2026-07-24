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
