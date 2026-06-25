package daemon

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gezihua123/phonefast/internal/adb"
	phonelog "github.com/gezihua123/phonefast/internal/log"
	"github.com/gezihua123/phonefast/internal/session"
)

// ActorStatus is a lightweight snapshot of the actor's current state,
// updated by the actor goroutine after connect/reconnect.
// Daemon.Status() reads this via atomic.Value without blocking the actor.
type ActorStatus struct {
	Connected    bool   `json:"connected"`
	Serial       string `json:"serial,omitempty"`
	DeviceWidth  int    `json:"device_width,omitempty"`
	DeviceHeight int    `json:"device_height,omitempty"`
	ControlAvail bool   `json:"control_available"`
	UIAvail      bool   `json:"ui_available"`
}

// actorRequest is sent to the device actor's reqCh.
type actorRequest struct {
	req     *Request
	replyCh chan *Response
}

// DeviceActor exclusively owns a session for one device. All session access
// is serialized through its event loop goroutine (run()).
type DeviceActor struct {
	serial  string
	scid    int // assigned once at construction; reused across reconnects
	session *session.Session

	reqCh chan actorRequest // unbuffered — natural backpressure

	status atomic.Value // *ActorStatus, updated after connect/reconnect

	// Reconnect throttling (read/written only from the actor goroutine, so
	// unsynchronized). After a failed reconnect we refuse to retry for
	// reconnectCooldown so a down device doesn't make every queued request
	// each pay the ~2.5s connect cost and pile up past the 35s send timeout.
	lastReconnect     time.Time
	reconnectCooldown time.Duration // 0 → use reconnectCooldown const; overridable in tests
	restartBackoff    time.Duration // 0 → use 1s default; overridable in tests
}

// reconnectCooldown is the minimum interval between reconnect attempts after
// a failure. The health ticker (10s) still drives background retries; this
// only stops request-driven reconnects from stacking.
const reconnectCooldown = 5 * time.Second

// connectDeviceFn is the connect implementation used by newDeviceActor and
// reconnect. It is a package-level variable so tests can substitute a fake
// that doesn't touch ADB. Production code uses connectDevice.
var connectDeviceFn = connectDevice

// cooldown returns the effective reconnect cooldown for this actor.
func (a *DeviceActor) cooldown() time.Duration {
	if a.reconnectCooldown > 0 {
		return a.reconnectCooldown
	}
	return reconnectCooldown
}

// newDeviceActor creates an actor for a device. It allocates a scid from the
// allocator (guaranteeing no port collision with other actors in this daemon)
// and connects synchronously so Start() can fail fast if the device is down.
//
// The allocator is borrowed: the caller owns it and must Release(actor.scid)
// when the actor is permanently removed.
func newDeviceActor(serial string, alloc *ScidAllocator) (*DeviceActor, error) {
	scid, err := alloc.Alloc()
	if err != nil {
		return nil, fmt.Errorf("allocate scid: %w", err)
	}

	a := &DeviceActor{
		serial: serial,
		scid:   scid,
		reqCh:  make(chan actorRequest),
	}

	// Initial connect
	sess, err := connectDeviceFn(serial, scid)
	if err != nil {
		alloc.Release(scid) // free the slot so a later device can reuse it
		return nil, err
	}
	a.session = sess
	a.serial = sess.Serial // update to resolved serial (important when input was empty)
	a.updateStatus(sess)

	return a, nil
}

// Scid returns the scrcpy scid this actor is bound to.
func (a *DeviceActor) Scid() int { return a.scid }

// run is the actor's event loop. It processes requests and health checks
// serially — no mutexes needed for session access.
//
// If the event loop panics, the actor is restarted (after closing the
// session) so a transient bug never leaves the device permanently
// unreachable. Restarts are bounded by a backoff to avoid hot loops.
func (a *DeviceActor) run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	// Final cleanup on real shutdown (ctx cancelled). On panic, run()
	// is re-invoked by the loop below and the session is closed there.
	defer func() {
		if a.session != nil {
			a.session.Close()
			a.session = nil
		}
		a.updateStatus(nil)
	}()

	backoff := a.restartBackoff
	if backoff == 0 {
		backoff = time.Second
	}
	for {
		restarted := a.runLoop(ctx)
		if !restarted {
			return // ctx cancelled — clean exit
		}
		// Panic path: close stale session, back off, then retry.
		if a.session != nil {
			a.session.Close()
			a.session = nil
		}
		a.updateStatus(nil)
		// NewTimer (not time.After) so we Stop it on the ctx.Done path and
		// don't leak a live timer for up to backoff when shutting down.
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// runLoop runs one iteration of the event loop. It returns true if it exited
// via a panic (caller should restart), false if it exited via ctx.Done()
// (clean shutdown).
func (a *DeviceActor) runLoop(ctx context.Context) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			phonelog.Default().Write("actor [%s] panic: %v — restarting", a.serial, r)
			panicked = true
		}
	}()

	healthTicker := time.NewTicker(10 * time.Second)
	defer healthTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false

		case req := <-a.reqCh:
			a.handleRequest(req)

		case <-healthTicker.C:
			a.healthCheck()
		}
	}
}

