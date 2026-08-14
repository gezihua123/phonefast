package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// newTestDaemon builds a Daemon wired for handleConn tests: real dispatcher,
// fake connect (never touches a device), cancellable context.
func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := New(Config{})
	d.connectFn = func(s string, scid int) (Device, error) {
		return nil, fmt.Errorf("test connect disabled")
	}
	d.ctx, d.cancel = testCtx(t)
	return d
}

// handleConnRoundTrip drives d.handleConn over a net.Pipe with the given
// raw JSON-RPC request line and returns the parsed response.
func handleConnRoundTrip(t *testing.T, d *Daemon, ctx context.Context, raw string) *Response {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()

	d.wg.Add(1)
	go d.handleConn(ctx, server)

	if err := client.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set write deadline: %v", err)
	}
	if _, err := client.Write([]byte(raw)); err != nil {
		t.Fatalf("write request: %v", err)
	}

	if err := client.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	line, err := bufio.NewReader(client).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", line, err)
	}
	return &resp
}

// TestHandleConnParseError verifies a malformed request yields ErrParse.
func TestHandleConnParseError(t *testing.T) {
	d := newTestDaemon(t)
	resp := handleConnRoundTrip(t, d, d.ctx, "this is not json\n")
	if resp.Error == nil || resp.Error.Code != ErrParse {
		t.Fatalf("malformed request error = %+v, want ErrParse", resp.Error)
	}
}

// TestHandleConnNoDevice verifies a device-bound method without a device
// param (and no usable ADB auto-detect) yields ErrNoDevice. The injected
// connectFn fails, so even a machine with ADB devices attached cannot
// accidentally connect.
func TestHandleConnNoDevice(t *testing.T) {
	d := newTestDaemon(t)
	raw := fmt.Sprintf(`{"jsonrpc":"2.0","method":"%s","params":{"x":1,"y":2},"id":7}`+"\n", protocol.MethodTap)
	resp := handleConnRoundTrip(t, d, d.ctx, raw)
	if resp.Error == nil || resp.Error.Code != ErrNoDevice {
		t.Fatalf("device-less tap error = %+v, want ErrNoDevice", resp.Error)
	}
}

// TestHandleConnStatusConnectionless verifies "status" without a device
// reports daemon-level info and never binds a session.
func TestHandleConnStatusConnectionless(t *testing.T) {
	d := newTestDaemon(t)
	resp := handleConnRoundTrip(t, d, d.ctx, `{"jsonrpc":"2.0","method":"status","id":7}`+"\n")
	if resp.Error != nil {
		t.Fatalf("status error: %v", resp.Error)
	}
	var info map[string]any
	if err := json.Unmarshal(resp.Result, &info); err != nil {
		t.Fatalf("unmarshal status result: %v", err)
	}
	if info["connected"] != false {
		t.Errorf("connected = %v, want false (no actors)", info["connected"])
	}
	if n, ok := info["device_count"].(float64); !ok || n != 0 {
		t.Errorf("device_count = %v, want 0", info["device_count"])
	}
	if pid, ok := info["pid"].(float64); !ok || pid <= 0 {
		t.Errorf("pid = %v, want a positive number", info["pid"])
	}
}

// TestHandleConnStatusReportsConnectedActor verifies writeDaemonStatus
// snapshot consistency: a registered connected actor appears in
// device_count, devices, and connected_devices from the same snapshot.
func TestHandleConnStatusReportsConnectedActor(t *testing.T) {
	d := newTestDaemon(t)
	a := &DeviceActor{serial: "dev-A", reqCh: make(chan actorRequest)}
	a.status.Store(&ActorStatus{Connected: true, Serial: "dev-A", DeviceWidth: 1080, DeviceHeight: 2400})
	d.devices["dev-A"] = a

	resp := handleConnRoundTrip(t, d, d.ctx, `{"jsonrpc":"2.0","method":"status","id":7}`+"\n")
	if resp.Error != nil {
		t.Fatalf("status error: %v", resp.Error)
	}
	var info map[string]any
	if err := json.Unmarshal(resp.Result, &info); err != nil {
		t.Fatalf("unmarshal status result: %v", err)
	}
	if info["connected"] != true {
		t.Errorf("connected = %v, want true", info["connected"])
	}
	if n, ok := info["device_count"].(float64); !ok || n != 1 {
		t.Errorf("device_count = %v, want 1", info["device_count"])
	}
	devices, ok := info["devices"].([]any)
	if !ok || len(devices) != 1 || devices[0] != "dev-A" {
		t.Errorf("devices = %v, want [dev-A]", info["devices"])
	}
	connected, ok := info["connected_devices"].([]any)
	if !ok || len(connected) != 1 {
		t.Fatalf("connected_devices = %v, want 1 entry", info["connected_devices"])
	}
	first, _ := connected[0].(map[string]any)
	if first["serial"] != "dev-A" {
		t.Errorf("connected_devices[0].serial = %v, want dev-A", first["serial"])
	}
}

