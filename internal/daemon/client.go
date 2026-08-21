package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// ErrDaemonUnreachable is returned by Client.Call when the daemon's Unix socket
// cannot be dialed — i.e. no daemon process is listening. Client.Call itself
// recovers from this when an ensurer is configured (see WithEnsurer); callers
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

// ClientOption configures a Client at construction.
type ClientOption func(*Client)

// WithEnsurer installs a daemon-restart callback the Client invokes when the
// daemon is unreachable (its socket won't dial). The callback should start
// the daemon and return nil once it's listening (or return an error).
// Concurrent-restart deduplication is the ensurer's own responsibility —
// Supervisor.EnsureRunning provides it (all clients in a process share one
// Supervisor instance, so N simultaneous unreachable calls collapse into one
// restart attempt).
func WithEnsurer(fn func() error) ClientOption {
	return func(c *Client) { c.ensurer = fn }
}

// Client talks to the unified daemon over a Unix socket. The serial is
// injected into every RPC params as "device" so the daemon can route the
// request to the correct per-device session.
type Client struct {
	socketPath string
	serial     string
	timeout    time.Duration
	ensurer    func() error // optional daemon-restart callback (WithEnsurer)
}

// NewClient creates a client for the unified daemon, bound to the given
// device serial. The serial is sent with every RPC call so the daemon can
// route the request to the correct DeviceActor.
func NewClient(serial string, opts ...ClientOption) *Client {
	c := &Client{
		socketPath: SocketName(),
		serial:     serial,
		// The read deadline must exceed the daemon's actorReplyTimeout (60s)
		// plus write/read slop: when a request runs long, the daemon itself
		// answers with "request timed out", and the client must wait for that
		// answer rather than give up first. A client that gives up first
		// leaves the request executing — and for non-idempotent methods like
		// type_text, an agent-level retry then replays it (typed twice).
		timeout: 90 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Call sends a JSON-RPC request to the daemon and returns the result.
//
// If the daemon is unreachable and an ensurer is configured (WithEnsurer),
// Call restarts the daemon once and retries the request a single time.
// Regular RPC errors (device down, bad params) are returned as-is and never
// trigger a restart.
func (c *Client) Call(method string, params map[string]any) (json.RawMessage, error) {
	result, err := c.callOnce(method, params)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, ErrDaemonUnreachable) {
		return nil, err
	}

	// Daemon down. Run the ensurer (if any), then retry once.
	if c.ensurer == nil {
		return nil, err
	}
	if restartErr := c.ensurer(); restartErr != nil {
		return nil, restartErr
	}
	return c.callOnce(method, params)
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

// GetClipboard returns the device clipboard cached by the daemon. observed is
// false when no clipboard change has been observed since the daemon connected
// — the text is then unknown, not empty.
func (c *Client) GetClipboard() (clipboard string, observed bool, err error) {
	result, err := c.Call(protocol.MethodGetClipboard, nil)
	if err != nil {
		return "", false, err
	}
	var res struct {
		Clipboard string `json:"clipboard"`
		Observed  bool   `json:"observed"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		return "", false, fmt.Errorf("unmarshal clipboard: %w", err)
	}
	return res.Clipboard, res.Observed, nil
}
