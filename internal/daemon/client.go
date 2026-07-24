package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrDaemonUnreachable is returned by Client.Call when the daemon's Unix socket
// cannot be dialed — i.e. no daemon process is listening. Client.Call itself
// recovers from this when an ensurer is configured (see SetEnsurer); callers
// only see it if recovery fails or no ensurer is set.
var ErrDaemonUnreachable = errors.New("daemon unreachable")

// SocketName returns the Unix socket path for the unified daemon.
// All devices share a single daemon and socket; the target device is selected
// via the "device" field in each RPC request, not the socket path.
func SocketName() string {
	return fmt.Sprintf("/tmp/phonefast-%d.sock", os.Getuid())
}

// PidFileName returns the PID file path for the unified daemon.
func PidFileName() string {
	return fmt.Sprintf("/tmp/phonefast-%d.pid", os.Getuid())
}

// ensurerClient is the shared restart-on-unreachable state for all Clients on
// the same socket path. It collapses concurrent unreachable errors into a
// single restart attempt so N simultaneous failed calls don't spawn N daemons.
var (
	globalEnsurerMu      sync.Mutex
	globalEnsurer        func() error
	globalRestartInFlight bool
)

// SetEnsurer installs a process-wide callback the Client invokes when the
// daemon is unreachable (its socket won't dial). The callback should start the
// daemon and return nil once it's listening (or return an error). Concurrent
// unreachable calls share one restart attempt — the callback runs at most once
// per concurrent burst. Long-lived callers (the MCP server) use this to
// self-heal after a daemon crash; short-lived CLI calls don't need it because
// ensureDaemon runs at startup.
func SetEnsurer(fn func() error) {
	globalEnsurerMu.Lock()
	globalEnsurer = fn
	globalEnsurerMu.Unlock()
}

// Client talks to the unified daemon over a Unix socket. The serial is
// injected into every RPC params as "device" so the daemon can route the
// request to the correct per-device session.
type Client struct {
	socketPath string
	serial     string
	timeout    time.Duration
}

// NewClient creates a client for the unified daemon, bound to the given
// device serial. The serial is sent with every RPC call so the daemon can
// route the request to the correct DeviceActor.
func NewClient(serial string) *Client {
	return &Client{
		socketPath: SocketName(),
		serial:     serial,
		timeout:    30 * time.Second,
	}
}

// Call sends a JSON-RPC request to the daemon and returns the result.
//
// If the daemon is unreachable and an ensurer is configured (SetEnsurer),
// Call restarts the daemon once — deduplicated across concurrent callers — and
// retries the request a single time. Regular RPC errors (device down, bad
// params) are returned as-is and never trigger a restart.
func (c *Client) Call(method string, params map[string]any) (json.RawMessage, error) {
	result, err := c.callOnce(method, params)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, ErrDaemonUnreachable) {
		return nil, err
	}

	// Daemon down. Run the ensurer at most once for this burst of concurrent
	// unreachable callers, then retry once.
	if restartErr := c.ensureDaemonOnce(); restartErr != nil {
		return nil, restartErr
	}
	return c.callOnce(method, params)
}

// ensureDaemonOnce invokes the configured ensurer, but if another goroutine is
// already restarting the daemon, waits for it instead of spawning a second
// restart. Returns nil if the daemon is (now) up, or the ensurer's error.
func (c *Client) ensureDaemonOnce() error {
	globalEnsurerMu.Lock()
	ensurer := globalEnsurer
	if ensurer == nil {
		globalEnsurerMu.Unlock()
		return ErrDaemonUnreachable
	}
	// Another caller is already restarting — wait for it.
	for globalRestartInFlight {
		globalEnsurerMu.Unlock()
		time.Sleep(50 * time.Millisecond)
		globalEnsurerMu.Lock()
	}
	// Re-check: the prior restart may have succeeded while we waited.
	if _, statErr := os.Stat(c.socketPath); statErr == nil {
		globalEnsurerMu.Unlock()
		return nil
	}
	globalRestartInFlight = true
	globalEnsurerMu.Unlock()
	defer func() {
		globalEnsurerMu.Lock()
		globalRestartInFlight = false
		globalEnsurerMu.Unlock()
	}()

	return ensurer()
}

// callOnce performs a single dial + request, with no retry logic.
func (c *Client) callOnce(method string, params map[string]any) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("daemon not running (is '%s daemon' started?): %w (hint: %v)",
			filepath.Base(os.Args[0]), ErrDaemonUnreachable, err)
	}
	defer conn.Close()

	req := Request{
		JSONRPC: "2.0",
		Method:  method,
		ID:      1,
	}
	if params == nil {
		params = make(map[string]any)
	}
	// Inject device serial into every RPC request so the daemon can route
	// to the correct DeviceActor. Respect explicit "device" if already set.
	if _, hasDevice := params["device"]; !hasDevice {
		params["device"] = c.serial
	}
	data, _ := json.Marshal(params)
	req.Params = data

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	reqBytes = append(reqBytes, '\n')

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(c.timeout))
	reader := bufio.NewReader(conn)
	respBytes, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}

	return resp.Result, nil
}

// Ping checks if the daemon is reachable and returns its status.
func (c *Client) Ping() (map[string]any, error) {
	result, err := c.Call("status", nil)
	if err != nil {
		return nil, err
	}
	var status map[string]any
	if err := json.Unmarshal(result, &status); err != nil {
		return nil, fmt.Errorf("unmarshal status: %w", err)
	}
	return status, nil
}