// TestHandleConnWaitDefaultAndCap verifies the wait default (no
// duration_ms → DefaultWaitMs, checked before the cap is shrunk so the
// default itself isn't clamped) and the maxWaitMs cap (shrunk so the branch
// runs instantly).
func TestHandleConnWaitDefaultAndCap(t *testing.T) {
	d := newTestDaemon(t)

	resp := handleConnRoundTrip(t, d, d.ctx, `{"jsonrpc":"2.0","method":"wait","id":7}`+"\n")
	if resp.Error != nil {
		t.Fatalf("wait (default) error: %v", resp.Error)
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal wait result: %v", err)
	}
	if msg, _ := out["message"].(string); msg != protocol.FormatWaitResult(protocol.DefaultWaitMs) {
		t.Errorf("default wait message = %q, want %q", msg, protocol.FormatWaitResult(protocol.DefaultWaitMs))
	}

	old := maxWaitMs
	maxWaitMs = 50
	defer func() { maxWaitMs = old }()

	resp = handleConnRoundTrip(t, d, d.ctx, `{"jsonrpc":"2.0","method":"wait","params":{"duration_ms":100000},"id":8}`+"\n")
	if resp.Error != nil {
		t.Fatalf("wait (capped) error: %v", resp.Error)
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal capped wait result: %v", err)
	}
	if msg, _ := out["message"].(string); msg != protocol.FormatWaitResult(50) {
		t.Errorf("capped wait message = %q, want %q", msg, protocol.FormatWaitResult(50))
	}
}

// TestHandleConnWaitInterruptedByShutdown verifies a long wait responds
// immediately (rather than sleeping) when the daemon context is cancelled.
func TestHandleConnWaitInterruptedByShutdown(t *testing.T) {
	d := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancel() // daemon already shutting down

	start := time.Now()
	resp := handleConnRoundTrip(t, d, ctx, `{"jsonrpc":"2.0","method":"wait","params":{"duration_ms":5000},"id":7}`+"\n")
	elapsed := time.Since(start)
	if resp.Error != nil {
		t.Fatalf("wait (interrupted) error: %v", resp.Error)
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal interrupted wait result: %v", err)
	}
	if msg, _ := out["message"].(string); msg != protocol.FormatWaitResult(5000) {
		t.Errorf("interrupted wait message = %q, want %q", msg, protocol.FormatWaitResult(5000))
	}
	if elapsed > 2*time.Second {
		t.Errorf("wait took %v — ctx.Done did not interrupt the sleep", elapsed)
	}
}

// newBusyActor registers an actor whose reqCh has no reader, so handleConn's
// send can never complete (the actor loop is intentionally not running).
func newBusyActor(t *testing.T, d *Daemon, serial string) *DeviceActor {
	t.Helper()
	a := &DeviceActor{serial: serial, reqCh: make(chan actorRequest)}
	d.devices[serial] = a
	return a
}

// TestHandleConnActorBusyTimeout verifies the send timeout: an actor that
// cannot accept a request (reqCh never consumed) yields ErrTimeout within
// the (shrunk) window instead of hanging.
func TestHandleConnActorBusyTimeout(t *testing.T) {
	d := newTestDaemon(t)
	old := actorSendTimeout
	actorSendTimeout = 50 * time.Millisecond
	defer func() { actorSendTimeout = old }()
	newBusyActor(t, d, "dev-X")

	raw := fmt.Sprintf(`{"jsonrpc":"2.0","method":"%s","params":{"device":"dev-X","x":1,"y":2},"id":7}`+"\n", protocol.MethodTap)
	start := time.Now()
	resp := handleConnRoundTrip(t, d, d.ctx, raw)
	elapsed := time.Since(start)
	if resp.Error == nil || resp.Error.Code != ErrTimeout {
		t.Fatalf("busy-actor error = %+v, want ErrTimeout", resp.Error)
	}
	if elapsed > 2*time.Second {
		t.Errorf("busy-actor timeout took %v, want ~50ms (shrunk send window)", elapsed)
	}
}

// newHungReplyActor registers an actor that consumes ONE request off reqCh
// and then never writes the reply, so handleConn's reply wait can only end
// via the reply timeout. This is the reply-phase counterpart of newBusyActor
// (which covers the send phase): here the actor DID accept the request.
func newHungReplyActor(t *testing.T, d *Daemon, serial string) *DeviceActor {
	t.Helper()
	a := &DeviceActor{serial: serial, reqCh: make(chan actorRequest)}
	d.devices[serial] = a
	go func() {
		<-a.reqCh // consume the request, then hang — no replyCh write
	}()
	return a
}

// TestHandleConnReplyTimeout verifies the reply timeout: an actor that
// accepts the request off reqCh but never writes to replyCh yields ErrTimeout
// within the (shrunk) reply window instead of hanging forever.
func TestHandleConnReplyTimeout(t *testing.T) {
	d := newTestDaemon(t)
	old := actorReplyTimeout
	actorReplyTimeout = 50 * time.Millisecond
	defer func() { actorReplyTimeout = old }()
	newHungReplyActor(t, d, "dev-Y")

	raw := fmt.Sprintf(`{"jsonrpc":"2.0","method":"%s","params":{"device":"dev-Y","x":1,"y":2},"id":7}`+"\n", protocol.MethodTap)
	start := time.Now()
	resp := handleConnRoundTrip(t, d, d.ctx, raw)
	elapsed := time.Since(start)
	if resp.Error == nil || resp.Error.Code != ErrTimeout {
		t.Fatalf("hung-actor error = %+v, want ErrTimeout", resp.Error)
	}
	if elapsed > 2*time.Second {
		t.Errorf("reply timeout took %v, want ~50ms (shrunk reply window)", elapsed)
	}
}

