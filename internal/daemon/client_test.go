package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestListener creates a unix-socket listener at a SHORT path. The
// default t.TempDir() path on macOS exceeds the 104-byte sockaddr_un limit,
// so /tmp is tried first (same convention as SocketName()).
func newTestListener(t *testing.T) (net.Listener, string) {
	t.Helper()
	var (
		l          net.Listener
		socketPath string
	)
	for _, dir := range []string{"/tmp", os.TempDir()} {
		if dir == "" {
			continue
		}
		tdir, err := os.MkdirTemp(dir, "pf-test-")
		if err != nil {
			continue
		}
		socketPath = filepath.Join(tdir, "daemon.sock")
		l, err = net.Listen("unix", socketPath)
		if err == nil {
			t.Cleanup(func() {
				l.Close()
				os.RemoveAll(tdir)
			})
			break
		}
		os.RemoveAll(tdir)
	}
	if l == nil {
		t.Fatal("could not create unix-socket listener (tried /tmp and os.TempDir)")
	}
	return l, socketPath
}

// startTestRPCServer starts a unix-socket listener that serves one request:
// it parses the incoming JSON-RPC Request, forwards it on requests, then
// writes the first bytes received on responses back to the client. Sending a
// nil response skips the write (server just closes).
func startTestRPCServer(t *testing.T) (socketPath string, requests chan *Request, responses chan []byte) {
	t.Helper()
	l, socketPath := newTestListener(t)

	requests = make(chan *Request, 1)
	responses = make(chan []byte, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}
		requests <- &req
		if resp := <-responses; resp != nil {
			conn.Write(resp)
		}
	}()
	return socketPath, requests, responses
}

// newTestClient builds a Client pointed at a test socket (same-package
// access to the unexported fields, so no SocketName override is needed).
func newTestClient(socketPath, serial string, timeout time.Duration) *Client {
	return &Client{socketPath: socketPath, serial: serial, timeout: timeout}
}

// callAsync runs c.Call in a goroutine so the test can drive the fake server
// handshake without deadlocking on the client's blocking read.
func callAsync(c *Client, method string, params map[string]any) (<-chan json.RawMessage, <-chan error) {
	resCh := make(chan json.RawMessage, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := c.Call(method, params)
		resCh <- res
		errCh <- err
	}()
	return resCh, errCh
}

// TestClientCallRoundTripAndDeviceInjection verifies callOnce's dial +
// marshal + write + read + unmarshal path, and that the client serial is
// injected into params as "device" when not explicitly set.
func TestClientCallRoundTripAndDeviceInjection(t *testing.T) {
	socketPath, requests, responses := startTestRPCServer(t)
	c := newTestClient(socketPath, "dev-123", 5*time.Second)

	resCh, errCh := callAsync(c, "status", map[string]any{"extra": float64(1)})

	req := <-requests
	if req.Method != "status" {
		t.Errorf("server received method %q, want %q", req.Method, "status")
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("server received jsonrpc %q, want %q", req.JSONRPC, "2.0")
	}
	var params map[string]any
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal captured params: %v", err)
	}
	if params["device"] != "dev-123" {
		t.Errorf("captured params[device] = %v, want %q", params["device"], "dev-123")
	}
	if params["extra"] != float64(1) {
		t.Errorf("captured params[extra] = %v, want 1", params["extra"])
	}

	responses <- []byte(`{"jsonrpc":"2.0","result":{"connected":true,"serial":"dev-123"},"id":1}` + "\n")

	var got map[string]any
	if err := json.Unmarshal(<-resCh, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got["connected"] != true {
		t.Errorf("result connected = %v, want true", got["connected"])
	}
	if err := <-errCh; err != nil {
		t.Errorf("Call error: %v", err)
	}
}

// TestClientCallPreservesExplicitDevice verifies that an explicit "device"
// param is not overwritten by the serial injection.
func TestClientCallPreservesExplicitDevice(t *testing.T) {
	socketPath, requests, responses := startTestRPCServer(t)
	c := newTestClient(socketPath, "dev-123", 5*time.Second)

	resCh, errCh := callAsync(c, "tap", map[string]any{"device": "explicit", "x": float64(1)})

	req := <-requests
	var params map[string]any
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal captured params: %v", err)
	}
	if params["device"] != "explicit" {
		t.Errorf("captured params[device] = %v, want %q", params["device"], "explicit")
	}

	responses <- []byte(`{"jsonrpc":"2.0","result":{"message":"ok"},"id":1}` + "\n")
	<-resCh
	if err := <-errCh; err != nil {
		t.Errorf("Call error: %v", err)
	}
}