// handleRequest dispatches an RPC, reconnecting + retrying if the session
// is missing or died during the request.
func (a *DeviceActor) handleRequest(req actorRequest) {
	resp := Dispatch(a.session, req.req)

	// If the session is nil (prior reconnect failed) or died during this
	// request, try reconnect + retry once. This prevents a temporary
	// disconnect from failing every request until the next health tick.
	dead := a.session == nil || !a.session.IsAlive()
	if resp.Error != nil && dead {
		if a.tryReconnect() {
			resp = Dispatch(a.session, req.req)
		}
		// On throttle/failure, resp keeps the original error.
	}

	req.replyCh <- resp
}

// tryReconnect attempts a reconnect unless one was tried very recently (to
// avoid every queued request each paying the connect cost when the device is
// down). Returns true if a reconnect was attempted and succeeded.
func (a *DeviceActor) tryReconnect() bool {
	if !a.lastReconnect.IsZero() && time.Since(a.lastReconnect) < a.cooldown() {
		return false // throttled — let the health ticker drive the next attempt
	}
	a.lastReconnect = time.Now()
	phonelog.Default().Write("actor [%s]: session dead/nil, reconnecting...", a.serial)
	if err := a.reconnect(); err != nil {
		phonelog.Default().Write("actor [%s]: reconnect failed: %v", a.serial, err)
		return false
	}
	phonelog.Default().Write("actor [%s]: reconnected", a.serial)
	return true
}

// healthCheck is called periodically by the event loop. It checks liveness
// and reconnects if the session is dead. It respects the reconnect cooldown
// so the 10s ticker and request-driven reconnects don't stack.
func (a *DeviceActor) healthCheck() {
	if a.session == nil {
		phonelog.Default().Write("actor [%s]: health: no session", a.serial)
		a.tryReconnect()
		return
	}

	if !a.session.IsAlive() {
		phonelog.Default().Write("actor [%s]: health: connection dead", a.serial)
		a.tryReconnect()
	}
}

// reconnect tears down the old session and creates a new one.
// Called ONLY from the actor goroutine — no synchronization needed.
func (a *DeviceActor) reconnect() error {
	// Close old session
	if a.session != nil {
		a.session.Close()
		a.session = nil
	}

	sess, err := connectDeviceFn(a.serial, a.scid)
	if err != nil {
		a.updateStatus(nil)
		return err
	}

	a.session = sess
	a.serial = sess.Serial // keep in sync (may change if serial was auto-detected)
	a.updateStatus(sess)
	return nil
}

// updateStatus publishes a snapshot of the actor's state for fast reads.
// Pass nil session to mark the actor as disconnected.
func (a *DeviceActor) updateStatus(sess *session.Session) {
	if sess == nil {
		a.status.Store(&ActorStatus{Serial: a.serial})
		return
	}
	a.status.Store(&ActorStatus{
		Connected:    true,
		Serial:       sess.Serial,
		DeviceWidth:  sess.DeviceW,
		DeviceHeight: sess.DeviceH,
		ControlAvail: sess.IsControlAvailable(),
		UIAvail:      sess.IsUIAvailable(),
	})
}

// connectDevice resolves the serial (auto-detect if empty) and establishes
// a session. This is the shared connect logic used by both initial connect
// and reconnect.
func connectDevice(serial string, scid int) (*session.Session, error) {
	// Resolve serial if empty
	if serial == "" {
		devices, err := adb.ListDevices()
		if err != nil {
			return nil, fmt.Errorf("list devices: %w", err)
		}
		if len(devices) == 0 {
			return nil, fmt.Errorf("no devices connected")
		}
		serial = devices[0].Serial
	}

	phonelog.Default().Write("connecting to device %s (scid=%x)...", serial, scid)
	sess, err := session.Connect(serial, scid)
	if err != nil {
		return nil, fmt.Errorf("connect device: %w", err)
	}

	phonelog.Default().Write("device ready: %s (%dx%d)", serial, sess.DeviceW, sess.DeviceH)
	return sess, nil
}