// TestHandleConnDeviceRoundTrip exercises the success round-trip for a
// device-bound method: read request → getOrCreateActor → send on actor.reqCh
// → actor dispatches against the device → reply written back. The stub actor
// loop drains reqCh through the real Dispatcher, so the full routing path a
// normal tap takes through the daemon is covered end-to-end.
func TestHandleConnDeviceRoundTrip(t *testing.T) {
	d := newTestDaemon(t)

	dev := newFakeDevice("dev-R")
	var tappedX, tappedY int
	dev.tapFn = func(x, y int) error { tappedX, tappedY = x, y; return nil }

	a := &DeviceActor{serial: "dev-R", reqCh: make(chan actorRequest), device: dev, dispatch: d.dispatcher}
	d.devices["dev-R"] = a
	t.Cleanup(func() { close(a.reqCh) })
	go func() {
		for ar := range a.reqCh {
			ar.replyCh <- a.dispatch.Dispatch(a.device, ar.req)
		}
	}()

	raw := fmt.Sprintf(`{"jsonrpc":"2.0","method":"%s","params":{"device":"dev-R","x":3,"y":4},"id":42}`+"\n", protocol.MethodTap)
	resp := handleConnRoundTrip(t, d, d.ctx, raw)
	if resp.Error != nil {
		t.Fatalf("tap round-trip error: %+v", resp.Error)
	}
	if resp.ID != 42 {
		t.Errorf("response ID = %d, want 42 (request ID must round-trip)", resp.ID)
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal tap result: %v", err)
	}
	if msg, _ := out["message"].(string); msg != "Tapped at (3, 4)" {
		t.Errorf("tap message = %q, want %q", msg, "Tapped at (3, 4)")
	}
	if tappedX != 3 || tappedY != 4 {
		t.Errorf("device received tap (%d, %d), want (3, 4)", tappedX, tappedY)
	}
}

// TestHandleConnShutdownMidRequest verifies ctx cancellation while waiting
// for an actor reply yields ErrInternal instead of hanging for the reply.
func TestHandleConnShutdownMidRequest(t *testing.T) {
	d := newTestDaemon(t)
	newBusyActor(t, d, "dev-X")

	raw := fmt.Sprintf(`{"jsonrpc":"2.0","method":"%s","params":{"device":"dev-X","x":1,"y":2},"id":7}`+"\n", protocol.MethodTap)
	server, client := net.Pipe()
	defer client.Close()

	// Start handleConn, let it reach the blocked send, then cancel.
	go func() {
		time.Sleep(50 * time.Millisecond)
		d.cancel()
	}()
	d.wg.Add(1)
	go d.handleConn(d.ctx, server)

	if err := client.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set write deadline: %v", err)
	}
	if _, err := client.Write([]byte(raw)); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	line, err := bufio.NewReader(client).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", line, err)
	}
	if resp.Error == nil || resp.Error.Code != ErrInternal {
		t.Fatalf("shutdown-mid-request error = %+v, want ErrInternal", resp.Error)
	}
}

// TestHandleConnConnectSuccess verifies the connectionless "connect" branch
// creates an actor and reports its serial.
func TestHandleConnConnectSuccess(t *testing.T) {
	d := newTestDaemon(t)
	d.connectFn = func(s string, scid int) (Device, error) { return newFakeDevice(s), nil }

	raw := fmt.Sprintf(`{"jsonrpc":"2.0","method":"%s","params":{"device":"dev-C"},"id":7}`+"\n", protocol.MethodConnect)
	resp := handleConnRoundTrip(t, d, d.ctx, raw)
	if resp.Error != nil {
		t.Fatalf("connect error: %v", resp.Error)
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("unmarshal connect result: %v", err)
	}
	if serial, _ := out["serial"].(string); serial != "dev-C" {
		t.Errorf("connect serial = %q, want %q", serial, "dev-C")
	}
	if len(d.devices) != 1 {
		t.Errorf("devices map has %d entries, want 1 (connect must register the actor)", len(d.devices))
	}
}

// TestHandleConnDisconnectUnknownDevice verifies disconnect of an unmanaged
// device yields ErrNoDevice.
func TestHandleConnDisconnectUnknownDevice(t *testing.T) {
	d := newTestDaemon(t)
	raw := fmt.Sprintf(`{"jsonrpc":"2.0","method":"%s","params":{"device":"dev-missing"},"id":7}`+"\n", protocol.MethodDisconnect)
	resp := handleConnRoundTrip(t, d, d.ctx, raw)
	if resp.Error == nil || resp.Error.Code != ErrNoDevice {
		t.Fatalf("disconnect unknown error = %+v, want ErrNoDevice", resp.Error)
	}
}