// TestClientCallSurfacesRPCError verifies a resp.Error response surfaces as
// a plain error — NOT ErrDaemonUnreachable, so Call never triggers the
// restart path for a real RPC error.
func TestClientCallSurfacesRPCError(t *testing.T) {
	socketPath, requests, responses := startTestRPCServer(t)
	c := newTestClient(socketPath, "dev-123", 5*time.Second)

	resCh, errCh := callAsync(c, "tap", map[string]any{"x": float64(1), "y": float64(2)})

	<-requests
	responses <- []byte(`{"jsonrpc":"2.0","error":{"code":-32001,"message":"no device connected"},"id":1}` + "\n")

	<-resCh
	err := <-errCh
	if err == nil {
		t.Fatal("Call returned nil error for resp.Error response")
	}
	if errors.Is(err, ErrDaemonUnreachable) {
		t.Errorf("Call error wraps ErrDaemonUnreachable, want a plain RPC error: %v", err)
	}
	if err.Error() != "no device connected" {
		t.Errorf("Call error = %q, want %q", err.Error(), "no device connected")
	}
}

// TestClientPingReturnsStatus verifies Ping decodes the status map.
func TestClientPingReturnsStatus(t *testing.T) {
	socketPath, requests, responses := startTestRPCServer(t)
	c := newTestClient(socketPath, "dev-123", 5*time.Second)

	statusCh := make(chan map[string]any, 1)
	errCh := make(chan error, 1)
	go func() {
		status, err := c.Ping()
		statusCh <- status
		errCh <- err
	}()

	req := <-requests
	if req.Method != "status" {
		t.Errorf("Ping sent method %q, want %q", req.Method, "status")
	}

	responses <- []byte(`{"jsonrpc":"2.0","result":{"connected":true,"pid":1234},"id":1}` + "\n")

	status := <-statusCh
	if status["connected"] != true {
		t.Errorf("status connected = %v, want true", status["connected"])
	}
	if err := <-errCh; err != nil {
		t.Errorf("Ping error: %v", err)
	}
}

// TestClientCallReadDeadline verifies the read deadline: a server that
// accepts and reads the request but never responds yields an error that is
// NOT ErrDaemonUnreachable (the daemon was reachable; it just didn't reply).
func TestClientCallReadDeadline(t *testing.T) {
	l, socketPath := newTestListener(t)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		bufio.NewReader(conn).ReadBytes('\n')
		<-release // hold the connection open without replying
	}()

	c := newTestClient(socketPath, "dev-123", 200*time.Millisecond)
	if _, err := c.Call("status", nil); err == nil {
		t.Fatal("Call returned nil error when the server never responded")
	} else if errors.Is(err, ErrDaemonUnreachable) {
		t.Errorf("read-deadline error wraps ErrDaemonUnreachable: %v", err)
	}
}

// newUnreachableSocketPath returns a socket path that does NOT exist yet
// (no listener), plus a cleanup that removes anything created at it. The
// client's first dial must fail with ErrDaemonUnreachable.
func newUnreachableSocketPath(t *testing.T) (socketPath string) {
	t.Helper()
	var tdir string
	for _, dir := range []string{"/tmp", os.TempDir()} {
		if dir == "" {
			continue
		}
		d, err := os.MkdirTemp(dir, "pf-test-")
		if err == nil {
			tdir = d
			break
		}
	}
	if tdir == "" {
		t.Fatal("could not create temp dir for unreachable socket")
	}
	socketPath = filepath.Join(tdir, "daemon.sock")
	t.Cleanup(func() { os.RemoveAll(tdir) })
	return socketPath
}

