package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gezihua123/phonefast/internal/session"
)

// withFakeConnect swaps connectDeviceFn for fn and restores it on return.
// Tests must never hit real ADB, so the fake either errors or returns a
// zero-value *session.Session (dead: nil control conn) that has no
// background goroutines and whose Close() is never invoked by these tests.
func withFakeConnect(fn func(serial string, scid int) (*session.Session, error)) func() {
	prev := connectDeviceFn
	connectDeviceFn = fn
	return func() { connectDeviceFn = prev }
}

// newTestActor builds a bare actor with tiny cooldowns for fast tests.
// session starts nil so reconnect's Close() is a no-op and shutdown never
// hits ADB.
func newTestActor() *DeviceActor {
	return &DeviceActor{
		serial:            "test-device",
		scid:              0x3f,
		reqCh:             make(chan actorRequest),
		reconnectCooldown: 5 * time.Millisecond,
		restartBackoff:    2 * time.Millisecond,
	}
}

// --- tryReconnect throttling ---

func TestTryReconnectThrottles(t *testing.T) {
	calls := 0
	restore := withFakeConnect(func(string, int) (*session.Session, error) {
		calls++
		return nil, errors.New("device down")
	})
	defer restore()

	a := newTestActor()

	if a.tryReconnect() {
		t.Fatal("tryReconnect reported success on failing connect")
	}
	if a.tryReconnect() {
		t.Fatal("tryReconnect reported success on failing connect")
	}
	if calls != 1 {
		t.Fatalf("connect called %d times, want 1 (second should be throttled)", calls)
	}
}

func TestTryReconnectExpiresAfterCooldown(t *testing.T) {
	calls := 0
	restore := withFakeConnect(func(string, int) (*session.Session, error) {
		calls++
		return nil, errors.New("device down")
	})
	defer restore()

	a := newTestActor()

	a.tryReconnect() // calls == 1
	// Wait past the tiny cooldown so the next attempt is allowed.
	time.Sleep(20 * time.Millisecond)
	a.tryReconnect() // calls == 2
	if calls != 2 {
		t.Fatalf("connect called %d times, want 2 after cooldown elapsed", calls)
	}
}

func TestTryReconnectSuccessReturnsTrue(t *testing.T) {
	restore := withFakeConnect(func(string, int) (*session.Session, error) {
		return &session.Session{}, nil // dead-but-non-nil fake
	})
	defer restore()

	a := newTestActor()
	if !a.tryReconnect() {
		t.Fatal("tryReconnect returned false on successful connect")
	}
	if a.session == nil {
		t.Fatal("session not set after successful reconnect")
	}
}

// --- handleRequest reconnect/retry behavior ---

func TestHandleRequestDeadSessionTriggersReconnect(t *testing.T) {
	calls := 0
	restore := withFakeConnect(func(string, int) (*session.Session, error) {
		calls++
		return nil, errors.New("device down")
	})
	defer restore()

	a := newTestActor() // session nil → tap returns ErrNoDevice, session is "dead"

	replyCh := make(chan *Response, 1)
	a.handleRequest(actorRequest{req: &Request{Method: "tap", ID: 1, Params: []byte(`{"x":1,"y":2}`)}, replyCh: replyCh})

	resp := <-replyCh
	if resp.Error == nil {
		t.Fatal("expected error response for tap on nil session")
	}
	if calls != 1 {
		t.Fatalf("reconnect called %d times, want 1", calls)
	}
}

func TestHandleRequestThrottlesRepeatedReconnect(t *testing.T) {
	calls := 0
	restore := withFakeConnect(func(string, int) (*session.Session, error) {
		calls++
		return nil, errors.New("device down")
	})
	defer restore()

	a := newTestActor()

	// Two rapid taps on a nil session: only the first may reconnect.
	for i := 0; i < 2; i++ {
		replyCh := make(chan *Response, 1)
		a.handleRequest(actorRequest{req: &Request{Method: "tap", ID: int64(i + 1), Params: []byte(`{"x":1,"y":2}`)}, replyCh: replyCh})
		<-replyCh
	}
	if calls != 1 {
		t.Fatalf("reconnect called %d times, want 1 (second throttled)", calls)
	}
}

func TestHandleRequestStatusReplyFlow(t *testing.T) {
	// A zero session is "dead" but status() returns a valid (non-error)
	// response without touching the control socket, so no reconnect fires.
	a := newTestActor()
	a.session = &session.Session{}

	replyCh := make(chan *Response, 1)
	a.handleRequest(actorRequest{req: &Request{Method: "status", ID: 1}, replyCh: replyCh})

	select {
	case resp := <-replyCh:
		if resp.Error != nil {
			t.Fatalf("status returned error: %v", resp.Error)
		}
		if resp.ID != 1 {
			t.Fatalf("response ID = %d, want 1", resp.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("no reply within 1s")
	}
}

// --- run() lifecycle ---

func TestRunExitsOnCtxCancel(t *testing.T) {
	a := newTestActor() // nil session → no Close() / no ADB on shutdown

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan struct{})
	go func() {
		a.run(ctx, &wg)
		close(done)
	}()

	cancel()
	select {
	case <-done:
		// run() returned cleanly; wg.Done fired.
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not exit within 2s of ctx cancel")
	}
	wg.Wait()
}

func TestRunRestartsAfterPanic(t *testing.T) {
	// A nil req panics inside Dispatch (nil-pointer on req.Method). runLoop's
	// recover must catch it and run() must restart so a later, valid request
	// still gets answered — proving the actor self-heals.
	a := newTestActor()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go a.run(ctx, &wg)
	defer func() {
		cancel()
		wg.Wait()
	}()

	// Send a request with a nil *Request → panics in Dispatch → restart.
	// replyCh is buffered so the (never-sent) reply doesn't block the actor.
	a.reqCh <- actorRequest{req: nil, replyCh: make(chan *Response, 1)}

	// Allow time for panic + tiny backoff + restart.
	time.Sleep(50 * time.Millisecond)

	// A valid status request on a nil session returns a non-error response,
	// proving the event loop is alive again after the panic.
	replyCh := make(chan *Response, 1)
	a.reqCh <- actorRequest{req: &Request{Method: "status", ID: 7}, replyCh: replyCh}
	select {
	case resp := <-replyCh:
		if resp.ID != 7 {
			t.Fatalf("response ID = %d, want 7", resp.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("actor did not recover: no reply to post-panic request")
	}
}