// TestClientCallRecoversViaEnsurer verifies the mid-RPC self-heal path:
// ErrDaemonUnreachable → ensurer restart → exactly one retry that returns
// the server's result. The ensurer must be invoked at most once per Call.
func TestClientCallRecoversViaEnsurer(t *testing.T) {
	socketPath := newUnreachableSocketPath(t)
	c := newTestClient(socketPath, "dev-123", 5*time.Second)

	var (
		ensurerCalls int
		reqs         = make(chan *Request, 2)
	)
	c.ensurer = func() error {
		ensurerCalls++
		l, err := net.Listen("unix", socketPath)
		if err != nil {
			return err
		}
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				go func() {
					defer conn.Close()
					line, err := bufio.NewReader(conn).ReadBytes('\n')
					if err != nil {
						return
					}
					var req Request
					if err := json.Unmarshal(line, &req); err != nil {
						return
					}
					reqs <- &req
					conn.Write([]byte(`{"jsonrpc":"2.0","result":{"ok":true},"id":1}` + "\n"))
				}()
			}
		}()
		return nil
	}

	result, err := c.Call("status", nil)
	if err != nil {
		t.Fatalf("Call after ensurer restart: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("result = %v, want ok=true from the retried request", got)
	}
	if ensurerCalls != 1 {
		t.Errorf("ensurer invoked %d times, want exactly 1", ensurerCalls)
	}
	// The failed dial must NOT have reached the server: only the retry does.
	if n := len(reqs); n != 1 {
		t.Errorf("server saw %d requests, want exactly 1 (the retry)", n)
	}
	req := <-reqs
	if req.Method != "status" {
		t.Errorf("retried request method = %q, want %q", req.Method, "status")
	}
}

// TestClientCallEnsurerErrorSurfaced verifies that when the ensurer fails,
// Call returns the ensurer's error and does NOT retry the request.
func TestClientCallEnsurerErrorSurfaced(t *testing.T) {
	socketPath := newUnreachableSocketPath(t)
	c := newTestClient(socketPath, "dev-123", 5*time.Second)

	ensurerErr := errors.New("restart failed")
	ensurerCalls := 0
	c.ensurer = func() error {
		ensurerCalls++
		return ensurerErr
	}

	_, err := c.Call("status", nil)
	if err == nil {
		t.Fatal("Call returned nil error when the ensurer failed")
	}
	if !errors.Is(err, ensurerErr) {
		t.Errorf("Call error = %v, want the ensurer's error %v", err, ensurerErr)
	}
	if ensurerCalls != 1 {
		t.Errorf("ensurer invoked %d times, want exactly 1 (no retry loop)", ensurerCalls)
	}
	// No listener was ever created at socketPath, so any retry would have
	// returned ErrDaemonUnreachable — the ensurer error surfacing proves
	// callOnce was not re-run.
}

// TestClientCallNoEnsurerReturnsUnreachable verifies that without an
// ensurer, an unreachable daemon surfaces ErrDaemonUnreachable as-is.
func TestClientCallNoEnsurerReturnsUnreachable(t *testing.T) {
	socketPath := newUnreachableSocketPath(t)
	c := newTestClient(socketPath, "dev-123", 5*time.Second)

	_, err := c.Call("status", nil)
	if err == nil {
		t.Fatal("Call returned nil error for an unreachable daemon")
	}
	if !errors.Is(err, ErrDaemonUnreachable) {
		t.Errorf("Call error = %v, want errors.Is(err, ErrDaemonUnreachable)", err)
	}
}

// TestClientCallEnsurerSucceedsButRetryStillUnreachable covers the realistic
// race where the ensurer reports success (returns nil) but the daemon socket
// is not actually listening yet, so the single retry's dial STILL fails.
// Call must surface that second ErrDaemonUnreachable — not loop, not mask it.
func TestClientCallEnsurerSucceedsButRetryStillUnreachable(t *testing.T) {
	socketPath := newUnreachableSocketPath(t)
	c := newTestClient(socketPath, "dev-123", 5*time.Second)

	ensurerCalls := 0
	c.ensurer = func() error {
		ensurerCalls++
		return nil // claims the daemon is up, but creates no listener
	}

	_, err := c.Call("status", nil)
	if err == nil {
		t.Fatal("Call returned nil error when the retried dial failed")
	}
	if !errors.Is(err, ErrDaemonUnreachable) {
		t.Errorf("Call error = %v, want errors.Is(err, ErrDaemonUnreachable)", err)
	}
	if ensurerCalls != 1 {
		t.Errorf("ensurer invoked %d times, want exactly 1", ensurerCalls)
	}
}
